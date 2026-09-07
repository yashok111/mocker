import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  getListTrafficQueryKey,
  getPollTrafficQueryKey,
  useListTraffic,
  usePollTraffic,
} from "@/api/generated/traffic/traffic.ts";
import type { TrafficPollView, TrafficRow } from "@/api/generated/schemas";
import { openTrafficStream, probeStreamRefusal } from "@/api/stream";
import type { QueryStateQuery } from "../QueryState";
import {
  POLL_INTERVAL_MS,
  POLL_LIMIT,
  RATE_INTERVAL_MS,
  TAIL_LIMIT,
  mergeRows,
  type Transport,
} from "./model";

// useTrafficFeed owns the three-producer feed and nothing else: the tail, the
// poll and the stream, the generation fence that a clear bumps, and the
// cursor they share. It was inlined in TrafficPage.tsx, where the transport
// state machine, the table and the two "make something from this row"
// mutations sat in one 923-line component and the comments below — every one
// of them a bug someone paid for — were unreachable to a reader who came for
// the markup.
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

export type TrafficFeed = {
  /** Newest first. Already trimmed to MAX_ROWS_KEPT by the merge. */
  sortedRows: TrafficRow[];
  transport: Transport;
  live: boolean;
  fallbackReason: string | null;
  /** The recorder's PROCESS-WIDE LIFETIME counter, not per-workspace. */
  dropped: number;
  rate1m: number | null;
  /** The tail query, for the screen's own four-state ladder. Narrowed to
   * what QueryState reads: orval types the hook per response union, and
   * threading that union out of here would make the hook's own signature a
   * restatement of the traffic contract. */
  tail: QueryStateQuery;
  /** True when the tail answered with a status the screen does not render —
   * only meaningful once the tail has answered at all. */
  tailUnexpectedStatus: boolean;
  /** Retires the current generation: cancels what is on the wire for it,
   * drops the merged rows and reopens the stream from a fresh tail. */
  startNewGeneration: () => void;
};

export function useTrafficFeed(id: number, streamRetryMs: number): TrafficFeed {
  const queryClient = useQueryClient();

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
  const tailUnexpectedStatus = tail.data !== undefined && tail.data.status !== 200;

  // Stop anything already on the wire for the generation being retired
  // BEFORE that generation's local state disappears — the clear-vs-poll
  // race this whole design exists to close (§3.5).
  const startNewGeneration = useCallback((): void => {
    void queryClient.cancelQueries({ queryKey: getListTrafficQueryKey(id) });
    void queryClient.cancelQueries({ queryKey: getPollTrafficQueryKey(id) });
    void queryClient.invalidateQueries({ queryKey: getListTrafficQueryKey(id) });
    setRows({});
    setCursor(null);
    // A new generation closes the stream and reopens it from the new
    // tail's cursor (D20); the transport is unknown again until that
    // stream's first frame, so the poll bridges the gap.
    setTransport("connecting");
    setStreamDropped(null);
    setGeneration((g) => g + 1);
  }, [id, queryClient]);

  return {
    sortedRows,
    transport,
    live,
    fallbackReason,
    dropped,
    rate1m,
    tail,
    tailUnexpectedStatus,
    startNewGeneration,
  };
}
