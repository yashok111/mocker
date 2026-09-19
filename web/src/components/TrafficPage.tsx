import { useMemo, useState } from "react";
import type { ReactElement } from "react";
import {
  Alert,
  Box,
  Badge,
  Button,
  Code,
  Group,
  NativeSelect,
  Stack,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { modals } from "@mantine/modals";
import { IconActivity, IconAlertTriangle, IconSearch } from "@tabler/icons-react";
import { notifications } from "@mantine/notifications";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import {
  useClearTraffic,
  useTrafficToEndpoint,
  useTrafficToOverride,
} from "@/api/generated/traffic/traffic.ts";
import { useGetWorkspace } from "@/api/generated/workspaces/workspaces.ts";
import { invalidateEndpointChange, invalidateOperationChange } from "@/api/cachePolicy";
import { describeApiFailure } from "@/api/errors";
import { QueryState } from "./QueryState";
import { TabLink } from "./TabLink";
import { MAX_ROWS_KEPT, STREAM_RETRY_MS } from "./traffic/model";
import { TrafficTable } from "./traffic/TrafficTable";
import { useTrafficFeed } from "./traffic/useTrafficFeed";

// TrafficPage is DESIGN §14 screen 8, and since the A21 structural pass it
// is the orchestration boundary and nothing else: the feed's three producers
// and the generation fence are ./traffic/useTrafficFeed.ts's, the pure
// merge/format/refusal answers are ./traffic/model.ts's, and the rows and
// their two actions are ./traffic/TrafficTable.tsx's. What is left here is
// what the screen DECIDES — the clear, the two "make something from this
// row" mutations with their toasts, the client-side filters and which of the
// four states is on screen.

export function TrafficPage({
  id,
  streamRetryMs = STREAM_RETRY_MS,
}: {
  id: number;
  /** Test seam only: the fallback's retry interval. */
  streamRetryMs?: number;
}): ReactElement {
  const queryClient = useQueryClient();
  const navigate = useNavigate();

  const {
    sortedRows,
    transport,
    live,
    fallbackReason,
    dropped,
    rate1m,
    tail,
    tailUnexpectedStatus,
    startNewGeneration,
  } = useTrafficFeed(id, streamRetryMs);

  const [expanded, setExpanded] = useState<number | null>(null);
  const [rowErrors, setRowErrors] = useState<Record<number, string>>({});

  // A21 (U5): the screen the guide sends people to first when the mock
  // answers wrongly had no filter at all. Client-side over the rows already
  // held (at most MAX_ROWS_KEPT), never a request.
  const [filterPath, setFilterPath] = useState("");
  const [filterMethod, setFilterMethod] = useState("");
  const [filterStatus, setFilterStatus] = useState("");
  const visibleRows = useMemo(
    () =>
      sortedRows.filter(
        (row) =>
          (filterPath === "" || row.path.includes(filterPath)) &&
          (filterMethod === "" || row.method === filterMethod) &&
          (filterStatus === "" || String(row.status).startsWith(filterStatus)),
      ),
    [sortedRows, filterPath, filterMethod, filterStatus],
  );
  const methodsSeen = useMemo(
    () => Array.from(new Set(sortedRows.map((r) => r.method))).sort(),
    [sortedRows],
  );
  // The mock's address for the empty state — the moment the person asks
  // «дошёл ли запрос?» is the moment the screen used to say one bare sentence.
  const workspace = useGetWorkspace(id);
  const workspaceUrl = workspace.data?.status === 200 ? workspace.data.data.url : null;
  const clearTraffic = useClearTraffic({
    mutation: {
      onSuccess: (res) => {
        if (res.status !== 200) {
          return;
        }
        startNewGeneration();
        setExpanded(null);
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
      cancelProps: { "data-testid": "dialog-cancel" },
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
        invalidateOperationChange(queryClient, id, res.data.opKey);
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
        invalidateEndpointChange(queryClient, id);
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
        <div>
          <Title order={2}>Трафик</Title>
          <Text size="sm" c="dimmed" mt={6}>
            Запросы к моку, ответы и время выполнения.
          </Text>
        </div>
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

      <QueryState queries={[tail]} testIdPrefix="traffic">
        {tailUnexpectedStatus ? (
          // An unexpected status is not the query failing — a second try
          // answers the same way, so this branch offers no retry.
          <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
            {describeApiFailure(null)}
          </Alert>
        ) : sortedRows.length === 0 ? (
          <Stack gap="sm" className="mocker-empty-state" data-testid="traffic-empty">
            <IconActivity size={32} color="var(--mocker-accent)" stroke={1.5} />
            <Text>Пока ничего не записано.</Text>
            <Text size="sm" c="dimmed">
              Запросы появятся здесь, как только фронтенд сходит на мок
              {workspaceUrl !== null ? (
                <>
                  {" "}
                  по адресу <Code>{workspaceUrl}</Code>
                </>
              ) : null}
              . «Как подключить фронтенд» и кнопка «Проверить» — на вкладке{" "}
              <TabLink id={id} tab="overview" testId="traffic-empty-overview-link">
                «Обзор»
              </TabLink>
              .
            </Text>
          </Stack>
        ) : (
          <Stack gap="md">
            <Group gap="sm" align="flex-end" data-testid="traffic-filters">
              <TextInput
                size="sm"
                label="Путь содержит"
                placeholder="Найти запрос по пути…"
                leftSection={<IconSearch size={16} />}
                style={{ flex: "1 1 220px", maxWidth: 420 }}
                value={filterPath}
                onChange={(e) => setFilterPath(e.currentTarget.value)}
                data-testid="traffic-filter-path"
              />
              <NativeSelect
                size="sm"
                label="Метод"
                value={filterMethod}
                onChange={(e) => setFilterMethod(e.currentTarget.value)}
                data-testid="traffic-filter-method"
              >
                <option value="">все</option>
                {methodsSeen.map((m) => (
                  <option key={m} value={m}>
                    {m}
                  </option>
                ))}
              </NativeSelect>
              <NativeSelect
                size="sm"
                label="Статус"
                value={filterStatus}
                onChange={(e) => setFilterStatus(e.currentTarget.value)}
                data-testid="traffic-filter-status"
              >
                <option value="">все</option>
                <option value="2">2xx</option>
                <option value="3">3xx</option>
                <option value="4">4xx</option>
                <option value="5">5xx</option>
              </NativeSelect>
              {visibleRows.length !== sortedRows.length ? (
                <Text size="xs" c="dimmed" data-testid="traffic-filter-count">
                  показано {visibleRows.length} из {sortedRows.length}
                </Text>
              ) : null}
            </Group>
            <Box
              component="section"
              className="mocker-table-surface"
              style={{ maxHeight: "calc(100dvh - 330px)", minHeight: 160 }}
              tabIndex={0}
              aria-label="Журнал запросов"
            >
              <TrafficTable
                workspaceId={id}
                rows={visibleRows}
                expanded={expanded}
                onToggle={(rowId) => setExpanded((prev) => (prev === rowId ? null : rowId))}
                rowErrors={rowErrors}
                onCreateOverride={(tid) => toOverride.mutate({ id, tid })}
                onCreateEndpoint={(tid) => toEndpoint.mutate({ id, tid })}
                overridePendingFor={toOverride.isPending ? toOverride.variables?.tid : undefined}
                endpointPendingFor={toEndpoint.isPending ? toEndpoint.variables?.tid : undefined}
              />
              {visibleRows.length === 0 ? (
                <Text p="xl" ta="center" c="dimmed">
                  Нет запросов с такими фильтрами.
                </Text>
              ) : null}
            </Box>
          </Stack>
        )}
      </QueryState>
    </Stack>
  );
}
