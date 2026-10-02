import "../validation/configure";

// No network from a test, ever. happy-dom ships a REAL fetch that resolves a
// relative URL against its origin (http://localhost:3000) and connects —
// jsdom had no fetch of its own, so a stray call failed on the URL and
// nobody noticed. The stray call exists: React Query's refetchInterval can
// fire once more after a test's afterEach has run vi.unstubAllGlobals(),
// and that poll then reached for 127.0.0.1:3000 (ECONNREFUSED as an
// unhandled error, found on the move to happy-dom, 2026-09-05). This
// baseline is what unstubAllGlobals restores to: a rejection with a
// sentence, never a socket. A test that wants a fetch installs one with
// route() from src/test/http.ts.
const noNetwork = (): Promise<Response> =>
  Promise.reject(new Error("no network in tests: stub fetch with route() from src/test/http.ts"));
globalThis.fetch = noNetwork;
if (typeof window !== "undefined") {
  window.fetch = noNetwork;
}

if (typeof window !== "undefined") {
  await import("./setupDom");
}
