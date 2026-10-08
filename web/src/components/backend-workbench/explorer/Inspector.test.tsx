import { afterEach, expect, it, vi } from "vitest";
import { cleanup, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { json } from "@/test/http";
import { Inspector } from "./Inspector";
import { projectId, revisionId, nodeId, workspaceHTTP } from "./testFixtures";
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
    expect(screen.getByRole("tab", { name: "Связи" })).toHaveAttribute("aria-selected", "true");
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
