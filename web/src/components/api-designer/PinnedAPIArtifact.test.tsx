import { afterEach, expect, it, vi } from "vitest";
import { useState } from "react";
import userEvent from "@testing-library/user-event";
import { screen, waitFor } from "@testing-library/react";
import { sha256 } from "@noble/hashes/sha2.js";
import { bytesToHex } from "@noble/hashes/utils.js";
import { renderWithProviders } from "@/test/render";
import { json } from "@/test/http";
import {
  PinnedAPIArtifact,
  readPinnedAPIArtifact,
  resolveRawConsumerPointer,
  pinnedBackendReturnHref,
} from "./PinnedAPIArtifact";
const raw =
  '{ "paths": {"/alias":{"$ref":"#/components/pathItems/Shared"}}, "components":{"pathItems":{"Shared":{"get":{"responses":{}}}},"schemas":{"Flag":false}} }';
const hash = bytesToHex(sha256(new TextEncoder().encode(raw)));
const pin = {
  pinnedRevisionId: "23",
  pinnedHash: hash,
  pinnedObjectKey: "legacy-owner-key",
  pinnedPointer: "/components/pathItems/Shared/get",
};
const hydrated = JSON.stringify({
  paths: {
    "/alias": {
      $ref: "#/components/pathItems/Shared",
      "x-mocker-canvas-operation-ids": { get: "legacy-owner-key" },
    },
  },
  components: { pathItems: { Shared: { get: { responses: {} } } } },
});
function server(overrides: Record<string, unknown> = {}) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url) =>
      String(url).endsWith("artifact-snapshot")
        ? json(200, {
            artifactId: "12",
            revisionId: "23",
            contentHash: hash,
            name: "Frozen",
            document: raw,
            ...overrides,
          })
        : json(200, { id: 23, designId: 12, hash, document: hydrated }),
    ),
  );
}
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
it("pairs raw hash truth with exact owner identity and highlights inherited authored source", async () => {
  server();
  renderWithProviders(
    <PinnedAPIArtifact
      artifactId="12"
      pin={pin}
      currentRevisionId="24"
      dirty
      onOpenCurrent={vi.fn()}
    />,
  );
  expect(await screen.findByText(/Потребитель: GET \/alias/)).toBeInTheDocument();
  expect(
    screen.getByText(/Текущий черновик: 24 · есть несохранённые изменения/),
  ).toBeInTheDocument();
  expect(screen.getByTestId("pinned-api-selection")).toHaveTextContent('"get":{"responses":{}}');
  expect(screen.getByTestId("pinned-api-raw").textContent).toBe(raw);
  expect(screen.getByRole("button", { name: "Открыть текущий черновик" })).toBeInTheDocument();
});
it("admits false schema positions and leaves authored refs unexpanded", async () => {
  server();
  const data = await readPinnedAPIArtifact(
    "12",
    { pinnedRevisionId: "23", pinnedHash: hash, pinnedSelectorPointer: "/components/schemas/Flag" },
    new AbortController().signal,
  );
  expect(data.pointer).toBe("/components/schemas/Flag");
  expect(data.selection).toBe(false);
  expect(resolveRawConsumerPointer(JSON.parse(raw), "/alias", "get")).toBe(
    "/components/pathItems/Shared/get",
  );
  expect(() =>
    resolveRawConsumerPointer(
      { paths: { "/cycle": { $ref: "#/paths/~1cycle" } } },
      "/cycle",
      "get",
    ),
  ).toThrow(/цикл/);
  expect(() =>
    resolveRawConsumerPointer(
      { paths: { "/external": { $ref: "https://example.org/api" } } },
      "/external",
      "get",
    ),
  ).toThrow(/внешн/);
});
it.each([{ contentHash: "b".repeat(64) }, { revisionId: "24" }, { document: "{}" }])(
  "rejects snapshot identity/hash mismatch %j",
  async (overrides) => {
    server(overrides);
    await expect(readPinnedAPIArtifact("12", pin, new AbortController().signal)).rejects.toThrow(
      /сним/,
    );
  },
);
it("keeps wide IDs exact for raw calls without unsafe owner numeric reads", async () => {
  const wide = "9007199254740993";
  const fetch = vi.fn(async (_url: RequestInfo | URL) =>
    json(200, {
      artifactId: wide,
      revisionId: wide,
      contentHash: hash,
      name: "Wide",
      document: raw,
    }),
  );
  vi.stubGlobal("fetch", fetch);
  const data = await readPinnedAPIArtifact(
    wide,
    { pinnedRevisionId: wide, pinnedHash: hash, pinnedSelectorPointer: "/components/schemas/Flag" },
    new AbortController().signal,
  );
  expect(data.selection).toBe(false);
  expect(fetch.mock.calls).toHaveLength(1);
  expect(fetch.mock.calls[0]?.[0]).toBe(`/api/designs/${wide}/revisions/${wide}/artifact-snapshot`);
});
it("discards late old raw snapshots on selection changes", async () => {
  let resolve: ((response: Response) => void) | undefined;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url) =>
      String(url).includes("/23/")
        ? new Promise<Response>((done) => {
            resolve = done;
          })
        : json(200, {
            artifactId: "12",
            revisionId: "24",
            contentHash: hash,
            name: "New pin",
            document: raw,
          }),
    ),
  );
  function Host() {
    const [revisionId, setRevisionId] = useState("23");
    return (
      <>
        <button onClick={() => setRevisionId("24")}>Change pin</button>
        <PinnedAPIArtifact
          artifactId="12"
          pin={{
            pinnedRevisionId: revisionId,
            pinnedHash: hash,
            pinnedSelectorPointer: "/components/schemas/Flag",
          }}
          currentRevisionId="25"
          dirty={false}
          onOpenCurrent={vi.fn()}
        />
      </>
    );
  }
  renderWithProviders(<Host />);
  await waitFor(() => expect(resolve).toBeDefined());
  await userEvent.click(screen.getByRole("button", { name: "Change pin" }));
  expect(await screen.findByText(/New pin/)).toBeInTheDocument();
  resolve?.(
    json(200, {
      artifactId: "12",
      revisionId: "23",
      contentHash: hash,
      name: "Obsolete",
      document: raw,
    }),
  );
  expect(screen.queryByText(/Obsolete/)).not.toBeInTheDocument();
});
it("validates backend return pins and rejects arbitrary external context", () => {
  expect(
    pinnedBackendReturnHref({ returnProjectId: "https://example.org", returnRevisionId: "bad" }),
  ).toBeUndefined();
  const id = "0197aaf9-5555-7000-8000-000000000001";
  expect(
    pinnedBackendReturnHref({ returnProjectId: id, returnRevisionId: id, returnSourceNodeId: id }),
  ).toBe(`/backend-projects/${id}?revisionId=${id}&recordId=${id}&recordType=node`);
});
it("highlights exact authored numeric bytes without rounding selected API content", async () => {
  const exact = '{"components":{"schemas":{"Count":{"const":9007199254740993}}}}';
  const contentHash = bytesToHex(sha256(new TextEncoder().encode(exact)));
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      json(200, {
        artifactId: "12",
        revisionId: "23",
        contentHash,
        name: "Exact count",
        document: exact,
      }),
    ),
  );
  renderWithProviders(
    <PinnedAPIArtifact
      artifactId="12"
      pin={{
        pinnedRevisionId: "23",
        pinnedHash: contentHash,
        pinnedSelectorPointer: "/components/schemas/Count/const",
      }}
      currentRevisionId="24"
      dirty={false}
      onOpenCurrent={vi.fn()}
    />,
  );
  expect(await screen.findByTestId("pinned-api-selection")).toHaveTextContent("9007199254740993");
  expect(screen.getByTestId("pinned-api-raw").textContent).toBe(exact);
});
it("shows the exact target snapshot when the requested proposed operation selector is missing", async () => {
  server();
  renderWithProviders(
    <PinnedAPIArtifact
      artifactId="12"
      pin={{ pinnedRevisionId: "23", pinnedHash: hash, pinnedObjectKey: "deleted-key" }}
      currentRevisionId="24"
      dirty
      onOpenCurrent={vi.fn()}
    />,
  );
  expect(await screen.findByTestId("pinned-api-raw")).toHaveTextContent(raw);
  expect(screen.getByText(/object key отсутствует в целевом снимке/)).toBeInTheDocument();
  expect(screen.queryByTestId("pinned-api-selection")).not.toBeInTheDocument();
});
it("rejects a mismatching historical owner revision instead of using the current draft identity", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url) =>
      String(url).endsWith("artifact-snapshot")
        ? json(200, {
            artifactId: "12",
            revisionId: "23",
            contentHash: hash,
            name: "Frozen",
            document: raw,
          })
        : json(200, { id: 24, designId: 12, hash, document: hydrated }),
    ),
  );
  await expect(readPinnedAPIArtifact("12", pin, new AbortController().signal)).rejects.toThrow(
    /идентичность владельца/,
  );
});
