import { afterEach, expect, it, vi } from "vitest";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { json } from "@/test/http";
import { BackendEvents } from "./BackendEvents";
import { BackendValueInspector } from "./BackendValueInspector";
const hash = "a".repeat(64);
const field = {
  id: "field",
  name: "Order ID",
  kind: "event_field",
  parentId: "message",
  attributes: { section: "payload", path: [] },
  evidenceIds: [],
};
const producer = {
  id: "producer",
  name: "Emit order",
  kind: "flow_step",
  parentId: "producer-flow",
  attributes: {
    stepKind: "emit",
    analysisStatus: "complete",
    gaps: [],
    outputs: [{ key: "order", name: "Order port" }],
  },
  evidenceIds: [],
};
const event = {
  kind: "event_field",
  nodeId: "field",
  endpointId: "consumer",
  routeId: "delivery-b",
};
const port = { kind: "port", nodeId: "producer", collection: "outputs", portKey: "order" };
const producerEvent = {
  kind: "event_field",
  nodeId: "field",
  endpointId: "producer",
  routeId: "emit",
};
const witness = {
  provenance: "source",
  status: "explicit",
  nodeIds: ["producer", "consumer", "message"],
  edgeIds: ["emit", "delivery-b"],
  evidenceIds: [],
  limitations: [],
};
function fixture() {
  vi.stubGlobal("fetch", async (url: RequestInfo | URL, init?: RequestInit) => {
    const path = String(url).split("?")[0] ?? "",
      input = init?.body ? JSON.parse(String(init.body)) : {};
    if (path.endsWith("/revisions/0197aaf9-5555-7000-8000-000000000115"))
      return json(200, {
        id: "0197aaf9-5555-7000-8000-000000000115",
        projectId: "project",
        schemaVersion: "5",
        semanticHash: hash,
      });
    if (path.endsWith("/nodes/producer")) return json(200, producer);
    if (path.endsWith("/nodes/field")) return json(200, field);
    if (path.endsWith("/graph/query")) return json(200, { nodes: [], edges: [], nextCursor: "" });
    if (path.endsWith("/evidence")) return json(200, { items: [], nextCursor: "" });
    if (path.endsWith("/events/query"))
      return json(200, {
        projectId: "project",
        revisionId: "0197aaf9-5555-7000-8000-000000000115",
        semanticHash: hash,
        policy: "source-events-projection-v1",
        view: "routes",
        ...(input.seedNodeId ? { seedNodeId: input.seedNodeId } : {}),
        items: [
          {
            kind: "route",
            route: {
              references: {
                producerId: "producer",
                consumerId: "consumer",
                messageId: "message",
                channelId: "channel",
                emitsEdgeId: "emit",
                deliveryEdgeId: "delivery-b",
              },
              condition: { status: "known", value: "configured" },
              group: { status: "known", value: "group" },
              dispatch: [
                {
                  handlerId: "handler",
                  handlesEdgeId: "handles",
                  flowIds: ["consumer-flow"],
                  witness,
                },
              ],
              related: [],
              witness,
            },
          },
        ],
        nextCursor: "",
        complete: true,
        truncated: false,
        truncationReasons: [],
        limitations: [],
        totalEdgeCount: 5,
        examinedEdgeCount: 5,
        constructedItemCount: 1,
        auxiliaryRecordCount: 3,
        limits: {
          maxExaminedEdges: 20000,
          maxItems: 5000,
          maxAuxiliaryRecords: 20000,
          maxWitnessRecords: 256,
          defaultPageSize: 50,
          maxPageSize: 100,
          scanPolicy: "complete-scan-admission",
        },
        coverage: {
          coverage: { status: "partial", knownObjects: 5, denominator: null, gaps: [] },
          snapshots: [],
          inventory: [],
        },
      });
    if (path.endsWith("/lineage/query"))
      return json(200, {
        projectId: "project",
        revisionId: "0197aaf9-5555-7000-8000-000000000115",
        semanticHash: hash,
        policy: "field-lineage-traversal-v2",
        seed: input.seed,
        direction: input.direction,
        items: [
          {
            mapping: {
              id: "mapping",
              name: "Serialize order",
              kind: "field_mapping",
              parentId: "producer",
              attributes: {
                sources: [port],
                destination: producerEvent,
                transform: { kind: "copy", description: "Serialize", redacted: false },
                analysisStatus: "complete",
                gaps: [],
              },
              evidenceIds: [],
            },
            via: port,
            depth: 1,
            witnessMappingIds: ["mapping"],
            status: "explicit",
            expansion: "expanded",
            expandedValues: [producerEvent],
            reasons: [],
            requiresReview: false,
          },
          {
            mapping: {
              id: "transport",
              name: "Transport order",
              kind: "field_mapping",
              parentId: "consumer",
              attributes: {
                sources: [producerEvent],
                destination: event,
                transport: { emitsEdgeId: "emit", deliveryEdgeId: "delivery-b" },
                transform: { kind: "copy", description: "Explicit route", redacted: false },
                analysisStatus: "complete",
                gaps: [],
              },
              evidenceIds: [],
            },
            via: producerEvent,
            depth: 2,
            witnessMappingIds: ["mapping", "transport"],
            status: "explicit",
            expansion: "expanded",
            expandedValues: [event],
            reasons: [],
            requiresReview: false,
          },
        ],
        coverage: {
          coverage: { status: "partial", knownObjects: 5, denominator: null, gaps: [] },
          snapshots: [],
          inventory: [],
        },
        nextCursor: "",
        truncated: false,
        truncationReasons: [],
        limitations: [],
        visitedValueCount: 3,
        examinedMappingCount: 2,
      });
    return json(404, {});
  });
}
afterEach(() => vi.unstubAllGlobals());
it.each(["events record", "event endpoint"])(
  "keeps the contextual address through %s → port lineage → event value",
  async (entry) => {
    fixture();
    const user = userEvent.setup();
    renderWithProviders(
      entry === "events record" ? (
        <BackendEvents
          projectId="project"
          revisionId="0197aaf9-5555-7000-8000-000000000115"
          semanticHash={hash}
        />
      ) : (
        <BackendValueInspector
          projectId="project"
          revisionId="0197aaf9-5555-7000-8000-000000000115"
          value={{ kind: "event_field", nodeId: "field", endpointId: "producer", routeId: "emit" }}
          onClose={() => {}}
        />
      ),
    );
    await user.click(
      await screen.findByRole("button", {
        name: entry === "events record" ? "Отправитель producer" : "Открыть endpoint producer",
      }),
    );
    const inspector = await screen.findByRole("region", { name: "Инспектор Flow" });
    await user.click(
      await within(inspector).findByRole("button", {
        name: "Открыть точное значение Order port · outputs · order",
      }),
    );
    const launch = within(inspector).getByRole("button", { name: "Куда передаётся значение" });
    await vi.waitFor(() => expect(launch).toBeEnabled());
    await user.click(launch);
    await user.click(
      await screen.findByRole("button", {
        name: "Открыть значение field · поле события · consumer · маршрут delivery-b",
      }),
    );
    expect(
      await screen.findByText("Маршрут значения: delivery-b · endpoint: consumer"),
    ).toBeVisible();
    expect(await screen.findByRole("button", { name: "Открыть endpoint consumer" })).toBeVisible();
    expect(
      (await screen.findAllByRole("button", { name: "Открыть Flow consumer-flow" })).length,
    ).toBeGreaterThan(0);
  },
);
it("returns keyboard focus to the nested endpoint launch button after closing its record", async () => {
  fixture();
  const user = userEvent.setup();
  renderWithProviders(
    <BackendValueInspector
      projectId="project"
      revisionId="0197aaf9-5555-7000-8000-000000000115"
      value={{ kind: "event_field", nodeId: "field", endpointId: "producer", routeId: "emit" }}
      onClose={() => {}}
    />,
  );
  const launch = await screen.findByRole("button", { name: "Открыть endpoint producer" });
  launch.focus();
  await user.keyboard("{Enter}");
  await vi.waitFor(() => expect(screen.getByRole("heading", { name: "Emit order" })).toHaveFocus());
  await user.tab();
  expect(screen.getByRole("button", { name: "Закрыть инспектор Flow" })).toHaveFocus();
  await user.keyboard("{Enter}");
  await vi.waitFor(() => expect(launch).toHaveFocus());
});
