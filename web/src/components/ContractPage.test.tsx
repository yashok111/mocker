// P7b B2–B5 on the component alone (B6, the links, is routes.test.tsx's,
// because it needs the real route tree): the marker in every state, the
// empty skeleton, the badges and their counts, the schema tree's collapsed
// `$ref` and a self-referencing schema, §14's word rule and the download.

import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ContractPage } from "./ContractPage";
import { renderWithProviders } from "@/test/render";
import { json, route } from "@/test/http";
import {
  endpointListViewFixture,
  endpointViewFixture,
  mergedOperationViewFixture,
  opOverrideSummaryViewFixture,
} from "@/test/fixtures";

const WS = 7;
const CONTRACT = `GET /api/workspaces/${WS}/openapi.json`;
const OPERATIONS = `GET /api/workspaces/${WS}/operations`;
const ENDPOINTS = `GET /api/workspaces/${WS}/endpoints`;

const skeleton = {
  openapi: "3.1.0",
  info: { title: "From Nothing", version: "0.0.0-draft.1" },
  paths: {},
};

/** A base with two operations, a component, and a self-referencing one. */
const designed = {
  openapi: "3.1.0",
  info: { title: "platform", version: "1.2.0-draft.9" },
  components: {
    schemas: {
      User: {
        type: "object",
        required: ["id"],
        properties: { id: { type: "integer" }, name: { type: "string" } },
      },
      Node: {
        type: "object",
        properties: {
          label: { type: "string" },
          children: { type: "array", items: { $ref: "#/components/schemas/Node" } },
        },
      },
    },
  },
  paths: {
    "/users": {
      get: {
        operationId: "listUsers",
        summary: "List users",
        responses: {
          "200": {
            description: "OK",
            content: {
              "application/json": {
                schema: { type: "array", items: { $ref: "#/components/schemas/User" } },
              },
            },
          },
        },
      },
    },
    "/users/{id}": {
      get: {
        operationId: "getUser",
        deprecated: true,
        responses: {
          "200": {
            description: "OK",
            content: { "application/json": { schema: { $ref: "#/components/schemas/User" } } },
          },
        },
      },
    },
    "/tree": {
      get: {
        operationId: "getTree",
        responses: {
          "200": {
            description: "OK",
            content: { "application/json": { schema: { $ref: "#/components/schemas/Node" } } },
          },
        },
      },
    },
    "/things": {
      post: {
        operationId: "makeThing",
        summary: "Make a thing",
        parameters: [{ name: "dryRun", in: "query", schema: { type: "boolean" } }],
        requestBody: {
          content: {
            "application/json": {
              schema: { type: "object", properties: { name: { type: "string" } } },
            },
          },
        },
        responses: {
          "201": {
            description: "Created",
            content: {
              "application/json": {
                schema: { type: "object", properties: { id: { type: "integer" } } },
                examples: [{ id: 1 }],
              },
            },
          },
        },
      },
    },
  },
};

function designedOperations() {
  return [
    mergedOperationViewFixture({
      method: "GET",
      path: "/users",
      opKey: "GET%20%2Fusers",
      override: opOverrideSummaryViewFixture({
        overrideOn: true,
        responses: { "200": { mode: "generated", recipeCount: 0, hasSchemaPatch: true } },
      }),
    }),
    mergedOperationViewFixture({
      method: "GET",
      path: "/users/{id}",
      opKey: "GET%20%2Fusers%2F%7Bid%7D",
      override: opOverrideSummaryViewFixture({ overrideOn: true, routeOff: true }),
    }),
    mergedOperationViewFixture({
      method: "GET",
      path: "/tree",
      opKey: "GET%20%2Ftree",
      override: undefined,
    }),
  ];
}

function designedEndpoints() {
  return endpointListViewFixture({
    endpoints: [endpointViewFixture({ id: 5, method: "POST", path: "/things" })],
  });
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("ContractPage", () => {
  it("renders its marker in every state and the empty skeleton without throwing (B2)", async () => {
    route({
      [CONTRACT]: () => json(200, skeleton),
      [OPERATIONS]: () => json(200, []),
      [ENDPOINTS]: () => json(200, endpointListViewFixture({ endpoints: [] })),
    });
    renderWithProviders(<ContractPage id={WS} />);
    expect(screen.getByTestId("contract-page")).toBeInTheDocument();
    expect(await screen.findByTestId("contract-empty")).toBeInTheDocument();
    expect(screen.getByTestId("contract-header")).toHaveTextContent("0.0.0-draft.1");
    expect(screen.getByTestId("contract-count-base")).toHaveTextContent("база: 0");
  });

  it("shows an error state with a retry when a read fails", async () => {
    route({
      [CONTRACT]: () => json(500, { error: { code: "internal", message: "boom" } }),
      [OPERATIONS]: () => json(200, []),
      [ENDPOINTS]: () => json(200, endpointListViewFixture({ endpoints: [] })),
    });
    renderWithProviders(<ContractPage id={WS} />);
    expect(await screen.findByTestId("contract-error")).toBeInTheDocument();
    expect(screen.getByTestId("contract-page")).toBeInTheDocument();
  });

  it("badges every operation and counts them in the header (B3)", async () => {
    route({
      [CONTRACT]: () => json(200, designed),
      [OPERATIONS]: () => json(200, designedOperations()),
      [ENDPOINTS]: () => json(200, designedEndpoints()),
    });
    renderWithProviders(<ContractPage id={WS} />);
    const ops = await screen.findAllByTestId("contract-op");
    const byKey = new Map(ops.map((el) => [el.textContent ?? "", el.getAttribute("data-badge")]));
    const kindOf = (fragment: string) =>
      [...byKey.entries()].find(([text]) => text.includes(fragment))?.[1];
    expect(kindOf("Make a thing")).toBe("added");
    expect(kindOf("List users")).toBe("changed");
    expect(kindOf("getUser")).toBe("removed");
    expect(kindOf("getTree")).toBe("base");
    expect(screen.getByTestId("contract-count-base")).toHaveTextContent("база: 1");
    expect(screen.getByTestId("contract-count-added")).toHaveTextContent("добавлено: 1");
    expect(screen.getByTestId("contract-count-changed")).toHaveTextContent("изменено: 1");
    expect(screen.getByTestId("contract-count-removed")).toHaveTextContent("удалено: 1");
    // The deprecated operation says so in the product's words.
    expect(screen.getByText("устарело")).toBeInTheDocument();
    // Base operations carry no editor link; the others do.
    expect(screen.getAllByTestId("contract-op-link")).toHaveLength(3);
  });

  it("keeps a $ref collapsed until clicked and survives a self-referencing schema (B4)", async () => {
    route({
      [CONTRACT]: () => json(200, designed),
      [OPERATIONS]: () => json(200, designedOperations()),
      [ENDPOINTS]: () => json(200, designedEndpoints()),
    });
    renderWithProviders(<ContractPage id={WS} />);
    const toggles = await screen.findAllByTestId("contract-op-toggle");
    const treeToggle = toggles.find((el) => el.textContent?.includes("getTree"));
    if (treeToggle === undefined) {
      throw new Error("no getTree row");
    }
    await userEvent.click(treeToggle);
    // Collapsed: the component NAME, not its properties.
    const ref = await screen.findByTestId("schema-ref-Node");
    expect(screen.queryByText("label")).toBeNull();
    await userEvent.click(ref);
    expect(await screen.findByText("label")).toBeInTheDocument();
    // The nested self-reference is again a collapsed name — one more click,
    // one more level, never a stack overflow.
    const inner = screen.getAllByTestId("schema-ref-Node");
    expect(inner.length).toBe(2);
    await userEvent.click(inner[1] as HTMLElement);
    expect(screen.getAllByTestId("schema-ref-Node").length).toBe(3);
  });

  it("names no wire word and downloads the fetched document once (B5)", async () => {
    route({
      [CONTRACT]: () => json(200, designed),
      [OPERATIONS]: () => json(200, designedOperations()),
      [ENDPOINTS]: () => json(200, designedEndpoints()),
    });
    const createObjectURL = vi.fn((_blob: Blob) => "blob:contract");
    const revokeObjectURL = vi.fn();
    vi.stubGlobal("URL", Object.assign(URL, { createObjectURL, revokeObjectURL }));
    const click = vi
      .spyOn(HTMLAnchorElement.prototype, "click")
      .mockImplementation(() => undefined);

    renderWithProviders(<ContractPage id={WS} />);
    const toggles = await screen.findAllByTestId("contract-op-toggle");
    for (const t of toggles) {
      await userEvent.click(t);
    }
    const text = (document.body.textContent ?? "").toLowerCase();
    for (const banned of ["patch", "recipe", "matcher", "schemapatch"]) {
      expect(text, banned).not.toContain(banned);
    }

    await userEvent.click(screen.getByTestId("contract-download"));
    await waitFor(() => expect(createObjectURL).toHaveBeenCalledTimes(1));
    const blob = createObjectURL.mock.calls[0]?.[0];
    if (blob === undefined) {
      throw new Error("createObjectURL was not given a blob");
    }
    expect(await blob.text()).toBe(JSON.stringify(designed, null, 2));
    expect(click).toHaveBeenCalledTimes(1);
    expect(revokeObjectURL).toHaveBeenCalledWith("blob:contract");
    // One fetch of the contract for the whole test: the download re-fetched nothing.
    const fetchMock = globalThis.fetch as unknown as { mock: { calls: unknown[][] } };
    const contractCalls = fetchMock.mock.calls.filter((c) =>
      String(c[0]).endsWith("/openapi.json"),
    );
    expect(contractCalls).toHaveLength(1);
  });
});
