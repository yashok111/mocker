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

// jsdom implements neither of these, and Mantine uses both: matchMedia for
// its responsive props and colour scheme, ResizeObserver inside ScrollArea and
// anything that measures itself. Without the stubs every Mantine render throws
// before a single assertion runs.
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
  // @ts-expect-error – jsdom polyfill
  window.ResizeObserver = class {
    observe(): void {}
    unobserve(): void {}
    disconnect(): void {}
  };
}

// Mantine's Transition components schedule through rAF; jsdom has it, but
// scrollIntoView (used when a Select opens) is missing and throws.
if (typeof Element !== "undefined" && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = vi.fn();
}
