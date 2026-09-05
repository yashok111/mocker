import { useEffect, useMemo, useRef, useState } from "react";
import {
  Alert,
  Anchor,
  Badge,
  Button,
  Code,
  Group,
  Loader,
  ScrollArea,
  Stack,
  Table,
  Text,
  Title,
} from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertTriangle } from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import {
  getListTrafficQueryKey,
  getPollTrafficQueryKey,
  useClearTraffic,
  useListTraffic,
  usePollTraffic,
  useTrafficToEndpoint,
  useTrafficToOverride,
} from "@/api/generated/traffic/traffic.ts";
import { getGetWorkspaceQueryKey } from "@/api/generated/workspaces/workspaces.ts";
import {
  getGetOperationOverrideQueryKey,
  getListWorkspaceOperationsQueryKey,
} from "@/api/generated/operations/operations.ts";
import { getListEndpointsQueryKey } from "@/api/generated/endpoints/endpoints.ts";
import type { TrafficPollView, TrafficRow } from "@/api/generated/schemas";
import { describeApiFailure } from "@/api/errors";
import { openTrafficStream, probeStreamRefusal } from "@/api/stream";

// TrafficPage is DESIGN §14 screen 8. Since P6a (decisions.md mocker-p6a-sse
// D19) the feed has THREE producers into one id-keyed row state: the tail
// (an ordinary list, newest first, no cursor), the poll (a since-cursor
// route, the fallback) and the stream (Server-Sent Events over the same
// cursor, the default). The merge is not rewritten for the third — it is
// keyed by id, so a row that arrives twice collapses to one, which is also
// what makes the fallback's overlap of one poll interval harmless.
//
// The transport state machine, in full. On mount the screen polls until the
// tail seeds the cursor, then opens an EventSource from that cursor. The
// FIRST traffic frame makes it live: the poll is disabled and the badge
// reads «живой поток». ANY EventSource error — before the first frame (a
// 501, a 503, a proxy that cannot stream) or after it (the server
// restarting, a proxy cutting the connection, the server's own 900-second
// lifetime expiring) — closes the stream, enters the fallback (the poll at
// its 2 s interval, badge «опрос каждые 2 с»), and issues one bounded probe
// of the same URL ONLY to classify the reason shown beside the badge. While
// in fallback a replacement stream is opened every streamRetryMs (60 s),
// and the poll stays on until THAT stream delivers its first frame — never
// on the probe's say-so, because the probe proves a handshake would be
// accepted, not that a feed is running. Clearing the log bumps the
// generation, which closes the stream and reopens it from the new tail's
// cursor; the server learns nothing about the clear and needs to (D20).

const TAIL_LIMIT = 50;
const POLL_LIMIT = 200;
const POLL_INTERVAL_MS = 2000;
const RATE_INTERVAL_MS = 10000;
const MAX_ROWS_KEPT = 500;
/** How often, in fallback, a replacement stream is tried (D19's own 60 s —
 * a client constant, not configuration; see CARVE-OUTS.md). */
const STREAM_RETRY_MS = 60000;

type Transport = "connecting" | "live" | "fallback";

/** notes is a comma-separated token list; a token must match a WHOLE entry,
 * never a substring — free text in another token must not be able to forge
 * "redacted" by containing it (internal/traffic/traffic.go:158-165). */
function hasNoteToken(notes: string | undefined, token: string): boolean {
  return (notes ?? "")
    .split(",")
    .map((entry) => entry.trim())
    .includes(token);
}

/** The per-row `dropped:<n>` note IS local to this row, unlike the process-wide
 * counter the wire envelope carries (§3.5 fact 2) — worth surfacing per row. */
function droppedNoteCount(notes: string | undefined): number | null {
  const token = (notes ?? "")
    .split(",")
    .map((entry) => entry.trim())
    .find((entry) => entry.startsWith("dropped:"));
  if (token === undefined) {
    return null;
  }
  const n = Number(token.slice("dropped:".length));
  return Number.isFinite(n) ? n : null;
}

/** Why «Создать правку из этого ответа» is refused, or null when it is not.
 * Order matches the server's own refusal order (§3.5 fact 5) so the reason
 * shown is the first one that would actually fire. */
function overrideDisabledReason(row: TrafficRow): string | null {
  if (row.matchedKind !== "operation" || row.matchedId == null) {
    return "запрос не сматчился с операцией спеки — пинить правку не на что";
  }
  if (row.truncated) {
    return "тело обрезано при записи — это не то, что реально ушло по сети";
  }
  if (hasNoteToken(row.notes, "redacted")) {
    return "тело было отредактировано при записи (redacted)";
  }
  if (hasNoteToken(row.notes, "suppressed")) {
    return "тело не записывалось для этого пути (suppressed) — например, путь логина";
  }
  if (row.status === 204 || row.status === 205) {
    return `у ответа со статусом ${row.status} нет тела — пинить нечего`;
  }
  return null;
}

/** Why «Создать endpoint из этого запроса» is refused. Deliberately NOT the
 * same predicate as above — this one has no matchedKind requirement, since it
 * creates a route rather than editing an existing operation (§3.5 fact 5). */
function endpointDisabledReason(row: TrafficRow): string | null {
  if (row.truncated) {
    return "тело обрезано при записи — это не то, что реально ушло по сети";
  }
  if (hasNoteToken(row.notes, "redacted")) {
    return "тело было отредактировано при записи (redacted)";
  }
  if (hasNoteToken(row.notes, "suppressed")) {
    return "тело не записывалось для этого пути (suppressed) — например, путь логина";
  }
  return null;
}

/** Merges incoming rows into the id-keyed map (a duplicate id collapses to
 * one entry) and trims to the newest MAX_ROWS_KEPT by id — enforced on the
 * stored state itself, not just at render, so the map cannot grow without
 * bound across an unattended session. */
function mergeRows(
  prev: Record<number, TrafficRow>,
  incoming: readonly TrafficRow[],
): Record<number, TrafficRow> {
  if (incoming.length === 0) {
    return prev;
  }
  const merged = { ...prev };
  for (const row of incoming) {
    merged[row.id] = row;
  }
  const ids = Object.keys(merged)
    .map(Number)
    .sort((a, b) => b - a);
  if (ids.length <= MAX_ROWS_KEPT) {
    return merged;
  }
  const trimmed: Record<number, TrafficRow> = {};
  for (const id of ids.slice(0, MAX_ROWS_KEPT)) {
    trimmed[id] = merged[id]!;
  }
  return trimmed;
}

function formatTime(ts: string): string {
  const d = new Date(ts);
  return Number.isNaN(d.getTime()) ? ts : d.toLocaleString();
}

/** DESIGN's reason for a source column: a colleague's e2e run hitting this
 * workspace must not read as a ghost row with no origin. */
function formatSource(row: TrafficRow): string {
  if (row.fwdIp !== undefined && row.fwdIp !== "") {
    return row.peerIp !== undefined && row.peerIp !== ""
      ? `${row.fwdIp} (via ${row.peerIp})`
      : row.fwdIp;
  }
  return row.peerIp !== undefined && row.peerIp !== "" ? row.peerIp : "—";
}

export function TrafficPage({
  id,
  streamRetryMs = STREAM_RETRY_MS,
}: {
  id: number;
  /** Test seam only: the fallback's retry interval. */
  streamRetryMs?: number;
}) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();

  // Local merged feed. `generation` is bumped by a successful clear and folded
  // into every query key below: an in-flight request dispatched under the OLD
  // generation resolves into an orphaned cache entry under the OLD key, which
  // this component's hooks — now subscribed to the NEW generation's key — never
  // read from. That is what "drop any response whose generation is stale"
  // means in a react-query world: the generation IS the key that a request was
  // captured under when it went out, and only same-generation responses ever
  // reach the merge effects below. queryClient.cancelQueries (in the clear
  // handler) adds a second layer that also stops same-key in-flight requests.
  const [generation, setGeneration] = useState(0);
  const [rows, setRows] = useState<Record<number, TrafficRow>>({});
  const [cursor, setCursor] = useState<number | null>(null);
  const [expanded, setExpanded] = useState<number | null>(null);
  const [rowErrors, setRowErrors] = useState<Record<number, string>>({});
  const [transport, setTransport] = useState<Transport>("connecting");
  const [fallbackReason, setFallbackReason] = useState<string | null>(null);
  const [streamDropped, setStreamDropped] = useState<number | null>(null);
  // The stream is opened from the cursor ONCE per generation; every frame
  // moves the cursor, and re-running the effect on each move would reopen
  // the connection per frame. The ref carries the latest cursor into the
  // effect without being one of its dependencies.
  const cursorRef = useRef<number | null>(null);
  cursorRef.current = cursor;
  const live = transport === "live";

  const tailParams = { limit: TAIL_LIMIT };
  const tail = useListTraffic(id, tailParams, {
    query: { queryKey: [...getListTrafficQueryKey(id, tailParams), generation] },
  });

  // rate1m lives ONLY on the list route (§3.5 fact 1), and re-fetching 200 full
  // rows every 2s just to read a counter would be wasteful — so this is its own
  // small, independently-cadenced query, the same shape ConnectPanel's
  // RateIndicator already uses for the same reason.
  const rateParams = { limit: 1 };
  const rate = useListTraffic(id, rateParams, {
    query: {
      queryKey: [...getListTrafficQueryKey(id, rateParams), generation],
      refetchInterval: RATE_INTERVAL_MS,
    },
  });

  const pollParams = { since: cursor ?? 0, limit: POLL_LIMIT };
  const poll = usePollTraffic(id, pollParams, {
    query: {
      queryKey: [...getPollTrafficQueryKey(id, pollParams), generation],
      // The poll is the fallback AND the bridge: it runs from the moment the
      // cursor is seeded until the stream's first frame, and again from any
      // stream error until a replacement's first frame (D19). Never while
      // live — one feed at a time, except for the one overlapping interval
      // the id-keyed merge absorbs.
      enabled: cursor !== null && !live,
      refetchInterval: POLL_INTERVAL_MS,
      // The default 5-minute cache would otherwise leave a stack of stale
      // `since`-keyed entries alive, each holding up to 200 full bodies —
      // `since` is part of the key, and the cursor advances every tick.
      gcTime: 0,
    },
  });

  // Seed the merged feed and the poll cursor from the initial tail. The list
  // is newest-first and carries no lastId (§3.5 fact 3), so the cursor is
  // max(rows[].id) computed here, not anything the server hands back.
  useEffect(() => {
    if (tail.data?.status !== 200) {
      return;
    }
    const data = tail.data.data;
    setRows((prev) => mergeRows(prev, data.rows));
    if (data.rows.length > 0) {
      const maxId = Math.max(...data.rows.map((r) => r.id));
      setCursor((prev) => (prev === null ? maxId : Math.max(prev, maxId)));
    } else {
      setCursor((prev) => (prev === null ? 0 : prev));
    }
    // oxlint-disable-next-line react/exhaustive-deps -- effect keys off tail.data identity, not the fresh closures above
  }, [tail.data]);

  // The poll answers oldest-first (§3.5 fact 3) — mergeRows doesn't care about
  // order, it just keys by id — and lastId is always the next cursor.
  useEffect(() => {
    if (poll.data?.status !== 200) {
      return;
    }
    const data = poll.data.data;
    if (data.rows.length > 0) {
      setRows((prev) => mergeRows(prev, data.rows));
    }
    setCursor((prev) => (prev === null ? data.lastId : Math.max(prev, data.lastId)));
    // oxlint-disable-next-line react/exhaustive-deps -- effect keys off poll.data identity, not the fresh closures above
  }, [poll.data]);

  // The stream (D19). Opened once the tail has seeded the cursor, and
  // reopened per generation (a clear). Everything the effect owns — the
  // EventSource, the retry timer, the probe's AbortController — is released
  // by its cleanup, so a screen unmounted mid-fallback leaves no timer that
  // would open a stream into nothing.
  const seeded = cursor !== null;
  useEffect(() => {
    if (!seeded) {
      return;
    }
    let source: EventSource | null = null;
    let retry: ReturnType<typeof setTimeout> | null = null;
    let probe: AbortController | null = null;
    let disposed = false;

    const open = (): void => {
      if (disposed) {
        // A retry timer that fired after cleanup would otherwise open a
        // stream into an unmounted screen.
        return;
      }
      source = openTrafficStream(id, cursorRef.current ?? 0);
      if (source === null) {
        // No EventSource in this browser: the poll is the feed, for good,
        // with nothing to say beside the badge.
        setTransport("fallback");
        setFallbackReason(null);
        return;
      }
      source.addEventListener("traffic", (event) => {
        if (disposed) {
          return;
        }
        const data = JSON.parse((event as MessageEvent<string>).data) as TrafficPollView;
        if (data.rows.length > 0) {
          setRows((prev) => mergeRows(prev, data.rows));
        }
        setCursor((prev) => (prev === null ? data.lastId : Math.max(prev, data.lastId)));
        setStreamDropped(data.dropped);
        setTransport("live");
        setFallbackReason(null);
      });
      source.onerror = () => {
        if (disposed) {
          return;
        }
        // Whatever the browser would do next on its own (its silent
        // reconnect) is not allowed to leave the badge saying "live" with
        // no poll running — so the stream is closed HERE and the retry is
        // ours, on our interval.
        source?.close();
        source = null;
        setTransport("fallback");
        probe?.abort();
        probe = new AbortController();
        void probeStreamRefusal(
          `/api/workspaces/${id}/traffic/stream?since=${cursorRef.current ?? 0}`,
          probe.signal,
        ).then((result) => {
          if (disposed) {
            return;
          }
          setFallbackReason(result?.message ?? null);
        });
        retry = setTimeout(open, streamRetryMs);
      };
    };
    open();

    return () => {
      disposed = true;
      source?.close();
      if (retry !== null) {
        clearTimeout(retry);
      }
      probe?.abort();
    };
  }, [id, seeded, generation, streamRetryMs]);

  const sortedRows = useMemo(() => Object.values(rows).sort((a, b) => b.id - a.id), [rows]);

  // dropped is on all three producers (§3.5 fact 1; the stream frame carries
  // the poll view, D6); take it from whichever answer is freshest — the
  // stream's own while live. It is the recorder's PROCESS-WIDE LIFETIME
  // counter, not per-workspace and not reset by clearing (§3.5 fact 2) —
  // worded as that fact, not as "this list is missing rows".
  const dropped =
    live && streamDropped !== null
      ? streamDropped
      : poll.data?.status === 200
        ? poll.data.data.dropped
        : rate.data?.status === 200
          ? rate.data.data.dropped
          : tail.data?.status === 200
            ? tail.data.data.dropped
            : 0;
  const rate1m = rate.data?.status === 200 ? rate.data.data.rate1m : null;

  const clearTraffic = useClearTraffic({
    mutation: {
      onSuccess: (res) => {
        if (res.status !== 200) {
          return;
        }
        // Stop anything already on the wire for the generation being retired
        // BEFORE that generation's local state disappears — the clear-vs-poll
        // race this whole design exists to close (§3.5).
        void queryClient.cancelQueries({ queryKey: getListTrafficQueryKey(id) });
        void queryClient.cancelQueries({ queryKey: getPollTrafficQueryKey(id) });
        void queryClient.invalidateQueries({ queryKey: getListTrafficQueryKey(id) });
        setRows({});
        setCursor(null);
        setExpanded(null);
        // A new generation closes the stream and reopens it from the new
        // tail's cursor (D20); the transport is unknown again until that
        // stream's first frame, so the poll bridges the gap.
        setTransport("connecting");
        setStreamDropped(null);
        setGeneration((g) => g + 1);
        notifications.show({
          color: "green",
          message: `Журнал очищен: удалено записей — ${res.data.deleted}`,
        });
      },
    },
  });

  function handleClear(): void {
    modals.openConfirmModal({
      title: "Очистить журнал трафика",
      children: (
        <Text size="sm">Удалить все записанные запросы этого воркспейса? Действие необратимо.</Text>
      ),
      labels: { confirm: "Очистить", cancel: "Отмена" },
      confirmProps: { color: "red", "data-testid": "traffic-clear-confirm" },
      onConfirm: () => clearTraffic.mutate({ id }),
    });
  }

  const toOverride = useTrafficToOverride({
    mutation: {
      onSuccess: (res, vars) => {
        if (res.status !== 200) {
          return;
        }
        setRowErrors((prev) => {
          const next = { ...prev };
          delete next[vars.tid];
          return next;
        });
        void queryClient.invalidateQueries({ queryKey: getGetWorkspaceQueryKey(id) });
        void queryClient.invalidateQueries({ queryKey: getListWorkspaceOperationsQueryKey(id) });
        void queryClient.invalidateQueries({
          queryKey: getGetOperationOverrideQueryKey(id, res.data.opKey),
        });
        // A21 (U4): the toast used to end here; the result lives on another
        // tab and the operator had to find it. navigate is the page's own
        // closure, so it works from inside the notification's portal.
        const opKey = res.data.opKey;
        notifications.show({
          color: "green",
          message: (
            <Group gap="xs" justify="space-between" wrap="nowrap">
              <span>
                Правка создана: {decodeURIComponent(opKey)}, статус {res.data.status}
              </span>
              <Button
                size="compact-xs"
                variant="light"
                data-testid="traffic-open-override"
                onClick={() =>
                  void navigate({
                    to: "/workspaces/$id/operations",
                    params: { id },
                    search: { opKey },
                  })
                }
              >
                Открыть
              </Button>
            </Group>
          ),
        });
      },
      onError: (err, vars) => {
        setRowErrors((prev) => ({ ...prev, [vars.tid]: describeApiFailure(err) }));
      },
    },
  });

  const toEndpoint = useTrafficToEndpoint({
    mutation: {
      onSuccess: (res, vars) => {
        if (res.status !== 201) {
          return;
        }
        setRowErrors((prev) => {
          const next = { ...prev };
          delete next[vars.tid];
          return next;
        });
        void queryClient.invalidateQueries({ queryKey: getGetWorkspaceQueryKey(id) });
        void queryClient.invalidateQueries({ queryKey: getListEndpointsQueryKey(id) });
        const endpointId = String(res.data.id);
        notifications.show({
          color: "green",
          message: (
            <Group gap="xs" justify="space-between" wrap="nowrap">
              <span>
                Создан endpoint: {res.data.method} {res.data.path}
              </span>
              <Button
                size="compact-xs"
                variant="light"
                data-testid="traffic-open-endpoint"
                onClick={() =>
                  void navigate({
                    to: "/workspaces/$id/endpoints",
                    params: { id },
                    search: { endpointId },
                  })
                }
              >
                Открыть
              </Button>
            </Group>
          ),
        });
      },
      onError: (err, vars) => {
        setRowErrors((prev) => ({ ...prev, [vars.tid]: describeApiFailure(err) }));
      },
    },
  });

  return (
    <Stack gap="md" data-testid="traffic-page">
      <Group justify="space-between" wrap="wrap">
        <Title order={2}>Трафик</Title>
        <Group gap="md">
          <Badge
            color={live ? "green" : "yellow"}
            variant="light"
            data-testid="traffic-transport"
            data-transport={transport}
          >
            {live ? "живой поток" : "опрос каждые 2 с"}
          </Badge>
          {!live && fallbackReason !== null ? (
            <Text size="xs" c="dimmed" data-testid="traffic-transport-reason">
              {fallbackReason}
            </Text>
          ) : null}
          <Text size="sm" c="dimmed" component="output" data-testid="traffic-rate">
            {rate1m === null ? "Считаем скорость…" : `${rate1m} запросов за последнюю минуту`}
          </Text>
          <Button
            variant="default"
            color="red"
            size="xs"
            onClick={handleClear}
            loading={clearTraffic.isPending}
            data-testid="traffic-clear"
          >
            Очистить журнал
          </Button>
        </Group>
      </Group>

      {dropped > 0 ? (
        <Alert color="yellow" data-testid="traffic-dropped-banner">
          Сервер отбрасывал записи трафика под нагрузкой — {dropped} за всё время работы процесса
          (счётчик общий для всех воркспейсов и не сбрасывается очисткой журнала).
        </Alert>
      ) : null}

      {sortedRows.length >= MAX_ROWS_KEPT ? (
        <Text size="xs" c="dimmed">
          Показаны только последние {MAX_ROWS_KEPT} записей.
        </Text>
      ) : null}

      {tail.isPending ? (
        <Group gap="xs">
          <Loader size="sm" />
          <Text size="sm" component="output">
            Загрузка…
          </Text>
        </Group>
      ) : tail.isError ? (
        <Stack gap="sm">
          <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
            {describeApiFailure(tail.error)}
          </Alert>
          <Button
            variant="default"
            w="fit-content"
            onClick={() => void tail.refetch()}
            data-testid="traffic-retry"
          >
            Повторить
          </Button>
        </Stack>
      ) : tail.data.status !== 200 ? (
        <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
          {describeApiFailure(null)}
        </Alert>
      ) : sortedRows.length === 0 ? (
        <Text data-testid="traffic-empty">Пока ничего не записано</Text>
      ) : (
        <ScrollArea>
          <Table striped highlightOnHover data-testid="traffic-table">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>Время</Table.Th>
                <Table.Th>Метод</Table.Th>
                <Table.Th>Путь</Table.Th>
                <Table.Th>Статус</Table.Th>
                <Table.Th>Длительность</Table.Th>
                <Table.Th>Совпадение</Table.Th>
                <Table.Th>Источник</Table.Th>
                <Table.Th>Действия</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {sortedRows.map((row) => {
                const overrideReason = overrideDisabledReason(row);
                const endpointReason = endpointDisabledReason(row);
                const droppedNote = droppedNoteCount(row.notes);
                const isExpanded = expanded === row.id;
                return (
                  <TrafficRowGroup
                    key={row.id}
                    workspaceId={id}
                    row={row}
                    isExpanded={isExpanded}
                    onToggle={() => setExpanded((prev) => (prev === row.id ? null : row.id))}
                    overrideReason={overrideReason}
                    endpointReason={endpointReason}
                    droppedNote={droppedNote}
                    error={rowErrors[row.id]}
                    onCreateOverride={() => toOverride.mutate({ id, tid: row.id })}
                    onCreateEndpoint={() => toEndpoint.mutate({ id, tid: row.id })}
                    overridePending={toOverride.isPending && toOverride.variables?.tid === row.id}
                    endpointPending={toEndpoint.isPending && toEndpoint.variables?.tid === row.id}
                  />
                );
              })}
            </Table.Tbody>
          </Table>
        </ScrollArea>
      )}
    </Stack>
  );
}

function TrafficRowGroup({
  workspaceId,
  row,
  isExpanded,
  onToggle,
  overrideReason,
  endpointReason,
  droppedNote,
  error,
  onCreateOverride,
  onCreateEndpoint,
  overridePending,
  endpointPending,
}: {
  workspaceId: number;
  row: TrafficRow;
  isExpanded: boolean;
  onToggle: () => void;
  overrideReason: string | null;
  endpointReason: string | null;
  droppedNote: number | null;
  error: string | undefined;
  onCreateOverride: () => void;
  onCreateEndpoint: () => void;
  overridePending: boolean;
  endpointPending: boolean;
}) {
  return (
    <>
      <Table.Tr
        data-testid="traffic-row"
        data-row-id={row.id}
        onClick={onToggle}
        style={{ cursor: "pointer" }}
      >
        <Table.Td>{formatTime(row.ts)}</Table.Td>
        <Table.Td>{row.method}</Table.Td>
        <Table.Td>
          <Code>{row.path}</Code>
        </Table.Td>
        <Table.Td>{row.status}</Table.Td>
        <Table.Td>{row.durationMs} мс</Table.Td>
        <Table.Td onClick={(e) => e.stopPropagation()}>
          <MatchCell workspaceId={workspaceId} row={row} />
        </Table.Td>
        <Table.Td>{formatSource(row)}</Table.Td>
        <Table.Td onClick={(e) => e.stopPropagation()}>
          <Group gap="xs" wrap="nowrap">
            <Button
              size="xs"
              variant="default"
              disabled={overrideReason !== null}
              title={overrideReason ?? undefined}
              loading={overridePending}
              onClick={onCreateOverride}
              data-testid="traffic-to-override"
            >
              Создать правку из этого ответа
            </Button>
            <Button
              size="xs"
              variant="default"
              disabled={endpointReason !== null}
              title={endpointReason ?? undefined}
              loading={endpointPending}
              onClick={onCreateEndpoint}
              data-testid="traffic-to-endpoint"
            >
              Создать endpoint из этого запроса
            </Button>
          </Group>
        </Table.Td>
      </Table.Tr>
      {overrideReason !== null ||
      endpointReason !== null ||
      droppedNote !== null ||
      error !== undefined ? (
        <Table.Tr>
          <Table.Td colSpan={8} style={{ paddingTop: 0 }}>
            <Group gap="xs" wrap="wrap">
              {overrideReason !== null ? (
                <Badge color="gray" variant="light" data-testid="traffic-override-reason">
                  Правка недоступна: {overrideReason}
                </Badge>
              ) : null}
              {endpointReason !== null ? (
                <Badge color="gray" variant="light" data-testid="traffic-endpoint-reason">
                  Endpoint недоступен: {endpointReason}
                </Badge>
              ) : null}
              {droppedNote !== null ? (
                <Badge color="yellow" variant="light">
                  Отброшено при записи этой строки: {droppedNote}
                </Badge>
              ) : null}
            </Group>
            {error !== undefined ? (
              <Alert color="red" role="alert" mt="xs">
                {error}
              </Alert>
            ) : null}
          </Table.Td>
        </Table.Tr>
      ) : null}
      {isExpanded ? (
        <Table.Tr data-testid="traffic-row-details">
          <Table.Td colSpan={8}>
            <Stack gap="xs">
              <Text size="sm" fw={500}>
                Заголовки запроса
              </Text>
              <Code block>{JSON.stringify(row.reqHeaders ?? {}, null, 2)}</Code>
              <Text size="sm" fw={500}>
                Тело запроса
              </Text>
              <Code block>
                {row.reqBody === undefined || row.reqBody === "" ? "—" : row.reqBody}
              </Code>
              <Text size="sm" fw={500}>
                Тело ответа
              </Text>
              <Code block>
                {row.respBody === undefined || row.respBody === "" ? "—" : row.respBody}
              </Code>
              <Text size="sm" fw={500}>
                Заметки
              </Text>
              <Text size="sm">{row.notes === undefined || row.notes === "" ? "—" : row.notes}</Text>
            </Stack>
          </Table.Td>
        </Table.Tr>
      ) : null}
    </>
  );
}

// MatchCell is the «Совпадение» column (A21, U4): what answered the request,
// as a LINK into its editor when there is one — the operations tab by the
// spec operation's id (`?opId=`, resolved there), the custom-endpoints tab
// by the row id (`?endpointId=`, the P7b deep link). Before this the cell
// printed `operation #12` as text and the most natural move on the screen —
// "this answer is wrong, open it" — was four clicks and a search.
function MatchCell({ workspaceId, row }: { workspaceId: number; row: TrafficRow }) {
  const navigate = useNavigate();
  if (row.matchedKind === "operation" && row.matchedId != null) {
    const opId = String(row.matchedId);
    return (
      <Anchor
        size="sm"
        href={`/workspaces/${workspaceId}/operations?opId=${opId}`}
        data-testid="traffic-match-link"
        onClick={(e) => {
          e.preventDefault();
          void navigate({
            to: "/workspaces/$id/operations",
            params: { id: workspaceId },
            search: { opId },
          });
        }}
      >
        операция #{row.matchedId}
      </Anchor>
    );
  }
  if (row.matchedKind === "endpoint" && row.matchedId != null) {
    const endpointId = String(row.matchedId);
    return (
      <Anchor
        size="sm"
        href={`/workspaces/${workspaceId}/endpoints?endpointId=${endpointId}`}
        data-testid="traffic-match-link"
        onClick={(e) => {
          e.preventDefault();
          void navigate({
            to: "/workspaces/$id/endpoints",
            params: { id: workspaceId },
            search: { endpointId },
          });
        }}
      >
        endpoint #{row.matchedId}
      </Anchor>
    );
  }
  return <>{row.matchedKind === "none" ? "—" : row.matchedKind}</>;
}
