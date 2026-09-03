// stream.ts is the browser side of P6a (decisions.md mocker-p6a-sse D17,
// D19): the admin traffic feed over Server-Sent Events, consumed through the
// browser's own EventSource and NOT through the orval-generated client. A
// generated hook for GET .../traffic/stream would run through customFetch,
// which awaits res.text() on every non-204 response — on a stream that
// never ends it would never resolve, holding one of the server's 64 slots
// until the connection's 900-second lifetime expired. So this file is the
// one place under web/src that calls EventSource and the one place that
// calls fetch outside api/client.ts, and both are decided rather than
// forgotten (client.ts's own rule against a screen calling fetch stands;
// this is the exception it names).
//
// web/src/api/coverage.test.ts recognises the `new EventSource(\`...\`)`
// call below as the route's caller: the scanner turns the contract's
// `{id}` template segment into a one-segment wildcard, so the natural
// template literal here matches without being written around the guard.

import { notifyUnauthorized } from "./client";

/** openTrafficStream opens the feed for one workspace from a cursor, or
 * returns null where the browser has no EventSource at all (then the screen
 * stays on the poll for good — the fallback D19 already requires, with no
 * reason to show beside it). */
export function openTrafficStream(id: number, since: number): EventSource | null {
  if (typeof EventSource === "undefined") {
    return null;
  }
  return new EventSource(`/api/workspaces/${id}/traffic/stream?since=${since}`);
}

export interface StreamProbe {
  status: number;
  /** The server's own envelope message on a refusal (501/503); null on a
   * success or when the body carried none. */
  message: string | null;
}

/** probeStreamRefusal is D19's "where the reason comes from": a native
 * EventSource `error` event carries neither the status nor the body, so a
 * 501, a 503 and a dropped connection are one event. ONE raw fetch of the
 * same URL reads the status and, only on a refusal, the JSON envelope's
 * message. On a success the response is a live stream and is CANCELLED at
 * once — the probe proves a handshake would be accepted, never that the
 * browser reconnected, so nothing here ever restores the live badge. A
 * fetch that itself fails answers null: the badge then shows no reason
 * rather than an invented one. A 401 is not a transport failure at all —
 * the session is gone — and goes where every other 401 goes.
 *
 * Deliberately NOT the orval mutator, and deliberately with credentials
 * (the session cookie is what the handshake is authorised by). */
export async function probeStreamRefusal(
  url: string,
  signal: AbortSignal,
): Promise<StreamProbe | null> {
  let res: Response;
  try {
    res = await fetch(url, {
      signal,
      credentials: "same-origin",
      headers: { Accept: "text/event-stream" },
    });
  } catch {
    return null;
  }
  if (res.ok) {
    try {
      await res.body?.cancel();
    } catch {
      // Already gone; nothing to release.
    }
    return { status: res.status, message: null };
  }
  if (res.status === 401) {
    notifyUnauthorized();
    return { status: 401, message: null };
  }
  let message: string | null = null;
  try {
    const envelope = (await res.json()) as { error?: { message?: string } };
    message = envelope.error?.message ?? null;
  } catch {
    message = null;
  }
  return { status: res.status, message };
}
