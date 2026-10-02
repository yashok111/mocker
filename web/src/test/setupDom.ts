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

// Mantine 9 runs its transition hook even with env="test". Reduced motion
// prevents loading-button animation timers from outliving the test DOM.
if (typeof window !== "undefined") {
  const matchMedia = window.matchMedia.bind(window);
  window.matchMedia = (query) => {
    const result = matchMedia(query);
    if (query === "(prefers-reduced-motion: reduce)") {
      Object.defineProperty(result, "matches", { configurable: true, value: true });
    }
    return result;
  };
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

// Mantine autosizing textareas listen for font loading; happy-dom has no FontFaceSet.
if (typeof document !== "undefined" && !document.fonts) {
  Object.defineProperty(document, "fonts", { configurable: true, value: new EventTarget() });
}
