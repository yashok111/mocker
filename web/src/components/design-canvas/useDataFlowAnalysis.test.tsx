import { act, renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { emptyCanvas } from "./canvasModel";
import { useDataFlowAnalysis, type AnalyzeDataFlow } from "./useDataFlowAnalysis";
import type { DataFlowAnalysis } from "./types";

const valid: DataFlowAnalysis = { messages: [], bindings: [], diagnostics: [] };

describe("useDataFlowAnalysis", () => {
  it("aborts superseded requests and ignores a late response that ignores cancellation", async () => {
    const pending: { resolve: (value: DataFlowAnalysis) => void; signal: AbortSignal }[] = [];
    const analyze = vi
      .fn<AnalyzeDataFlow>()
      .mockImplementation(
        (_document, signal) => new Promise((resolve) => pending.push({ resolve, signal })),
      );
    const initial = emptyCanvas();
    const { result, rerender, unmount } = renderHook(
      ({ document }) => useDataFlowAnalysis(document, true, analyze),
      { initialProps: { document: initial } },
    );
    await waitFor(() => expect(pending).toHaveLength(1));
    rerender({ document: { ...initial, title: "New draft" } });
    expect(pending[0]!.signal.aborted).toBe(true);
    expect(result.current.pending).toBe(true);
    await waitFor(() => expect(pending).toHaveLength(2));
    const invalid = {
      ...valid,
      diagnostics: [{ pointer: "/messages/0", message: "Источник удалён", severity: "error" }],
    };
    await act(async () => pending[1]!.resolve(invalid));
    await act(async () => pending[0]!.resolve(valid));
    expect(result.current.current).toEqual(invalid);
    unmount();
    expect(pending[1]!.signal.aborted).toBe(true);
  });

  it("reports failure and retries instead of reusing a successful analysis", async () => {
    const analyze = vi
      .fn<AnalyzeDataFlow>()
      .mockResolvedValueOnce(valid)
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValue(valid);
    const initial = emptyCanvas();
    const { result, rerender } = renderHook(
      ({ document }) => useDataFlowAnalysis(document, true, analyze),
      { initialProps: { document: initial } },
    );
    await waitFor(() => expect(result.current.current).toEqual(valid));
    rerender({ document: { ...initial, title: "New" } });
    await waitFor(() => expect(result.current.error).toBe("offline"));
    expect(result.current.current).toBeNull();
    expect(result.current.analysis).toEqual(valid);
    act(() => result.current.retry());
    expect(result.current.pending).toBe(true);
    await waitFor(() => expect(result.current.current).toEqual(valid));
    expect(result.current.error).toBeNull();
  });
});
