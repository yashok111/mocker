import type { TrafficRow } from "@/api/generated/schemas";

// traffic/model.ts is the traffic screen's pure half: the constants the feed
// is bounded by, the id-keyed merge, the two "why is this button refused"
// predicates and the two formatters. No React, no query client, no wire —
// which is exactly why it is worth having its own file: every one of these
// answers a question about DATA, and TrafficPage.tsx (923 lines) buried them
// above a component that reads as a transport state machine.

export const TAIL_LIMIT = 50;
export const POLL_LIMIT = 200;
export const POLL_INTERVAL_MS = 2000;
export const RATE_INTERVAL_MS = 10000;
export const MAX_ROWS_KEPT = 500;
/** How often, in fallback, a replacement stream is tried (D19's own 60 s —
 * a client constant, not configuration; see CARVE-OUTS.md). */
export const STREAM_RETRY_MS = 60000;

export type Transport = "connecting" | "live" | "fallback";

/** notes is a comma-separated token list; a token must match a WHOLE entry,
 * never a substring — free text in another token must not be able to forge
 * "redacted" by containing it (internal/traffic/traffic.go:158-165). */
export function hasNoteToken(notes: string | undefined, token: string): boolean {
  return (notes ?? "")
    .split(",")
    .map((entry) => entry.trim())
    .includes(token);
}

/** The per-row `dropped:<n>` note IS local to this row, unlike the process-wide
 * counter the wire envelope carries (§3.5 fact 2) — worth surfacing per row. */
export function droppedNoteCount(notes: string | undefined): number | null {
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
export function overrideDisabledReason(row: TrafficRow): string | null {
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
export function endpointDisabledReason(row: TrafficRow): string | null {
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
export function mergeRows(
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

export function formatTime(ts: string): string {
  const d = new Date(ts);
  return Number.isNaN(d.getTime()) ? ts : d.toLocaleString();
}

/** DESIGN's reason for a source column: a colleague's e2e run hitting this
 * workspace must not read as a ghost row with no origin. */
export function formatSource(row: TrafficRow): string {
  if (row.fwdIp !== undefined && row.fwdIp !== "") {
    return row.peerIp !== undefined && row.peerIp !== ""
      ? `${row.fwdIp} (via ${row.peerIp})`
      : row.fwdIp;
  }
  return row.peerIp !== undefined && row.peerIp !== "" ? row.peerIp : "—";
}
