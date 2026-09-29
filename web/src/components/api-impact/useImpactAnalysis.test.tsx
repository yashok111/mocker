import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { json } from "@/test/http";
import type { ApiImpactReport } from "@/api/generated/schemas";
import { useImpactAnalysis } from "./useImpactAnalysis";

const report: ApiImpactReport = {
  designId: 12,
  version: 2,
  fromRevisionId: 41,
  fromHash: "a",
  proposedHash: "b",
  changes: [],
  affected: [],
  evidence: [],
  diagnostics: [],
  complete: true,
  coverage: {
    scenariosScanned: 0,
    scenarioUsagesReturned: 0,
    changesReturned: 0,
    entitiesReturned: 0,
    evidenceReturned: 0,
    truncatedReasons: [],
  },
};
const local = { designId: 12, fromRevisionId: 41, document: '{ "value":9007199254740993 }' };
afterEach(() => vi.unstubAllGlobals());

describe("useImpactAnalysis", () => {
  it("runs explicitly with the exact string and exposes only a current result", async () => {
    const fetch = vi.fn().mockResolvedValue(json(200, report));
    vi.stubGlobal("fetch", fetch);
    const view = renderHook(({ request, blocked }) => useImpactAnalysis(request, blocked), {
      initialProps: { request: local, blocked: false },
    });
    expect(fetch).not.toHaveBeenCalled();
    await act(() => view.result.current.analyze());
    expect(JSON.parse(fetch.mock.calls[0]![1].body)).toEqual({
      fromRevisionId: 41,
      document: local.document,
    });
    expect(view.result.current.report).toEqual(report);
    view.rerender({ request: { ...local, fromRevisionId: 42 }, blocked: false });
    expect(view.result.current.report).toBeNull();
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it("aborts changed requests and discards late responses even when fetch ignores cancellation", async () => {
    let finish!: (response: Response) => void;
    const fetch = vi.fn().mockImplementation(
      () =>
        new Promise<Response>((resolve) => {
          finish = resolve;
        }),
    );
    vi.stubGlobal("fetch", fetch);
    const view = renderHook(({ document }) => useImpactAnalysis({ ...local, document }, false), {
      initialProps: { document: local.document },
    });
    act(() => {
      void view.result.current.analyze();
    });
    const signal = fetch.mock.calls[0]![1].signal as AbortSignal;
    view.rerender({ document: "{}" });
    expect(signal.aborted).toBe(true);
    await act(async () => finish(json(200, report)));
    expect(view.result.current.report).toBeNull();
  });

  it("invalidates on pending forms, remains invalid after cancellation and aborts on unmount", async () => {
    const fetch = vi.fn().mockResolvedValue(json(200, report));
    vi.stubGlobal("fetch", fetch);
    const view = renderHook(({ blocked }) => useImpactAnalysis(local, blocked), {
      initialProps: { blocked: false },
    });
    await act(() => view.result.current.analyze());
    view.rerender({ blocked: true });
    expect(view.result.current.report).toBeNull();
    view.rerender({ blocked: false });
    expect(view.result.current.report).toBeNull();
    await act(() => view.result.current.analyze());
    view.unmount();
    expect(fetch.mock.calls[1]![1].signal.aborted).toBe(true);
  });

  it("sends historical IDs, reports errors and allows retry", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(json(500, { error: { code: "internal", message: "database" } }))
      .mockResolvedValueOnce(json(200, { ...report, toRevisionId: 45 }));
    vi.stubGlobal("fetch", fetch);
    const view = renderHook(() =>
      useImpactAnalysis({ designId: 12, fromRevisionId: 41, toRevisionId: 45 }, false),
    );
    await act(() => view.result.current.analyze());
    expect(view.result.current.error).not.toBeNull();
    await act(() => view.result.current.analyze());
    await waitFor(() => expect(view.result.current.report?.toRevisionId).toBe(45));
    expect(JSON.parse(fetch.mock.calls[1]![1].body)).toEqual({
      fromRevisionId: 41,
      toRevisionId: 45,
    });
  });
});
