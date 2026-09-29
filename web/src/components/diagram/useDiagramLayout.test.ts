import { act, renderHook } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { useDiagramLayout } from "./useDiagramLayout";
import { layoutDiagram, type DiagramLayoutInput } from "./elkLayout";

vi.mock("./elkLayout", () => ({ layoutDiagram: vi.fn() }));
beforeEach(() => vi.mocked(layoutDiagram).mockReset());

it("reuses an equal semantic input when the editor rerenders after selection or movement", async () => {
  vi.mocked(layoutDiagram).mockResolvedValue({ nodes: [], edges: [] });
  const input: DiagramLayoutInput = { nodes: [], edges: [] };
  const view = renderHook(({ model }) => useDiagramLayout(model), {
    initialProps: { model: input },
  });
  await act(async () => {});
  expect(view.result.current.pending).toBe(false);
  view.rerender({ model: structuredClone(input) });
  await act(async () => {});
  expect(layoutDiagram).toHaveBeenCalledOnce();
});
