import { act, screen, waitFor } from "@testing-library/react";
import { useState } from "react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import { renderWithProviders } from "@/test/render";
import type { DiagramLayoutInput, DiagramLayoutResult } from "../diagram/elkLayout";
import StateDiagramEditor from "./StateDiagramEditor";
import { EXTENSION, orderTemplate, writeDiagrams } from "./model";

const requests = vi.hoisted(() => ({
  pending: [] as {
    input: DiagramLayoutInput;
    resolve: (layout: DiagramLayoutResult) => void;
    reject: (error: Error) => void;
  }[],
}));
vi.mock("../diagram/elkLayout", () => ({
  layoutDiagram: (input: DiagramLayoutInput) =>
    new Promise<DiagramLayoutResult>((resolve, reject) => {
      requests.pending.push({ input, resolve, reject });
    }),
}));
vi.mock("./StateGraph", () => ({ default: () => <div /> }));
beforeEach(() => {
  requests.pending = [];
});

it("ignores late layout from a replaced diagram and never writes without the explicit action", async () => {
  const original = orderTemplate();
  original.states = [original.states[0]!];
  original.transitions = [];
  const onChange = vi.fn();
  const replacement = {
    ...original,
    states: [{ ...original.states[0]!, id: "replacement", name: "Новое" }],
    initialStateId: "replacement",
  };
  function Harness() {
    const [diagram, setDiagram] = useState(original);
    return (
      <>
        <StateDiagramEditor
          designId={1}
          document={writeDiagrams({}, [diagram])}
          blocked={false}
          onChange={onChange}
        />
        <button onClick={() => setDiagram(replacement)}>Заменить диаграмму</button>
      </>
    );
  }
  renderWithProviders(<Harness />);
  await userEvent.click(screen.getByRole("button", { name: "Заменить диаграмму" }));
  expect(requests.pending).toHaveLength(2);
  await act(async () => {
    requests.pending[0]!.resolve({
      nodes: [{ id: "state:created", x: 10, y: 20, width: 190, height: 76 }],
      edges: [],
    });
  });
  expect(screen.getByRole("button", { name: "Автораскладка" })).toBeDisabled();
  expect(onChange).not.toHaveBeenCalled();
  await act(async () => {
    requests.pending[1]!.resolve({
      nodes: [{ id: "state:replacement", x: 100, y: 200, width: 190, height: 76 }],
      edges: [],
    });
  });
  await userEvent.click(screen.getByRole("button", { name: "Автораскладка" }));
  expect(onChange).toHaveBeenCalledOnce();
  expect(onChange.mock.calls[0]![0][EXTENSION].diagrams[0].states).toEqual([
    { ...replacement.states[0], x: 100, y: 200 },
  ]);
});

it("shows layout failure and retains the saved coordinates", async () => {
  const onChange = vi.fn();
  renderWithProviders(
    <StateDiagramEditor
      designId={1}
      document={writeDiagrams({}, [orderTemplate()])}
      blocked={false}
      onChange={onChange}
    />,
  );
  await act(async () => {
    requests.pending[0]!.reject(new Error("engine failed"));
  });
  await waitFor(() =>
    expect(screen.getByText("Не удалось рассчитать расположение графа.")).toBeInTheDocument(),
  );
  expect(screen.getByRole("button", { name: "Автораскладка" })).toBeDisabled();
  expect(onChange).not.toHaveBeenCalled();
});
