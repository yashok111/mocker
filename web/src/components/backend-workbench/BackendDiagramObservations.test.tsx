import { MantineProvider } from "@mantine/core";
import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { BackendDiagramObservations } from "./BackendDiagramObservations";
import type { BackendDiagramObserved, BackendDiagramScope } from "@/api/generated/schemas";
it("does not show another diagram's saved overlay", () => {
  const scope = {
    pin: { id: "diagram-a", version: 1, contentHash: "hash" },
    scopeHash: "scope",
  } as BackendDiagramScope;
  const report = {
    diagramScope: { ...scope, pin: { ...scope.pin, id: "diagram-b" } },
    elements: [],
    relations: [],
    gaps: [],
    pins: [],
  } as unknown as BackendDiagramObserved;
  render(
    <MantineProvider>
      <BackendDiagramObservations report={report} scope={scope} />
    </MantineProvider>,
  );
  expect(
    screen.getByText("Этот overlay относится к другой точной области диаграммы."),
  ).toBeInTheDocument();
  expect(screen.queryByTestId("backend-diagram-observations")).not.toBeInTheDocument();
});
it("keeps containment distinct from event precedence and shows gaps", () => {
  const scope = {
    pin: { id: "diagram-a", version: 1, contentHash: "hash" },
    scopeHash: "scope",
  } as BackendDiagramScope;
  const report = {
    diagramScope: scope,
    elements: [],
    relations: [
      { from: "a", to: "b", kind: "containment" },
      { from: "a/send", to: "c/receive", kind: "event_precedence" },
    ],
    gaps: ["missing peer"],
    pins: [],
  } as unknown as BackendDiagramObserved;
  render(
    <MantineProvider>
      <BackendDiagramObservations report={report} scope={scope} />
    </MantineProvider>,
  );
  expect(screen.getByText("a → b: containment")).toBeInTheDocument();
  expect(screen.getByText("a/send → c/receive: event_precedence")).toBeInTheDocument();
  expect(screen.getByText("missing peer")).toBeInTheDocument();
});
