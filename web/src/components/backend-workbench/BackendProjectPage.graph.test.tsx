import { afterEach, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { BackendProjectPage } from "./BackendProjectPage";
import { renderInRouter } from "@/test/render";
import { json, route } from "@/test/http";

afterEach(() => vi.unstubAllGlobals());

it("presents the imported revision inventory instead of pre-import guidance", async () => {
  const id = "0197aaf9-5555-7000-8000-000000000001";
  const rid = "0197aaf9-5555-7000-8000-000000000002";
  const newerId = "0197aaf9-5555-7000-8000-000000000007";
  const coverage = {
    status: "partial",
    denominator: null,
    knownObjects: 1,
    gaps: ["Analysis is partial"],
  };
  route({
    [`GET /api/backend-projects/${id}`]: () =>
      json(200, {
        id,
        name: "Импортированный проект",
        version: 2,
        currentRevisionId: rid,
        repositories: [],
        capabilities: [],
        createdAt: "2026-09-30T10:00:00Z",
        updatedAt: "2026-09-30T10:00:00Z",
      }),
    [`GET /api/backend-projects/${id}/revisions`]: () =>
      json(200, {
        items: [
          { id: rid, createdAt: "2026-09-30T10:00:00Z" },
          { id: newerId, createdAt: "2026-09-30T11:00:00Z" },
        ],
        nextCursor: "",
      }),
    [`GET /api/backend-projects/${id}/revisions/${newerId}`]: () =>
      new Response(new ReadableStream(), { headers: { "Content-Type": "application/json" } }),
    [`GET /api/backend-projects/${id}/revisions/${rid}`]: () =>
      json(200, {
        id: rid,
        projectId: id,
        parentRevisionId: null,
        schemaVersion: "1",
        semanticHash: "a".repeat(64),
        sourceSnapshotIds: ["0197aaf9-5555-7000-8000-000000000003"],
        artifactPins: [],
        coverage,
        author: "agent",
        summary: "First import",
        createdAt: "2026-09-30T10:00:00Z",
      }),
    [`GET /api/backend-projects/${id}/revisions/${rid}/coverage`]: () =>
      json(200, { coverage, inventory: [], snapshots: [] }),
    [`POST /api/backend-projects/${id}/graph/query`]: () =>
      json(200, {
        nodes: [
          {
            id: "0197aaf9-5555-7000-8000-000000000004",
            externalKey: "service",
            kind: "service",
            name: "Orders service",
            parentId: null,
            attributes: {},
            evidenceIds: [],
          },
        ],
        edges: [],
        nextCursor: "",
      }),
  });
  renderInRouter(<BackendProjectPage projectId={id} />);
  expect(
    await screen.findByRole("button", { name: "Открыть объект Orders service" }),
  ).toBeInTheDocument();
  expect(screen.queryByText(/Импорт исходников пока недоступен/)).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Сравнение ревизий" })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Проверить импорты" })).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Сравнение ревизий" }));
  expect(screen.getByRole("textbox", { name: "Ревизия до" })).toHaveValue(rid);
  await userEvent.click(screen.getByRole("button", { name: "История ревизий" }));
  await userEvent.click(await screen.findByRole("button", { name: `Открыть ревизию ${newerId}` }));
  expect(screen.getByRole("textbox", { name: "Ревизия до" })).toHaveValue(rid);
});
