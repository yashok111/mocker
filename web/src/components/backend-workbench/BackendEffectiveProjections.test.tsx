import { afterEach, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { json } from "@/test/http";
import type { BackendReadTarget, BackendEffectiveGraphPins } from "@/api/generated/schemas";
import { BackendEventsQueryLimitsValue } from "@/api/generated/schemas";
import { BackendFlow } from "./BackendFlow";
import { BackendDatabase } from "./BackendDatabase";
import { BackendEvents } from "./BackendEvents";
import { BackendAPIArtifacts, BackendAPIArtifactsContext } from "./BackendAPIArtifacts";
import { BackendArtifactProjections } from "./BackendArtifactProjections";
vi.mock("./BackendFlowGraph", () => ({ BackendFlowGraph: () => null }));
vi.mock("./BackendDatabaseGraph", () => ({ BackendDatabaseGraph: () => null }));
afterEach(() => vi.unstubAllGlobals());
const base = "0197aaf9-5555-7000-8000-000000000001",
  proposal = "0197aaf9-5555-7000-8000-000000000002",
  revision = "0197aaf9-5555-7000-8000-000000000003";
const hash = "a".repeat(64);
const coverage = {
  coverage: { status: "complete", knownObjects: 1, denominator: 1, gaps: [] },
  inventory: [],
  snapshots: [],
};
const artifact = {
  kind: "design_scenario" as const,
  id: "12",
  revisionId: "23",
  contentHash: hash,
};
function scope(full: boolean) {
  const target: BackendReadTarget = full
    ? { changeProposal: { proposalId: proposal, proposalRevisionId: revision } }
    : { revisionId: base };
  const pins: BackendEffectiveGraphPins = {
    baseRevisionId: base,
    baseSemanticHash: hash,
    effectiveSemanticHash: "b".repeat(64),
    sourceVectorHash: hash,
    targetHash: hash,
    viewSchemaVersion: full ? "proposal-graph-v1" : "6",
    structuralSchemaVersion: "6",
    sourceSnapshotIds: [],
    artifactPins: [artifact],
    artifactContext: null,
  };
  return { target, pins };
}
function server(full: boolean) {
  const { target, pins } = scope(full);
  const requests: { url: string; body: Record<string, unknown> }[] = [];
  vi.stubGlobal("fetch", async (url: RequestInfo | URL, init?: RequestInit) => {
    const path = String(url);
    const body = init?.body ? JSON.parse(String(init.body)) : {};
    requests.push({ url: path, body });
    const pinned = {
      projectId: "project",
      revisionId: base,
      semanticHash: pins.effectiveSemanticHash,
      target,
      pins,
      coverage,
      limitations: [],
      truncated: false,
      truncationReasons: [],
      nextCursor: "",
    };
    if (path.endsWith("/flow/query"))
      return json(200, {
        ...pinned,
        view: body.view,
        ...(body.view === "entrypoints"
          ? {
              entrypointItems: [
                {
                  operation: {
                    id: "entry",
                    kind: "http_operation",
                    name: "Desired endpoint",
                    attributes: {},
                    evidenceIds: [],
                  },
                  handlerIds: [],
                  flowIds: ["desired-flow"],
                  unresolvedHandles: [],
                  evidenceIds: [],
                  limitations: [],
                },
              ],
            }
          : body.view === "steps"
            ? {
                flowId: "desired-flow",
                stepItems: [
                  {
                    id: "step",
                    kind: "flow_step",
                    name: "Desired step",
                    parentId: "desired-flow",
                    attributes: {
                      stepKind: "validation",
                      analysisStatus: "complete",
                      gaps: [],
                      nativeText: "desired(input)",
                      transactionContext: { status: "unknown", reason: "Guarantee unknown" },
                    },
                    evidenceIds: [],
                  },
                ],
              }
            : body.view === "transitions"
              ? { flowId: "desired-flow", transitionItems: [] }
              : {
                  accessItems: [],
                  ...(body.entrypointId
                    ? { entrypointId: body.entrypointId }
                    : { dataNodeId: body.dataNodeId }),
                }),
      });
    if (path.endsWith("/graph/query"))
      return json(200, {
        viewSchemaVersion: pins.viewSchemaVersion,
        target,
        pins,
        nodes:
          body.recordType === "nodes"
            ? [
                {
                  id: "desired-store",
                  kind: "datastore",
                  name: "Desired store",
                  parentId: null,
                  attributes: {
                    relational: {
                      facets: {
                        qualified: {
                          dialect: "postgresql",
                          analysisStatus: "complete",
                          gaps: [],
                          qualifiedName: "desired",
                          databaseName: "desired",
                          nativeDefinition: null,
                        },
                      },
                    },
                  },
                  evidenceIds: [],
                },
              ]
            : [],
        edges: [],
        origins: [],
        identities: [],
        baselineEvidence: [],
        nextCursor: "",
      });
    if (path.endsWith("/database/query"))
      return json(200, {
        ...pinned,
        datastoreId: body.datastoreId,
        facetKey: body.facetKey,
        recordType: body.recordType,
        complete: true,
        tableItems: [],
        relationshipItems: [],
      });
    if (path.endsWith("/events/query"))
      return json(200, {
        ...pinned,
        view: body.view,
        policy: full ? "effective-events-v1" : "events-source6-query-v1",
        limits: BackendEventsQueryLimitsValue,
        complete: true,
        items: [],
      });
    if (path.endsWith("/api-artifacts/query"))
      return json(200, {
        revisionId: base,
        semanticHash: pins.effectiveSemanticHash,
        sourceSnapshotIds: [],
        target,
        effectivePins: pins,
        pins: [artifact],
        items: [],
        nextCursor: "",
      });
    if (path.endsWith("/artifacts/query"))
      return json(200, {
        revisionId: base,
        semanticHash: pins.effectiveSemanticHash,
        sourceSnapshotIds: [],
        target,
        effectivePins: pins,
        pins: [artifact],
        selectedPin: artifact,
        view: body.view,
        hashPolicy: "design-scenario-envelope-v1",
        apiBindings: [],
        editorBindings: [],
        bindingsComplete: true,
        items: [
          {
            id: "p",
            kind: "participant",
            label: "Desired participant",
            locator: { pin: artifact, view: "sequence", owner: { pointer: "/participants/0" } },
            sourceNodeIds: [],
            data: {
              kind: "participant",
              participant: {
                id: "p",
                name: "Desired participant",
                kind: "service",
                description: "",
              },
            },
            objectHash: hash,
            diagnostics: [],
          },
        ],
        nextCursor: "",
        resolution: { status: "resolved", diagnostics: [], updateAvailable: false },
        diagnostics: [],
        coverage: {
          itemsReturned: 1,
          totalItems: 1,
          nodesReturned: 1,
          edgesReturned: 0,
          diagnosticsReturned: 0,
          truncatedReasons: [],
        },
        complete: true,
      });
    return json(500, { error: "Unexpected read" });
  });
  return { target, pins, requests };
}
for (const full of [true, false]) {
  const label = full ? "full" : "native6";
  it(`${label} Flow loads desired child steps using the same target/pins`, async () => {
    const { target, pins, requests } = server(full);
    renderWithProviders(<BackendFlow projectId="project" target={target} pins={pins} />);
    await userEvent.click(
      await screen.findByRole("button", { name: "Открыть Flow Desired endpoint" }),
    );
    expect(
      await screen.findByRole("button", { name: /Открыть шаг Desired step/ }),
    ).toBeInTheDocument();
    await waitFor(() => expect(requests.some((r) => r.body.view === "steps")).toBe(true));
    for (const r of requests.filter((r) => r.url.endsWith("/flow/query")))
      expect(r.body).toMatchObject(target);
  });
  it(`${label} Database queries the effective datastore without selecting a repository owner`, async () => {
    const { target, pins, requests } = server(full);
    renderWithProviders(<BackendDatabase projectId="project" target={target} pins={pins} />);
    expect(
      await screen.findByRole("button", { name: "Открыть хранилище Desired store" }),
    ).toBeInTheDocument();
    await waitFor(() => expect(requests.some((r) => r.url.endsWith("/database/query"))).toBe(true));
    expect(requests.find((r) => r.url.endsWith("/database/query"))?.body).toMatchObject({
      ...target,
      datastoreId: "desired-store",
      facetKey: "qualified",
    });
    expect(screen.queryByLabelText("Предложение изменений")).not.toBeInTheDocument();
  });
  it(`${label} events validates the projection policy and exact target`, async () => {
    const { target, pins, requests } = server(full);
    renderWithProviders(<BackendEvents projectId="project" target={target} pins={pins} />);
    expect(await screen.findByText(/Другие маршруты и зависимости неизвестны/)).toBeInTheDocument();
    expect(requests[0]?.body).toMatchObject(target);
  });
  it(`${label} API artifacts consumes effectivePins without reading a committed revision`, async () => {
    const { target, pins, requests } = server(full);
    renderWithProviders(<BackendAPIArtifacts projectId="project" target={target} pins={pins} />);
    await waitFor(() =>
      expect(requests.some((r) => r.url.endsWith("/api-artifacts/query"))).toBe(true),
    );
    expect(requests.every((r) => !r.url.includes("/revisions/"))).toBe(true);
    expect(requests[0]?.body).toMatchObject(target);
  });
  it(`${label} scenario projects the exact desired owner revision and full edits stay in the proposal editor`, async () => {
    const { target, pins, requests } = server(full);
    renderWithProviders(
      <BackendAPIArtifactsContext
        value={{
          revision: {
            id: base,
            projectId: "project",
            semanticHash: hash,
            sourceSnapshotIds: [],
            artifactPins: [{ ...artifact, revisionId: "22" }],
          } as never,
          projectVersion: 1,
          canEdit: true,
          onDirty: vi.fn(),
          onApplied: vi.fn(),
        }}
      >
        <BackendArtifactProjections projectId="project" target={target} pins={pins} />
      </BackendAPIArtifactsContext>,
    );
    expect(await screen.findByText("Desired participant")).toBeInTheDocument();
    expect(screen.getByLabelText("Закреплённый артефакт")).toHaveTextContent("23");
    expect(requests.find((r) => r.url.endsWith("/artifacts/query"))?.body).toMatchObject(target);
    if (full)
      expect(
        screen.queryByRole("button", { name: /Изменить|Закрепить|Редактировать/ }),
      ).not.toBeInTheDocument();
  });
}
it("full source5 column inspector retains intent separately and never requests composed assertions", async () => {
  const { target, pins } = scope(true);
  const urls: string[] = [];
  const node = {
    id: "desired-column",
    kind: "column",
    name: "Desired nullable",
    parentId: "table",
    evidenceIds: ["proof"],
    attributes: {
      facets: {
        sql: {
          dialect: "postgresql",
          analysisStatus: "complete",
          gaps: [],
          nativeType: { status: "known", value: "integer" },
          typeFamily: { status: "known", value: "integer" },
          nullable: { status: "known", value: true },
          defaultExpression: { status: "known", value: null },
          generatedExpression: { status: "known", value: null },
          identity: { status: "unknown", reason: "unknown" },
          ordinal: { status: "known", value: 1 },
        },
      },
    },
  };
  vi.stubGlobal("fetch", async (url: RequestInfo | URL, init?: RequestInit) => {
    const path = String(url);
    urls.push(path);
    const envelope = { viewSchemaVersion: pins.viewSchemaVersion, target, pins };
    if (path.endsWith("/coverage")) return json(200, { ...envelope, ...coverage });
    if (path.includes("/nodes/"))
      return json(200, {
        ...envelope,
        node,
        origins: [
          {
            recordType: "node",
            subjectId: node.id,
            selector: {
              kind: "source_property",
              recordType: "node",
              id: node.id,
              source: { kind: "attributes", group: "nullable", facetKey: "sql" },
            },
            kind: "intent",
            reason: "Desired nullable change",
            commandId: revision,
            sourceClaims: [],
            evidenceIds: [],
          },
        ],
        baselineEvidence: [],
      });
    if (path.includes("/evidence"))
      return json(200, { ...envelope, items: [], nextCursor: "", baselineEvidence: [] });
    if (path.endsWith("/graph/query")) {
      const input = JSON.parse(String(init?.body));
      return json(200, {
        ...envelope,
        nodes: input.id === node.id ? [node] : [],
        edges: [],
        origins: [],
        identities: [],
        baselineEvidence: [],
        nextCursor: "",
      });
    }
    return json(500, { error: "Unexpected read" });
  });
  const { BackendValueInspector } = await import("./BackendValueInspector");
  renderWithProviders(
    <BackendValueInspector
      projectId="project"
      target={target}
      pins={pins}
      value={{ kind: "column", nodeId: node.id, facetKey: "sql" }}
      onClose={() => {}}
    />,
  );
  expect((await screen.findAllByText(/Desired nullable change/)).length).toBeGreaterThan(0);
  expect(screen.queryByText("currentness_missing")).not.toBeInTheDocument();
  expect(
    urls.every(
      (url) =>
        !url.includes("/backend-projects/project/revisions/") && !url.includes("/assertions"),
    ),
  ).toBe(true);
});
it("labels desired event structure separately from its baseline object evidence", async () => {
  const { target, pins } = scope(true);
  vi.stubGlobal("fetch", async () =>
    json(200, {
      projectId: "project",
      revisionId: base,
      semanticHash: pins.effectiveSemanticHash,
      target,
      pins,
      coverage,
      view: "routes",
      policy: "effective-events-v1",
      limits: BackendEventsQueryLimitsValue,
      complete: true,
      truncated: false,
      truncationReasons: [],
      limitations: [],
      nextCursor: "",
      items: [
        {
          kind: "route",
          route: {
            references: {},
            condition: { status: "known", value: "Desired condition" },
            group: { status: "unknown", reason: "Runtime group unknown" },
            dispatch: [],
            related: [],
            witness: {
              provenance: "desired",
              status: "desired",
              nodeIds: [],
              edgeIds: [],
              evidenceIds: ["baseline-proof"],
              limitations: [],
            },
          },
        },
      ],
    }),
  );
  renderWithProviders(<BackendEvents projectId="project" target={target} pins={pins} />);
  expect(
    await screen.findByText(/Исторические основания объектов базовой ревизии: baseline-proof/),
  ).toBeInTheDocument();
  expect(screen.getByText("Желаемая структура")).toBeInTheDocument();
  expect(screen.getByText(/Runtime group unknown/)).toBeInTheDocument();
});
