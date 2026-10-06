// Developer harness, deliberately not run on normal navigation. Invoke on an
// isolated development server with a real UI action and declared fixture profile.
export async function benchmarkWorkbenchUI(
  name: string,
  action: () => Promise<void>,
  metadata: {
    nodes: number;
    edges: number;
    buildHash: string;
    cache: "cold" | "warm";
    browser: string;
  },
  repetitions = 30,
) {
  if (repetitions < 30 || !metadata.buildHash)
    throw new Error("Need ≥30 repetitions and exact build identity");
  const supported =
    typeof PerformanceObserver !== "undefined" &&
    PerformanceObserver.supportedEntryTypes.includes("longtask");
  const longTasks: number[] = [];
  const observer = supported
    ? new PerformanceObserver((list) => {
        for (const entry of list.getEntries()) longTasks.push(entry.duration);
      })
    : undefined;
  observer?.observe({ type: "longtask", buffered: false });
  const samples: { durationMs: number; longTasksMs: number[] }[] = [];
  try {
    for (let i = 0; i < repetitions + 3; i++) {
      longTasks.length = 0;
      const start = performance.now();
      await action();
      await new Promise<void>((resolve) =>
        requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
      );
      samples.push({ durationMs: performance.now() - start, longTasksMs: [...longTasks] });
    }
  } finally {
    observer?.disconnect();
  }
  const measured = samples.slice(3),
    sorted = measured.map((s) => s.durationMs).sort((a, b) => a - b);
  return {
    name,
    metadata,
    warmups: samples.slice(0, 3),
    samples: measured,
    p95Ms: sorted[Math.ceil(sorted.length * 0.95) - 1],
    longTaskGate: supported
      ? measured.some((s) => s.longTasksMs.some((v) => v > 100))
        ? "FAIL"
        : "NO >100ms LONG TASK OBSERVED"
      : "NOT RUN: longtask API unsupported",
    acceptance: "LOCAL ONLY: input latency and target hardware acceptance remain separate",
  };
}
