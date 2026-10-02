import { expect, it } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/render";
import type { ArtifactProjectionData } from "@/api/generated/schemas";
import { ArtifactTypedContent, ArtifactEditorSide } from "./BackendArtifactContent";
const values: ArtifactProjectionData[] = [
  {
    kind: "participant",
    participant: { id: "p", name: "typed participant", kind: "service", description: "Authored" },
  },
  {
    kind: "sequence_message",
    sequenceMessage: {
      id: "m",
      fromId: "p",
      toId: "q",
      kind: "request",
      label: "typed message",
      description: "Authored",
    },
  },
  {
    kind: "fragment",
    fragment: {
      id: "f",
      kind: "alt",
      label: "typed fragment",
      fromMessageId: "m",
      toMessageId: "n",
      branches: [
        { id: "b1", label: "first", fromMessageId: "m", toMessageId: "m" },
        { id: "b2", label: "second", fromMessageId: "n", toMessageId: "n" },
      ],
    },
  },
  {
    kind: "state_diagram",
    stateDiagram: {
      id: "d",
      name: "typed diagram",
      initialStateId: "s",
      states: [],
      transitions: [],
    },
  },
  { kind: "state", state: { id: "s", name: "typed state", x: 0, y: 0, terminal: false } },
  {
    kind: "state_transition",
    stateTransition: {
      id: "t",
      name: "typed transition",
      from: "s",
      to: "q",
      patchJSON: '{"value":9007199254740993}',
      responseStatus: 200,
    },
  },
  { kind: "response_rule", responseRule: { id: "r", name: "typed rule", nodes: [], edges: [] } },
  {
    kind: "response_node",
    responseNode: { id: "n", type: "start", name: "typed start", x: 0, y: 0 },
  },
  {
    kind: "response_edge",
    responseEdge: { id: "typed response edge", from: "n", to: "x", port: "next" },
  },
  {
    kind: "event_node",
    eventNode: {
      id: "typed event node",
      kind: "participant",
      label: "node",
      locator: { pointer: "/event_model" },
    },
  },
  {
    kind: "event_edge",
    eventEdge: {
      id: "typed event edge",
      kind: "sends",
      source: "p",
      target: "m",
      label: "edge",
      locator: { pointer: "/event_model" },
    },
  },
  {
    kind: "event_server",
    eventServer: {
      id: "srv",
      name: "typed server",
      description: "authored",
      host: "localhost",
      protocol: "kafka",
      auth: "none",
    },
  },
  {
    kind: "event_channel",
    eventChannel: {
      id: "ch",
      name: "typed channel",
      description: "authored",
      address: "queue",
      serverIds: ["srv"],
      messageIds: ["msg"],
    },
  },
  {
    kind: "event_message",
    eventMessage: { id: "msg", name: "typed event message", description: "authored", examples: [] },
  },
  {
    kind: "event_schema",
    eventSchema: {
      id: "sch",
      name: "typed schema",
      description: "authored",
      schemaJSON: '{"const":9007199254740993}',
    },
  },
  {
    kind: "event_contract",
    eventContract: {
      id: "c",
      name: "typed contract",
      description: "authored",
      participantId: "p",
      version: "COPY0",
      operations: [],
    },
  },
  {
    kind: "event_operation",
    eventOperation: {
      id: "op",
      name: "typed operation",
      description: "authored",
      action: "send",
      channelId: "ch",
      messageId: "msg",
    },
  },
  {
    kind: "api_operation",
    apiOperation: {
      operationKey: "same-key",
      method: "get",
      path: "/orders",
      summary: "typed API operation",
      documentJSON: '{"x":9007199254740993}',
    },
  },
  {
    kind: "unsupported",
    unsupported: { documentJSON: '{"future":9007199254740993}', description: "typed unsupported" },
  },
];
it.each(values)("renders all authored fields for $kind", (data) => {
  const { container } = renderWithProviders(<ArtifactTypedContent data={data} />);
  expect(container.textContent).not.toBe("");
  if (data.kind === "fragment") {
    expect(
      screen.getByText("first").compareDocumentPosition(screen.getByText("second")) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  }
  if (data.kind === "event_schema")
    expect(screen.getByText('{"const":9007199254740993}')).toBeVisible();
});
it("renders projection-only group pins without a source identity", () => {
  renderWithProviders(
    <ArtifactEditorSide
      side={{
        pin: {
          kind: "design_scenario",
          id: "9007199254740993",
          revisionId: "9007199254740995",
          contentHash: "a".repeat(64),
        },
      }}
    />,
  );
  expect(screen.getByText(/9007199254740993/)).toBeVisible();
});
