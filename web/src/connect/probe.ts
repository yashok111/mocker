// probe.ts is the browser half of DESIGN §14 screen 4's two-sided
// «Проверить»: mocker dialling the workspace itself is a later slice (this
// tree has no outbound HTTP client at all yet), so today this is the whole
// check. interpretProbe is a pure function over what the one fetch to
// {ws.url}{reservedPrefix}/health produced — kept separate from runProbe so
// every branch (including the network-error one a real fetch is awkward to
// force) is a plain function call in probe.test.ts, no DOM or timers
// involved.
const DEFAULT_TIMEOUT_MS = 5000;

// ProbeOutcome is deliberately not "a Response": handleResponse-style
// parsing already happened by the time interpretProbe runs, and a rejected
// or aborted fetch never produces a Response at all — both have to be
// representable here as plainly as the 200/4xx case.
export type ProbeOutcome =
  | { kind: "response"; status: number; ok: boolean; body: unknown }
  | { kind: "network-error" }
  | { kind: "timeout" };

export type ProbeResult =
  | { kind: "ok"; workspace: string; revision: number }
  | { kind: "wrong-workspace"; workspace: string }
  | { kind: "http-error"; status: number; message: string }
  | { kind: "network-error" }
  | { kind: "timeout" };

type HealthBody = { ok: boolean; workspace: string; revision: number };

// isHealthBody narrows the parsed JSON at the one boundary that needs it:
// mirrors internal/mockplane/plane.go's `health` struct field for field
// (ok, workspace, revision — spec is never read here).
function isHealthBody(value: unknown): value is HealthBody {
  if (typeof value !== "object" || value === null) {
    return false;
  }
  const b = value as Record<string, unknown>;
  return (
    typeof b.ok === "boolean" && typeof b.workspace === "string" && typeof b.revision === "number"
  );
}

type ErrorEnvelope = { error: { message: string } };

// isErrorEnvelope mirrors client.ts's own guard: the reserved-prefix routes
// answer through the same httpx.Err as the admin API, so an error body here
// can carry the identical { error: { code, message } } shape — but this is
// a different plane (cross-origin, no admin auth), so it gets its own copy
// rather than importing api/client.ts's private one.
function isErrorEnvelope(value: unknown): value is ErrorEnvelope {
  if (typeof value !== "object" || value === null || !("error" in value)) {
    return false;
  }
  const err = (value as { error: unknown }).error;
  if (typeof err !== "object" || err === null) {
    return false;
  }
  return typeof (err as { message?: unknown }).message === "string";
}

// interpretProbe is the whole point of this screen (see the phase brief):
// a 200 with the wrong slug is a green tick that would be a lie (wildcard
// DNS or a misrouted proxy), so it gets its own branch rather than folding
// into "ok".
export function interpretProbe(outcome: ProbeOutcome, expectedSlug: string): ProbeResult {
  if (outcome.kind === "timeout" || outcome.kind === "network-error") {
    return { kind: outcome.kind };
  }
  const { status, ok, body } = outcome;
  if (!ok) {
    const message = isErrorEnvelope(body) ? body.error.message : `сервер ответил ${status}`;
    return { kind: "http-error", status, message };
  }
  if (!isHealthBody(body)) {
    // A 2xx that isn't the health shape at all — not a case the server
    // produces today, but a client that assumed it always would is exactly
    // the kind of guess this file's neighbours (client.ts, types.ts) avoid.
    return { kind: "http-error", status, message: "сервер ответил, но не тем, что ожидалось" };
  }
  if (body.workspace !== expectedSlug) {
    return { kind: "wrong-workspace", workspace: body.workspace };
  }
  return { kind: "ok", workspace: body.workspace, revision: body.revision };
}

function parseJSON(text: string): unknown {
  if (text === "") {
    return undefined;
  }
  try {
    return JSON.parse(text);
  } catch {
    return undefined;
  }
}

// runProbe is the one impure half: the actual cross-origin fetch, wrapped
// in a race against timeoutMs so a hung connection (a corporate proxy that
// swallows the request rather than answering it) never leaves the button
// spinning forever. AbortController's own reason distinguishes "we gave up"
// from "the browser refused the request" (network/CORS) — both look like a
// rejected promise, but only one of them is a timeout.
export async function runProbe(
  url: string,
  expectedSlug: string,
  timeoutMs = DEFAULT_TIMEOUT_MS,
): Promise<ProbeResult> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    const res = await fetch(url, { signal: controller.signal, cache: "no-store" });
    const text = await res.text();
    const body = parseJSON(text);
    return interpretProbe({ kind: "response", status: res.status, ok: res.ok, body }, expectedSlug);
  } catch (err) {
    if (controller.signal.aborted) {
      return interpretProbe({ kind: "timeout" }, expectedSlug);
    }
    // Anything else fetch throws for — DNS failure, TLS trust failure,
    // CORS refusal, an actual network drop — is indistinguishable from
    // here, on purpose: it is exactly the ambiguity the caller has to
    // explain with three named causes rather than one wrong guess. err is
    // deliberately unused: this file never inspects it, only the fact that
    // it happened.
    void err;
    return interpretProbe({ kind: "network-error" }, expectedSlug);
  } finally {
    clearTimeout(timer);
  }
}
