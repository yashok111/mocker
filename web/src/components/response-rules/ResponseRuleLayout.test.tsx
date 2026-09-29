import { act, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { renderWithProviders } from "@/test/render";
import { createFormDraftStore } from "../api-designer/forms/formDraftStore";
import type { DiagramLayoutInput, DiagramLayoutResult } from "../diagram/elkLayout";
import ResponseRulesEditor from "./ResponseRulesEditor";
import { headerTemplate, writeRules } from "./model";

const pending = vi.hoisted(() => ({
  resolve: undefined as ((layout: DiagramLayoutResult) => void) | undefined,
}));
vi.mock("../diagram/elkLayout", () => ({
  layoutDiagram: (_input: DiagramLayoutInput) =>
    new Promise<DiagramLayoutResult>((resolve) => {
      pending.resolve = resolve;
    }),
}));
vi.mock("./ResponseRuleGraph", () => ({ default: () => <div /> }));

it("keeps automatic layout blocked when a form is edited during the calculation", async () => {
  const rule = headerTemplate();
  const store = createFormDraftStore();
  const onChange = vi.fn();
  renderWithProviders(
    <ResponseRulesEditor
      designId={1}
      document={writeRules("{}", [rule])}
      blocked={null}
      formStore={store}
      onChange={onChange}
    />,
  );
  await act(async () => {
    store.set("/another-form", { source: "pending", propertySource: "original" });
  });
  await act(async () => {
    pending.resolve!({
      nodes: rule.nodes.map((node, index) => ({
        id: `node:${node.id}`,
        x: index * 300,
        y: 10,
        width: 240,
        height: 112,
      })),
      edges: [],
    });
  });
  expect(screen.getByRole("button", { name: "Автораскладка" })).toBeDisabled();
  expect(screen.queryByRole("button", { name: "Применить расстановку" })).not.toBeInTheDocument();
  expect(onChange).not.toHaveBeenCalled();
});
