import { useState } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { screen, fireEvent } from "@testing-library/react";
import { renderWithProviders } from "@/test/render";
import { json } from "@/test/http";
import { BackendLineage } from "./BackendLineage";
afterEach(() => vi.unstubAllGlobals());
const seed = { kind: "api_field" as const, nodeId: "request" };
const target = {
  kind: "port" as const,
  nodeId: "query",
  collection: "parameters" as const,
  portKey: "tax",
};
const page = {
  projectId: "project",
  revisionId: "rev",
  seed,
  direction: "forward",
  semanticHash: "hash",
  policy: "field-lineage-traversal-v1",
  items: [
    {
      mapping: {
        id: "mapping",
        name: "Aggregate",
        kind: "field_mapping",
        evidenceIds: [],
        attributes: {
          sources: [seed, { kind: "column", nodeId: "price", facetKey: "db" }],
          destination: target,
          transform: {
            kind: "unknown_transform",
            description: "unknown calculation",
            redacted: true,
          },
          analysisStatus: "unsupported",
          gaps: ["unknown"],
        },
      },
      via: seed,
      depth: 1,
      witnessMappingIds: ["mapping"],
      status: "unresolved",
      expansion: "boundary",
      expandedValues: [],
      reasons: ["unknown_transform"],
      requiresReview: true,
    },
  ],
  nextCursor: "",
  coverage: { coverage: { status: "partial", knownObjects: 4, denominator: null } },
  truncated: true,
  truncationReasons: ["depth_limit"],
  limitations: ["Static evidence only"],
  visitedValueCount: 1,
  examinedMappingCount: 1,
};
it("shows complete ordered co-inputs, boundary destination, transform and explicit separate-query action", async () => {
  vi.stubGlobal("fetch", async (_url: RequestInfo | URL, init?: RequestInit) =>
    json(200, init?.method === "POST" ? page : { items: [], nextCursor: "" }),
  );
  const select = vi.fn();
  renderWithProviders(
    <BackendLineage
      projectId="project"
      revisionId="rev"
      seed={seed}
      onClose={() => {}}
      onValueSelect={select}
    />,
  );
  await screen.findByText("unknown calculation");
  expect(
    screen.getByRole("button", { name: "Открыть значение price · колонка · db" }),
  ).toBeVisible();
  expect(screen.getAllByText(/unknown_transform/)[0]).toBeVisible();
  fireEvent.click(
    screen.getByRole("button", { name: "Открыть значение query · parameters · tax" }),
  );
  expect(select).toHaveBeenCalledWith(target);
  expect(
    screen.getByRole("button", { name: "Начать отдельный запрос query · parameters · tax" }),
  ).toBeVisible();
  expect(screen.getByText(/depth_limit/)).toBeVisible();
});
it.each(["seed", "revision"])(
  "does not display a previous %s after an A to B response race",
  async (change) => {
    let finish!: (response: Response) => void;
    vi.stubGlobal("fetch", (_url: unknown, init: RequestInit) => {
      const body = JSON.parse(String(init.body));
      return body.seed.nodeId === "request" && body.revisionId === "rev"
        ? new Promise<Response>((resolve) => {
            finish = resolve;
          })
        : Promise.resolve(
            json(200, { ...page, revisionId: body.revisionId, seed: body.seed, items: [] }),
          );
    });
    function Harness() {
      const [next, setNext] = useState(seed);
      const [revision, setRevision] = useState("rev");
      return (
        <>
          <button
            onClick={() =>
              change === "seed" ? setNext({ ...seed, nodeId: "B" }) : setRevision("B")
            }
          >
            Switch seed
          </button>
          <BackendLineage
            projectId="project"
            revisionId={revision}
            seed={next}
            onClose={() => {}}
            onValueSelect={() => {}}
          />
        </>
      );
    }
    renderWithProviders(<Harness />);
    fireEvent.click(screen.getByRole("button", { name: "Switch seed" }));
    await screen.findByText(/Нет импортированных/);
    finish(json(200, page));
    expect(screen.queryByText("unknown calculation")).not.toBeInTheDocument();
  },
);
it("binds paging, direction, depth and separate boundary requests to the immutable revision", async () => {
  const requests: Record<string, unknown>[] = [];
  vi.stubGlobal("fetch", async (_url: RequestInfo | URL, init?: RequestInit) => {
    if (init?.method !== "POST") return json(200, { items: [], nextCursor: "" });
    const body = JSON.parse(String(init.body));
    requests.push(body);
    return json(200, {
      ...page,
      seed: body.seed,
      direction: body.direction,
      nextCursor: body.cursor ? "" : "page2",
    });
  });
  renderWithProviders(
    <BackendLineage
      projectId="project"
      revisionId="rev"
      seed={seed}
      onClose={() => {}}
      onValueSelect={() => {}}
    />,
  );
  await screen.findByText("unknown calculation");
  fireEvent.click(screen.getByRole("button", { name: "Следующая страница происхождения" }));
  await vi.waitFor(() => expect(requests.at(-1)?.cursor).toBe("page2"));
  fireEvent.change(screen.getByLabelText("Направление происхождения"), {
    target: { value: "reverse" },
  });
  await vi.waitFor(() =>
    expect(requests.at(-1)).toMatchObject({ revisionId: "rev", direction: "reverse", cursor: "" }),
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "Начать отдельный запрос query · parameters · tax" }),
  );
  await screen.findByText(/Отдельный запрос за границей/);
  await vi.waitFor(() =>
    expect(requests.at(-1)).toMatchObject({ revisionId: "rev", seed: target, cursor: "" }),
  );
});
it("shows zero-source unknown inputs explicitly without labeling them as a constant", async () => {
  vi.stubGlobal("fetch", async (_url: RequestInfo | URL, init?: RequestInit) =>
    json(
      200,
      init?.method === "POST"
        ? {
            ...page,
            items: [
              {
                ...page.items[0],
                mapping: {
                  ...page.items[0]!.mapping,
                  attributes: { ...page.items[0]!.mapping.attributes, sources: [] },
                },
              },
            ],
          }
        : { items: [], nextCursor: "" },
    ),
  );
  renderWithProviders(
    <BackendLineage
      projectId="project"
      revisionId="rev"
      seed={seed}
      onClose={() => {}}
      onValueSelect={() => {}}
    />,
  );
  await screen.findByText("unknown calculation");
  expect(screen.queryByText("Константа: входов нет")).not.toBeInTheDocument();
  expect(screen.getByText("Входы не установлены")).toBeVisible();
});
