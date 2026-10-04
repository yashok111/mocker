import { afterEach, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { renderWithProviders } from "@/test/render";
import { json } from "@/test/http";
import {
  BackendAPIArtifacts,
  BackendAPIArtifactsContext,
  artifactNavigationHref,
} from "./BackendAPIArtifacts";
import { defaultParseSearch } from "@tanstack/react-router";
import { validateDesignSearch } from "@/routes/_authed.designs.$id";
const hash = "a".repeat(64);
const ref = {
  kind: "api_design",
  artifactId: "12",
  revisionId: "23",
  contentHash: hash,
  selector: { objectKey: "op-frozen" },
  objectHash: hash,
  lastKnownLabel: "Frozen operation",
  resolvedPointer: "/paths/~1orders/get",
};
const revision = {
  id: "0197aaf9-5555-7000-8000-000000000101",
  projectId: "project",
  parentRevisionId: "0197aaf9-5555-7000-8000-000000000101",
  semanticHash: hash,
  sourceSnapshotIds: ["snap"],
  artifactPins: [{ kind: "api_design", id: "12", revisionId: "23", contentHash: hash }],
};
function mount(onDirty = vi.fn(), onApplied = vi.fn()) {
  return {
    ...renderWithProviders(
      <BackendAPIArtifactsContext
        value={{
          revision: revision as never,
          projectVersion: 4,
          canEdit: true,
          onDirty,
          onApplied,
        }}
      >
        <BackendAPIArtifacts
          projectId="project"
          revisionId="0197aaf9-5555-7000-8000-000000000101"
          sourceNodeId="selected"
          sourceKind="http_operation"
        />
      </BackendAPIArtifactsContext>,
    ),
    onDirty,
    onApplied,
  };
}
function server(
  options: {
    apply?: (body: unknown) => Response | Promise<Response>;
    preview?: (body: unknown) => Response | Promise<Response>;
    genericEditors?: boolean;
    truncated?: boolean;
    orphan?: boolean;
  } = {},
) {
  const bodies: unknown[] = [];
  const fetch = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    const path = String(url);
    if (path.endsWith("/artifacts/query")) {
      const input = JSON.parse(String(init?.body));
      return json(200, {
        revisionId: input.revisionId,
        semanticHash: hash,
        sourceSnapshotIds: ["snap"],
        pins: revision.artifactPins,
        selectedPin: revision.artifactPins[0],
        view: input.view,
        hashPolicy: "api-design-raw-document-v1",
        apiBindings: (options.genericEditors ? ["selected"] : ["selected", "other"]).map(
          (sourceNodeId) => ({
            sourceNodeId,
            sourceKind: "http_operation",
            sourceLastKnownLabel: "Source operation",
            origin: "manual",
            reason: "prior",
            ref,
          }),
        ),
        editorBindings: options.genericEditors
          ? [
              {
                artifactKind: "api_design",
                artifactId: "12",
                selector: { kind: "state_diagram", diagramId: "d" },
                sourceNodeIds: ["selected"],
                sourceLabels: ["Frozen"],
                objectHash: hash,
                lastKnownLabel: "Saved state",
                origin: "manual",
                reason: "prior",
              },
            ]
          : [],
        bindingsComplete: true,
        items: [],
        nextCursor: "",
        resolution: { status: "resolved", diagnostics: [], updateAvailable: false },
        diagnostics: [],
        coverage: {
          itemsReturned: 0,
          totalItems: 0,
          nodesReturned: 0,
          edgesReturned: 0,
          diagnosticsReturned: 0,
          truncatedReasons: [],
        },
        complete: true,
      });
    }
    if (path.endsWith("/api-artifacts/query")) {
      const input = JSON.parse(String(init?.body));
      return json(200, {
        revisionId: input.revisionId,
        semanticHash:
          input.revisionId === "0197aaf9-5555-7000-8000-000000000201" ? "b".repeat(64) : hash,
        sourceSnapshotIds:
          input.revisionId === "0197aaf9-5555-7000-8000-000000000201" ? ["new-snap"] : ["snap"],
        pins: revision.artifactPins,
        items: [
          {
            binding: {
              sourceNodeId: input.cursor
                ? input.revisionId === "0197aaf9-5555-7000-8000-000000000201"
                  ? "new-other"
                  : "other"
                : "selected",
              sourceKind: "http_operation",
              sourceLastKnownLabel: "Source operation",
              origin: "manual",
              reason: "prior",
              ref,
            },
            resolution: {
              status: options.orphan ? "orphaned" : "resolved",
              currentDraftRevisionId: "24",
              updateAvailable: true,
              diagnostics: [],
            },
          },
        ],
        nextCursor: input.cursor ? "" : "0197aaf9-5555-7000-8000-000000000108",
      });
    }
    if (path === "/api/designs") return json(200, { designs: [{ id: 12, name: "Orders" }] });
    if (path === "/api/designs/12")
      return json(200, {
        design: { id: 12 },
        draft: { id: 24 },
        revisions: [
          { id: 23, summary: "Frozen" },
          { id: 24, summary: "New" },
        ],
      });
    if (path.endsWith("/artifacts/preview")) {
      const input = JSON.parse(String(init?.body));
      bodies.push(input);
      return json(200, {
        ...input,
        candidateHash: hash,
        semanticHash: hash,
        pins: revision.artifactPins,
        apiBindings: [],
        editorBindings: [],
        sourceSnapshotIds: ["snap"],
        diagnostics: [],
        diff: [],
        diffTruncated: false,
        canApply: true,
      });
    }
    if (path.endsWith("/api-artifacts/preview")) {
      const body = JSON.parse(String(init?.body));
      bodies.push(body);
      if (options.preview) return options.preview(body);
      return json(200, {
        baseRevisionId: body.baseRevisionId,
        expectedVersion: body.expectedVersion,
        candidateHash: hash,
        semanticHash: hash,
        pins: revision.artifactPins,
        bindings: [],
        sourceSnapshotIds:
          body.baseRevisionId === "0197aaf9-5555-7000-8000-000000000201" ? ["new-snap"] : ["snap"],
        canApply: !options.truncated,
        diffTruncated: !!options.truncated,
        diagnostics: [],
        diff: [
          {
            sourceNodeId: "selected",
            status: "missing",
            contextChanged: false,
            before: ref,
            changes: [],
          },
          {
            sourceNodeId: "selected",
            status: "changed",
            contextChanged: true,
            before: ref,
            after: ref,
            changes: [
              { pointer: "", kind: "changed", beforeHash: hash, afterHash: "b".repeat(64) },
            ],
          },
        ],
      });
    }
    if (path.endsWith("/api-artifacts/commands")) {
      const body = JSON.parse(String(init?.body));
      bodies.push(body);
      return options.apply
        ? options.apply(body)
        : json(200, {
            project: { id: "project", version: 5 },
            revision: { ...revision, id: "applied" },
          });
    }
    return json(500, { error: { code: "unrouted", message: path } });
  });
  vi.stubGlobal("fetch", fetch);
  return { fetch, bodies };
}
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
async function edit() {
  await userEvent.click(await screen.findByRole("button", { name: "Изменить связь API" }));
  await userEvent.selectOptions(screen.getByLabelText("Дизайн API"), "12");
  await userEvent.selectOptions(await screen.findByLabelText("Ревизия API"), "24");
  await userEvent.type(screen.getByLabelText("Object key операции"), "op-explicit");
  await userEvent.type(screen.getByLabelText(/Причина связи API/), "Explicit intent");
  await userEvent.click(screen.getByRole("button", { name: "Предпросмотр связей API" }));
}
it("previews a complete multi-page replacement, keeps duplicate source rows and acknowledges before adopting", async () => {
  const { bodies } = server();
  const { onDirty, onApplied } = mount();
  await edit();
  expect(bodies[0]).toMatchObject({
    commands: [
      {
        bindings: [
          { sourceNodeId: "other", selector: { objectKey: "op-frozen" } },
          { sourceNodeId: "selected", selector: { objectKey: "op-explicit" } },
        ],
      },
    ],
  });
  expect(await screen.findByText(/Контекст всего артефакта изменился/)).toBeInTheDocument();
  expect(screen.getAllByTestId("api-pin-diff-row")).toHaveLength(2);
  expect(onApplied).not.toHaveBeenCalled();
  await userEvent.click(screen.getByRole("button", { name: "Применить связи API" }));
  await waitFor(() =>
    expect(onApplied).toHaveBeenCalledWith(
      expect.objectContaining({ revision: expect.objectContaining({ id: "applied" }) }),
    ),
  );
  expect(onDirty).toHaveBeenCalledWith(true, expect.any(String));
});
it("retains exactly the lost-reply attempt and disables editing until exact retry resolves", async () => {
  let calls = 0;
  const { bodies } = server({
    apply: () =>
      ++calls === 1
        ? Promise.reject(new TypeError("Lost reply"))
        : json(200, {
            project: { id: "project", version: 5 },
            revision: { ...revision, id: "applied" },
          }),
  });
  mount();
  await edit();
  await screen.findByRole("button", { name: "Применить связи API" });
  await userEvent.click(screen.getByRole("button", { name: "Применить связи API" }));
  expect(await screen.findByText(/Результат применения неизвестен/)).toBeInTheDocument();
  expect(screen.getByLabelText(/Причина связи API/)).toBeDisabled();
  await userEvent.click(screen.getByRole("button", { name: "Повторить точную попытку API" }));
  await waitFor(() => expect(bodies).toHaveLength(3));
  expect(bodies[2]).toEqual(bodies[1]);
});
it("blocks truncated preview and exposes explicit orphan repair/removal", async () => {
  server({ truncated: true, orphan: true });
  mount();
  expect(await screen.findByText(/Узел исходника удалён/)).toBeInTheDocument();
  await edit();
  expect(await screen.findByRole("button", { name: "Применить связи API" })).toBeDisabled();
  expect(screen.getByRole("button", { name: "Удалить связь этого узла" })).toBeInTheDocument();
});
it("keeps unknown command/key through project head observation and never substitutes a newer API draft", async () => {
  const { bodies } = server({ apply: () => Promise.reject(new TypeError("Lost reply")) });
  function Host() {
    const [version, setVersion] = useState(4);
    return (
      <>
        <button onClick={() => setVersion(9)}>Observe head</button>
        <BackendAPIArtifactsContext
          value={{
            revision: revision as never,
            projectVersion: version,
            canEdit: version === 4,
            onDirty: vi.fn(),
            onApplied: vi.fn(),
          }}
        >
          <BackendAPIArtifacts
            projectId="project"
            revisionId="0197aaf9-5555-7000-8000-000000000101"
            sourceNodeId="selected"
            sourceKind="http_operation"
          />
        </BackendAPIArtifactsContext>
      </>
    );
  }
  renderWithProviders(<Host />);
  await edit();
  await userEvent.click(await screen.findByRole("button", { name: "Применить связи API" }));
  await screen.findByText(/Результат применения неизвестен/);
  await userEvent.click(screen.getByRole("button", { name: "Observe head" }));
  await userEvent.click(screen.getByRole("button", { name: "Повторить точную попытку API" }));
  await waitFor(() => expect(bodies).toHaveLength(3));
  expect(bodies[2]).toEqual(bodies[1]);
  expect(bodies[2]).toMatchObject({ expectedVersion: 4, commands: [{ revisionId: "24" }] });
});
it("preserves explicit remap intent after CAS reread and does not reuse the failed key", async () => {
  const { bodies } = server({
    apply: () => json(409, { error: { code: "backend_version_conflict", message: "CAS changed" } }),
  });
  renderWithProviders(
    <BackendAPIArtifactsContext
      value={{
        revision: revision as never,
        projectVersion: 4,
        canEdit: true,
        onDirty: vi.fn(),
        onApplied: vi.fn(),
        reread: async () => ({ revision: revision as never, projectVersion: 5 }),
      }}
    >
      <BackendAPIArtifacts
        projectId="project"
        revisionId="0197aaf9-5555-7000-8000-000000000101"
        sourceNodeId="selected"
        sourceKind="http_operation"
      />
    </BackendAPIArtifactsContext>,
  );
  await edit();
  await userEvent.click(await screen.findByRole("button", { name: "Применить связи API" }));
  await userEvent.click(
    await screen.findByRole("button", { name: "Перечитать источник и повторить предпросмотр" }),
  );
  expect(screen.getByLabelText("Object key операции")).toHaveValue("op-explicit");
  expect(screen.getByLabelText(/Причина связи API/)).toHaveValue("Explicit intent");
  await userEvent.click(screen.getByRole("button", { name: "Предпросмотр связей API" }));
  await waitFor(() => expect(bodies).toHaveLength(3));
  expect(bodies[2]).toMatchObject({
    expectedVersion: 5,
    commands: [{ revisionId: "24", reason: "Explicit intent" }],
  });
});
it("exposes orphan group removal in the project roster without a live source inspector", async () => {
  const { bodies } = server({ orphan: true });
  renderWithProviders(
    <BackendAPIArtifactsContext
      value={{
        revision: revision as never,
        projectVersion: 4,
        canEdit: true,
        onDirty: vi.fn(),
        onApplied: vi.fn(),
      }}
    >
      <BackendAPIArtifacts projectId="project" revisionId="0197aaf9-5555-7000-8000-000000000101" />
    </BackendAPIArtifactsContext>,
  );
  await userEvent.click((await screen.findAllByRole("button", { name: "Изменить связь API" }))[0]!);
  await userEvent.type(screen.getByLabelText(/Причина связи API/), "Explicit orphan removal");
  await userEvent.click(screen.getByRole("button", { name: "Удалить группу API" }));
  await waitFor(() => expect(bodies).toHaveLength(1));
  expect(bodies[0]).toMatchObject({
    commands: [{ type: "remove_api_pin", artifactId: "12", reason: "Explicit orphan removal" }],
  });
});
it("removes the previous source binding when explicitly remapping a deleted field", async () => {
  const { bodies } = server({ orphan: true });
  mount();
  await edit();
  const source = screen.getByLabelText("Исходный узел для явного переназначения");
  await userEvent.clear(source);
  await userEvent.type(source, "replacement-node");
  await userEvent.click(screen.getByRole("button", { name: "Предпросмотр связей API" }));
  await waitFor(() => expect(bodies).toHaveLength(2));
  expect(bodies[1]).toMatchObject({
    commands: [{ bindings: [{ sourceNodeId: "other" }, { sourceNodeId: "replacement-node" }] }],
  });
});
it("explicitly rereads an advanced source head without remounting or discarding selector/reason intent", async () => {
  const { bodies } = server({
    apply: () =>
      json(409, { error: { code: "backend_base_conflict", message: "Source head changed" } }),
  });
  renderWithProviders(
    <BackendAPIArtifactsContext
      value={{
        revision: revision as never,
        projectVersion: 4,
        canEdit: true,
        onDirty: vi.fn(),
        onApplied: vi.fn(),
        reread: async () => ({
          revision: {
            ...revision,
            id: "0197aaf9-5555-7000-8000-000000000201",
            semanticHash: "b".repeat(64),
            sourceSnapshotIds: ["new-snap"],
          } as never,
          projectVersion: 8,
        }),
      }}
    >
      <BackendAPIArtifacts
        projectId="project"
        revisionId="0197aaf9-5555-7000-8000-000000000101"
        sourceNodeId="selected"
        sourceKind="http_operation"
      />
    </BackendAPIArtifactsContext>,
  );
  await edit();
  await userEvent.click(await screen.findByRole("button", { name: "Применить связи API" }));
  await userEvent.click(
    await screen.findByRole("button", { name: "Перечитать источник и повторить предпросмотр" }),
  );
  expect(
    await screen.findByText(/База редактирования: 0197aaf9-5555-7000-8000-000000000201/),
  ).toHaveTextContent("снимки new-snap · версия проекта 8");
  expect(screen.getByLabelText("Object key операции")).toHaveValue("op-explicit");
  expect(screen.getByLabelText(/Причина связи API/)).toHaveValue("Explicit intent");
  await userEvent.click(screen.getByRole("button", { name: "Предпросмотр связей API" }));
  expect(
    await screen.findByText(/База предпросмотра: 0197aaf9-5555-7000-8000-000000000201/),
  ).toBeInTheDocument();
  expect(bodies[2]).toMatchObject({
    baseRevisionId: "0197aaf9-5555-7000-8000-000000000201",
    expectedVersion: 8,
    commands: [
      {
        reason: "Explicit intent",
        revisionId: "24",
        bindings: [
          { sourceNodeId: "new-other" },
          { sourceNodeId: "selected", selector: { objectKey: "op-explicit" } },
        ],
      },
    ],
  });
});
it("aborts and discards a delayed preview when the selected source node changes", async () => {
  let resolve!: (response: Response) => void;
  const { fetch } = server({
    preview: () =>
      new Promise((done) => {
        resolve = done;
      }),
  });
  function Host() {
    const [source, setSource] = useState("selected");
    return (
      <>
        <button onClick={() => setSource("next-source")}>Switch source</button>
        <BackendAPIArtifactsContext
          value={{
            revision: revision as never,
            projectVersion: 4,
            canEdit: true,
            onDirty: vi.fn(),
            onApplied: vi.fn(),
          }}
        >
          <BackendAPIArtifacts
            projectId="project"
            revisionId="0197aaf9-5555-7000-8000-000000000101"
            sourceNodeId={source}
            sourceKind="http_operation"
          />
        </BackendAPIArtifactsContext>
      </>
    );
  }
  renderWithProviders(<Host />);
  await edit();
  await waitFor(() => expect(resolve).toBeDefined());
  const signal = fetch.mock.calls.find(([url]) =>
    String(url).endsWith("/api-artifacts/preview"),
  )?.[1]?.signal;
  await userEvent.click(screen.getByRole("button", { name: "Switch source" }));
  expect(signal?.aborted).toBe(true);
  resolve(
    json(200, {
      baseRevisionId: "0197aaf9-5555-7000-8000-000000000101",
      expectedVersion: 4,
      candidateHash: hash,
      semanticHash: hash,
      pins: [],
      bindings: [],
      sourceSnapshotIds: ["snap"],
      diagnostics: [],
      diff: [{ sourceNodeId: "selected", status: "changed", contextChanged: true, changes: [] }],
      diffTruncated: false,
      canApply: true,
    }),
  );
  await screen.findByText("Ручных связей API нет.");
  expect(screen.queryByTestId("api-pin-preview")).not.toBeInTheDocument();
});
it("explains work-limit failures and keeps preview/apply unavailable", async () => {
  server({
    preview: () =>
      json(413, { error: { code: "backend_api_pins_limit", message: "Snapshot work limit" } }),
  });
  mount();
  await edit();
  expect(await screen.findByRole("alert")).toHaveTextContent("Требуется более узкий запрос");
  expect(screen.queryByRole("button", { name: "Применить связи API" })).not.toBeInTheDocument();
});
it.each(["123", "true", "false", "null", '{"opaque":1}', "123.5", "1e3"])(
  "roundtrips exact pinned strings and opaque object key %s through the real router parser and route validator",
  (key) => {
    const href = artifactNavigationHref(
      {
        artifactId: "12",
        revisionId: "23",
        contentHash: "1".repeat(64),
        selector: { objectKey: key },
      },
      "0197aaf9-5555-7000-8000-000000000001",
      "0197aaf9-5555-7000-8000-000000000002",
    );
    expect(href).toBeDefined();
    const parsed = defaultParseSearch(new URL(href!, "http://localhost").search);
    expect(validateDesignSearch(parsed)).toMatchObject({
      pinnedRevisionId: "23",
      pinnedHash: "1".repeat(64),
      pinnedObjectKey: key,
    });
  },
);

it("legacy last-node unlink uses generic set and preserves invisible editor associations", async () => {
  const { bodies } = server({ genericEditors: true });
  mount();
  await userEvent.click(await screen.findByRole("button", { name: "Изменить связь API" }));
  await userEvent.type(screen.getByLabelText(/Причина связи API/), "unlink only");
  await userEvent.click(screen.getByRole("button", { name: "Удалить связь этого узла" }));
  await screen.findByRole("button", { name: "Применить полную группу API и моделей" });
  expect(bodies[0]).toMatchObject({
    commands: [
      {
        type: "set_artifact_pin",
        artifact: { kind: "api_design", id: "12" },
        revisionId: "23",
        apiBindings: [],
        editorBindings: [
          { selector: { kind: "state_diagram", diagramId: "d" }, sourceNodeIds: ["selected"] },
        ],
      },
    ],
  });
  expect(bodies[0]).not.toHaveProperty("commands.0.editorBindings.0.objectHash");
});
it("keeps a full proposal in the artifact owner backlink without replacing it with baseline", () => {
  const target = {
    changeProposal: {
      proposalId: "0197aaf9-5555-7000-8000-000000000001",
      proposalRevisionId: "0197aaf9-5555-7000-8000-000000000002",
    },
  };
  const href = artifactNavigationHref(
    ref as never,
    "project",
    "0197aaf9-5555-7000-8000-000000000003",
    "field",
    target,
  );
  const search = defaultParseSearch(href!.slice(href!.indexOf("?")));
  expect(search).toMatchObject({
    pinnedRevisionId: "23",
    pinnedHash: hash,
    returnProjectId: "project",
    returnChangeProposalId: target.changeProposal.proposalId,
    returnProposalRevisionId: target.changeProposal.proposalRevisionId,
    returnSourceNodeId: "field",
  });
  expect(search).not.toHaveProperty("returnRevisionId");
});
