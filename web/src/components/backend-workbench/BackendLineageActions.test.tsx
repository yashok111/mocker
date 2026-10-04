import { afterEach, expect, it, vi } from "vitest";
import { screen, fireEvent, waitFor } from "@testing-library/react";
import { renderWithProviders } from "@/test/render";
import { json } from "@/test/http";
import { BackendLineageActions, BackendValueSeeds } from "./BackendLineageActions";
import type { BackendNode } from "@/api/generated/schemas";
afterEach(() => vi.unstubAllGlobals());
it.each(["inputs", "outputs", "parameters", "results"] as const)(
  "seeds the exact %s port key and retains reverse launch direction",
  async (collection) => {
    const requests: unknown[] = [];
    vi.stubGlobal("fetch", async (_url: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === "POST") {
        const body = JSON.parse(String(init.body));
        requests.push(body);
        return json(422, {});
      }
      return json(200, { id: "0197aaf9-5555-7000-8000-000000000002", schemaVersion: "4" });
    });
    const select = vi.fn();
    renderWithProviders(
      <BackendValueSeeds
        projectId="project"
        revisionId="0197aaf9-5555-7000-8000-000000000002"
        node={
          {
            id: "owner",
            kind: collection === "inputs" || collection === "outputs" ? "flow_step" : "query",
            name: "owner",
            attributes: { [collection]: [{ key: "opaque-key", name: "Value" }] },
          } as unknown as BackendNode
        }
        onValueSelect={select}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: /Открыть точное значение/ }));
    expect(select).toHaveBeenCalledWith({
      kind: "port",
      nodeId: "owner",
      collection,
      portKey: "opaque-key",
    });
    const launch = await screen.findByRole("button", { name: "Происхождение значения" });
    await waitFor(() => expect(launch).toBeEnabled());
    fireEvent.click(launch);
    await waitFor(() =>
      expect(requests).toContainEqual({
        revisionId: "0197aaf9-5555-7000-8000-000000000002",
        seed: { kind: "port", nodeId: "owner", collection, portKey: "opaque-key" },
        direction: "reverse",
        maxDepth: 8,
        limit: 50,
        cursor: "",
      }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Закрыть происхождение значения" }));
    await waitFor(() => expect(launch).toHaveFocus());
  },
);
it("explains source3 unsupported without requesting lineage", async () => {
  const fetch = vi.fn(async (_url: RequestInfo | URL) =>
    json(200, { id: "0197aaf9-5555-7000-8000-000000000002", schemaVersion: "3" }),
  );
  vi.stubGlobal("fetch", fetch);
  renderWithProviders(
    <BackendValueSeeds
      projectId="project"
      revisionId="0197aaf9-5555-7000-8000-000000000002"
      node={
        {
          id: "field",
          kind: "api_field",
          name: "request",
          attributes: {},
        } as unknown as BackendNode
      }
      onValueSelect={() => {}}
    />,
  );
  await screen.findByText(/только в снимке source4/);
  expect(fetch.mock.calls.every((call) => !String(call[0]).includes("lineage"))).toBe(true);
});
it("offers independent exact column facet values", () => {
  const select = vi.fn();
  vi.stubGlobal("fetch", async () =>
    json(200, { id: "0197aaf9-5555-7000-8000-000000000002", schemaVersion: "4" }),
  );
  renderWithProviders(
    <BackendValueSeeds
      projectId="project"
      revisionId="0197aaf9-5555-7000-8000-000000000002"
      node={
        {
          id: "column",
          kind: "column",
          name: "price",
          attributes: { facets: { first: {}, second: {} } },
        } as unknown as BackendNode
      }
      onValueSelect={select}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "Открыть точное значение price · second" }));
  expect(select).toHaveBeenCalledWith({ kind: "column", nodeId: "column", facetKey: "second" });
});
it.each(["opposite direction", "internal direction", "boundary and paging"])(
  "resets the original query scope on every launch after %s",
  async (scenario) => {
    const seed = { kind: "api_field" as const, nodeId: "original" };
    const destination = { kind: "api_field" as const, nodeId: "boundary" };
    vi.stubGlobal("fetch", async (url: RequestInfo | URL, init?: RequestInit) => {
      if (String(url).endsWith("/revisions/0197aaf9-5555-7000-8000-000000000002"))
        return json(200, { id: "0197aaf9-5555-7000-8000-000000000002", schemaVersion: "4" });
      if (init?.method !== "POST") return json(200, { items: [], nextCursor: "" });
      const body = JSON.parse(String(init.body));
      return json(200, {
        projectId: "project",
        revisionId: "0197aaf9-5555-7000-8000-000000000002",
        seed: body.seed,
        direction: body.direction,
        semanticHash: "hash",
        policy: "field-lineage-traversal-v1",
        items: [
          {
            mapping: {
              id: "mapping",
              name: "Boundary mapping",
              kind: "field_mapping",
              evidenceIds: [],
              attributes: {
                sources: [seed],
                destination,
                transform: { kind: "unknown_transform", description: "Unknown", redacted: false },
                analysisStatus: "unsupported",
                gaps: [],
              },
            },
            via: body.seed,
            depth: 1,
            witnessMappingIds: ["mapping"],
            status: "unresolved",
            expansion: "boundary",
            expandedValues: [],
            reasons: ["unknown_transform"],
            requiresReview: true,
          },
        ],
        nextCursor: body.cursor ? "" : "next",
        coverage: { coverage: { status: "partial", knownObjects: 1, denominator: null } },
        truncated: false,
        truncationReasons: [],
        limitations: [],
        visitedValueCount: 1,
        examinedMappingCount: 1,
      });
    });
    const { BackendLineageActions } = await import("./BackendLineageActions");
    renderWithProviders(
      <BackendLineageActions
        projectId="project"
        revisionId="0197aaf9-5555-7000-8000-000000000002"
        seed={seed}
        onValueSelect={() => {}}
      />,
    );
    const reverse = await screen.findByRole("button", { name: "Происхождение значения" });
    await waitFor(() => expect(reverse).toBeEnabled());
    const forward = screen.getByRole("button", { name: "Куда передаётся значение" });
    fireEvent.click(scenario === "opposite direction" ? reverse : forward);
    await screen.findByText("Unknown");
    if (scenario === "internal direction")
      fireEvent.change(screen.getByLabelText("Направление происхождения"), {
        target: { value: "reverse" },
      });
    if (scenario === "boundary and paging") {
      fireEvent.change(screen.getByLabelText("Глубина происхождения"), { target: { value: "3" } });
      await screen.findByText("Unknown");
      fireEvent.click(
        screen.getByRole("button", { name: "Начать отдельный запрос boundary · поле API" }),
      );
      await screen.findByText(/Отдельный запрос за границей/);
      fireEvent.click(
        await screen.findByRole("button", { name: "Следующая страница происхождения" }),
      );
      await screen.findByText("Страница 2");
    }
    fireEvent.click(forward);
    await waitFor(() =>
      expect(screen.getByLabelText("Направление происхождения")).toHaveValue("forward"),
    );
    expect(screen.queryByText(/Отдельный запрос за границей/)).not.toBeInTheDocument();
    expect(await screen.findByText("Страница 1")).toBeVisible();
    expect(screen.getByLabelText("Глубина происхождения")).toHaveValue("8");
    expect(
      screen.getByText("Ревизия: 0197aaf9-5555-7000-8000-000000000002 · original · поле API"),
    ).toBeVisible();
  },
);

it("opens full proposal lineage without reading a source head", async () => {
  const target = {
    changeProposal: {
      proposalId: "0197aaf9-5555-7000-8000-000000000001",
      proposalRevisionId: "0197aaf9-5555-7000-8000-000000000003",
    },
  };
  const fetcher = vi.fn(async () => json(422, {}));
  vi.stubGlobal("fetch", fetcher);
  renderWithProviders(
    <BackendLineageActions
      projectId="project"
      target={target}
      seed={{ kind: "representation_field", nodeId: "field" }}
      onValueSelect={() => {}}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "Происхождение значения" }));
  await waitFor(() => expect(fetcher).toHaveBeenCalled());
  expect(fetcher.mock.calls).toHaveLength(1);
});
