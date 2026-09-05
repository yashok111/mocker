import "@testing-library/jest-dom/vitest";
import { configure } from "@testing-library/react";
import { vi } from "vitest";

// Testing Library's default asyncUtilTimeout is 1000 ms of WALL CLOCK, which
// is not an assertion about anything — it just says "the machine had a second
// to spare". On a loaded box (a full-repo agent run, a parallel CI job) the
// suite would go red while the code under test was perfectly correct. That is
// a false negative, and the only thing it teaches is to re-run until green.
//
// 5 s is still far below anything a human waits for, so a genuinely stuck
// findBy/waitFor stays a fast failure. Keep this BELOW vite.config.ts's
// testTimeout, otherwise the test times out first and you lose Testing
// Library's much better error message.
configure({ asyncUtilTimeout: 5000 });

// The DOM is happy-dom (vitest 5, 2026-09-05; jsdom before). Mantine uses
// both of these — matchMedia for its responsive props and colour scheme,
// ResizeObserver inside ScrollArea and anything that measures itself — and
// the guards below install a stub only where the environment has none, so
// the file is honest under either implementation. Without the stubs every
// Mantine render would throw before a single assertion runs.
if (typeof window !== "undefined" && !window.matchMedia) {
  Object.defineProperty(window, "matchMedia", {
    writable: true,
    value: vi.fn().mockImplementation((query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })),
  });
}

if (typeof window !== "undefined" && !("ResizeObserver" in window)) {
  // @ts-expect-error – a DOM-environment polyfill
  window.ResizeObserver = class {
    observe(): void {}
    unobserve(): void {}
    disconnect(): void {}
  };
}

// Mantine's Transition components schedule through rAF; scrollIntoView (used
// when a Select opens) is stubbed where the environment lacks it.
if (typeof Element !== "undefined" && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = vi.fn();
}

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
