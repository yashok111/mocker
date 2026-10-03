import { useState } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { screen, fireEvent } from "@testing-library/react";
import { renderWithProviders } from "@/test/render";
import { json } from "@/test/http";
import { BackendFlow } from "./BackendFlow";
import type { BackendSourcePin, FlowSelection } from "./backendFlowReads";
import type { BackendLineageValueRef } from "@/api/generated/schemas";
vi.mock("./BackendFlowGraph", () => ({ BackendFlowGraph: () => null }));
vi.mock("./BackendFlowInspector", () => ({
  BackendFlowInspector: ({
    selection,
    selectedValue,
    onSelect,
  }: {
    selection: FlowSelection;
    selectedValue?: BackendLineageValueRef;
    onSelect: (value: FlowSelection) => void;
  }) => (
    <section>
      <h3>Flow record {selection.id}</h3>
      {selectedValue && <p>Exact metadata {selectedValue.nodeId}</p>}
      <button
        onClick={() =>
          onSelect({
            type: "node",
            id: "A",
            valueRef: {
              kind: "event_field",
              nodeId: "A",
              endpointId: "consumer",
              routeId: "delivery-b",
            },
          })
        }
      >
        Open event_field A
      </button>
      <button
        onClick={() =>
          onSelect({
            type: "node",
            id: "A",
            valueRef: { kind: "column", nodeId: "A", facetKey: "facet" },
          })
        }
      >
        Open column A
      </button>
      <button
        onClick={() =>
          onSelect({
            type: "node",
            id: "A",
            valueRef: { kind: "port", nodeId: "A", collection: "results", portKey: "exact" },
          })
        }
      >
        Open port A
      </button>
    </section>
  ),
}));
vi.mock("./BackendValueInspector", () => ({
  BackendValueInspector: ({ value }: { value: BackendLineageValueRef }) => (
    <h3>
      {value.kind === "event_field"
        ? `Event record ${value.nodeId} ${value.endpointId} ${value.routeId}`
        : `Column record ${value.nodeId}`}
    </h3>
  ),
}));
afterEach(() => vi.unstubAllGlobals());
it.each(["column", "port", "event_field"])(
  "discards stale %s exact state on same-revision external pin B",
  async (kind) => {
    vi.stubGlobal("fetch", async (_url: RequestInfo | URL, init?: RequestInit) => {
      const input = JSON.parse(String(init?.body));
      return json(200, {
        projectId: "project",
        revisionId: "rev",
        view: input.view,
        semanticHash: "hash",
        entrypointItems: [],
        coverage: {
          coverage: { status: "partial", knownObjects: 0, denominator: null, gaps: [] },
          snapshots: [],
          inventory: [],
        },
        nextCursor: "",
        limitations: [],
        truncated: false,
        truncationReasons: [],
      });
    });
    function Harness() {
      const [pin, setPin] = useState<BackendSourcePin>({
        revisionId: "rev",
        recordType: "node",
        recordId: "start",
      });
      return (
        <>
          <button onClick={() => setPin({ revisionId: "rev", recordType: "node", recordId: "B" })}>
            External B
          </button>
          <button onClick={() => setPin({ revisionId: "rev", recordType: "node", recordId: "A" })}>
            External A
          </button>
          <BackendFlow projectId="project" revisionId="rev" pin={pin} onPinChange={setPin} />
        </>
      );
    }
    renderWithProviders(<Harness />);
    fireEvent.click(await screen.findByRole("button", { name: `Open ${kind} A` }));
    await screen.findByRole("heading", {
      name:
        kind === "column"
          ? "Column record A"
          : kind === "event_field"
            ? "Event record A consumer delivery-b"
            : "Flow record A",
    });
    if (kind === "port") expect(screen.getByText("Exact metadata A")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "External B" }));
    await screen.findByRole("heading", { name: "Flow record B" });
    expect(screen.queryByRole("heading", { name: "Column record A" })).not.toBeInTheDocument();
    expect(
      screen.queryByRole("heading", { name: "Event record A consumer delivery-b" }),
    ).not.toBeInTheDocument();
    expect(screen.queryByText("Exact metadata A")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "External A" }));
    await screen.findByRole("heading", { name: "Flow record A" });
    expect(screen.queryByText("Exact metadata A")).not.toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Column record A" })).not.toBeInTheDocument();
  },
);
