import { afterEach, expect, it, vi } from "vitest";
import { cleanup, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { json } from "@/test/http";
import { Inspector } from "./Inspector";
import { projectId, revisionId, nodeId, workspaceHTTP, mapPage } from "./testFixtures";
import type { MapNode } from "./model";

const nativeText =
  '\tkernel, err := di.InitKernel(ctx, cmd)\n\tif err != nil {\n\t\treturn fmt.Errorf("<kernel>: %w", err)\n\t}\n';
const step: MapNode = {
  id: nodeId,
  kind: "flow_step",
  name: "Initialize application kernel",
  description: "Initialize application kernel",
  parentId: null,
  childCount: 0,
  attributes: { stepKind: "call" },
  origin: "Исходный код",
};
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it.each([true, false])(
  "omits related diagrams regardless of architecture availability (failure=%s)",
  async (failure) => {
    const diagramId = "0197aaf9-5555-7000-8000-000000000011";
    const pin = { id: diagramId, version: 1, contentHash: "c".repeat(64) };
    workspaceHTTP((path) => {
      if (path.endsWith("/diagrams"))
        return json(200, {
          catalogVersion: 1,
          nextCursor: "",
          items: [
            {
              id: diagramId,
              pin,
              kind: "architecture",
              name: "Architecture",
              targetHash: "a".repeat(64),
              target: { revisionId },
            },
          ],
        });
      if (path.endsWith(`/diagrams/${diagramId}/versions/1`))
        return failure
          ? json(500, { error: { code: "unavailable", message: "Unavailable" } })
          : json(200, {
              projectId,
              pin,
              targetHash: "a".repeat(64),
              document: {
                kind: "architecture",
                target: { revisionId },
                payload: {
                  primarySystemId: "system",
                  elements: [
                    {
                      id: "system",
                      role: "software_system",
                      label: "Architecture",
                      responsibility: "Boundary",
                      origin: { kind: "authored", reason: "Fixture" },
                      refs: [],
                    },
                  ],
                  links: [
                    { id: "link", refs: [{ kind: "record", recordType: "node", id: nodeId }] },
                  ],
                },
              },
            });
      if (path.endsWith("/diagram-views"))
        return json(200, { catalogVersion: 1, nextCursor: "", items: [] });
      return undefined;
    });
    const navigate = vi.fn();
    renderWithProviders(
      <Inspector
        projectId={projectId}
        target={{ revisionId }}
        search={{ revisionId }}
        id={nodeId}
        node={step}
        onClose={vi.fn()}
        onEnter={vi.fn()}
        onNavigate={navigate}
      />,
    );
    expect(screen.queryByRole("tablist")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /Architecture · Архитектура/ }),
    ).not.toBeInTheDocument();
    expect(vi.mocked(fetch).mock.calls.some(([url]) => String(url).includes("/diagrams"))).toBe(
      false,
    );
  },
);

it("omits callback relations and does not load the removed neighborhood section", async () => {
  const callback = {
    ...step,
    id: "0197aaf9-5555-7000-8000-000000000009",
    kind: "symbol",
    name: "Callback body",
  };
  workspaceHTTP((path) =>
    path.endsWith("/explore/query")
      ? json(200, {
          ...mapPage(),
          nodes: [step, callback],
          edges: [
            {
              id: "argument",
              kind: "callback_argument",
              from: nodeId,
              to: callback.id,
              label: "Callback",
              attributes: {
                argumentPosition: 0,
                invocationKnowledge: "unknown",
                reason: "External invoker",
              },
            },
          ],
          edgeTotal: 1,
        })
      : undefined,
  );
  const navigate = vi.fn();
  renderWithProviders(
    <Inspector
      projectId={projectId}
      target={{ revisionId }}
      search={{ revisionId, entrypointId: "caller" }}
      id={nodeId}
      node={step}
      onClose={vi.fn()}
      onEnter={vi.fn()}
      onNavigate={navigate}
    />,
  );
  expect(screen.queryByRole("button", { name: "Открыть тело callback" })).not.toBeInTheDocument();
  expect(vi.mocked(fetch).mock.calls.some(([url]) => String(url).includes("/explore/query"))).toBe(
    false,
  );
  expect(navigate).not.toHaveBeenCalled();
});

it.each([true, false])(
  "shows file and lines while keeping native code collapsed (in projection=%s)",
  async (inProjection) => {
    workspaceHTTP((path) =>
      path.endsWith(`/revisions/${revisionId}/nodes/${nodeId}`)
        ? json(200, { ...step, attributes: { ...step.attributes, nativeText }, evidenceIds: [] })
        : path.endsWith(`/revisions/${revisionId}/evidence`)
          ? json(200, {
              items: [
                {
                  id: "proof",
                  subjectId: nodeId,
                  method: "ast",
                  status: "explicit",
                  source: {
                    repositoryId: projectId,
                    snapshotId: revisionId,
                    file: "internal/application/actions/autochat.go",
                    contentHash: "a".repeat(64),
                    startLine: 14,
                    endLine: 17,
                  },
                  explanation: "Initialize application kernel",
                },
              ],
              nextCursor: "",
            })
          : undefined,
    );
    renderWithProviders(
      <Inspector
        projectId={projectId}
        target={{ revisionId }}
        search={{ revisionId }}
        id={nodeId}
        node={{
          ...step,
          attributes: { ...step.attributes, ...(inProjection ? { nativeText } : {}) },
        }}
        onClose={vi.fn()}
        onEnter={vi.fn()}
        onNavigate={vi.fn()}
      />,
    );
    const source = await screen.findByRole("region", { name: "Исходный код" });
    expect(await screen.findByText("internal/application/actions/autochat.go:14–17")).toBeVisible();
    expect(source.querySelector("pre")).not.toBeVisible();
    await userEvent.click(screen.getByText("Показать код", { exact: true }));
    expect(source.querySelector("pre")).toBeVisible();
    expect(source.querySelector("code")?.textContent).toBe(nativeText);
    expect(source.querySelector("kernel")).toBeNull();
    expect(screen.queryByRole("tablist")).not.toBeInTheDocument();
    expect(screen.getAllByText(step.name)).toHaveLength(1);
  },
);

it.each([
  { nativeDefinition: nativeText },
  { facets: { sql: { definition: nativeText } } },
  { facets: { declared: { nativeDefinition: nativeText } } },
  { relational: { facets: { effective: { nativeDefinition: nativeText } } } },
  { databaseRoutine: { facets: { declared: { nativeDefinition: nativeText } } } },
])("reveals exact native definitions without a language parser: %j", async (attributes) => {
  workspaceHTTP();
  renderWithProviders(
    <Inspector
      projectId={projectId}
      target={{ revisionId }}
      search={{ revisionId }}
      id={nodeId}
      node={{ ...step, kind: "database_routine", attributes }}
      onClose={vi.fn()}
      onEnter={vi.fn()}
      onNavigate={vi.fn()}
    />,
  );
  const source = await screen.findByRole("region", { name: "Исходный код" });
  expect(source.querySelector("pre")).not.toBeVisible();
  await userEvent.click(screen.getByText("Показать код", { exact: true }));
  expect(source.querySelector("code")?.textContent).toBe(nativeText);
});
