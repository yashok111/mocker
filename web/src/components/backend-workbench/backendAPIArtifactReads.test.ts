// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";
import { json } from "@/test/http";
import {
  readAPIArtifacts,
  replaceArtifactBindings,
  exactArtifactId,
  safeLegacyId,
  strictAuthoredPointer,
} from "./backendAPIArtifactReads";
const hash = "a".repeat(64);
const scope = {
  projectId: "project",
  revisionId: "0197aaf9-5555-7000-8000-000000000118",
  semanticHash: hash,
  sourceSnapshotIds: ["snapshot"],
  artifactPins: [{ kind: "api_design" as const, id: "12", revisionId: "23", contentHash: hash }],
};
const binding = (sourceNodeId: string, artifactId = "12") => ({
  sourceNodeId,
  sourceKind: "api_field" as const,
  sourceLastKnownLabel: sourceNodeId,
  origin: "manual" as const,
  reason: "manual",
  ref: {
    kind: "api_design" as const,
    artifactId,
    revisionId: "23",
    contentHash: hash,
    objectHash: hash,
    lastKnownLabel: sourceNodeId,
    resolvedPointer: "/components/schemas/Flag",
    selector: { jsonPointer: "/components/schemas/Flag" },
  },
});
afterEach(() => vi.unstubAllGlobals());
it("collects every page before full group replacement and preserves other source nodes", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_url, init) => {
      const request = JSON.parse(init.body);
      return json(200, {
        ...scope,
        pins: scope.artifactPins,
        items: [
          {
            binding: binding(request.cursor ? "other" : "selected"),
            resolution: { status: "valid", diagnostics: [], updateAvailable: false },
          },
        ],
        nextCursor: request.cursor ? "" : "second",
      });
    }),
  );
  const all = await readAPIArtifacts(scope, undefined, new AbortController().signal);
  expect(all.items.map((i) => i.binding.sourceNodeId)).toEqual(["selected", "other"]);
  const command = replaceArtifactBindings(
    all.items,
    "12",
    "24",
    "selected",
    { jsonPointer: "/components/schemas/New" },
    "intent",
  );
  expect(command).toEqual({
    type: "set_api_pin",
    artifactId: "12",
    revisionId: "24",
    reason: "intent",
    bindings: [
      { sourceNodeId: "other", selector: { jsonPointer: "/components/schemas/Flag" } },
      { sourceNodeId: "selected", selector: { jsonPointer: "/components/schemas/New" } },
    ],
  });
  expect(replaceArtifactBindings(all.items, "12", "23", "selected", null, "remove")).toMatchObject({
    type: "set_api_pin",
    bindings: [{ sourceNodeId: "other" }],
  });
  expect(
    replaceArtifactBindings(
      [
        {
          binding: binding("selected"),
          resolution: { status: "orphaned", diagnostics: [], updateAvailable: false },
        },
      ],
      "12",
      "23",
      "selected",
      null,
      "remove",
    ),
  ).toMatchObject({ type: "remove_api_pin" });
});
it.each(["semanticHash", "sourceSnapshotIds", "pins", "revisionId"])(
  "rejects a mixed %s pagination context",
  async (field) => {
    let page = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        json(200, {
          ...scope,
          pins: scope.artifactPins,
          items: [],
          nextCursor: ++page === 1 ? "0197aaf9-5555-7000-8000-000000000108" : "",
          ...(page === 2
            ? {
                [field]:
                  field === "pins"
                    ? [{ kind: "api_design", id: "12", revisionId: "24", contentHash: hash }]
                    : field === "sourceSnapshotIds"
                      ? ["0197aaf9-5555-7000-8000-000000000106"]
                      : "wrong",
              }
            : {}),
        }),
      ),
    );
    await expect(readAPIArtifacts(scope, undefined, new AbortController().signal)).rejects.toThrow(
      /контекст/,
    );
  },
);
it("discards replies after cancellation and rejects cursor cycles", async () => {
  const controller = new AbortController();
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => {
      controller.abort();
      return json(200, { ...scope, pins: scope.artifactPins, items: [], nextCursor: "" });
    }),
  );
  await expect(readAPIArtifacts(scope, "absent-source", controller.signal)).rejects.toThrow();
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      json(200, { ...scope, pins: scope.artifactPins, items: [], nextCursor: "same" }),
    ),
  );
  await expect(readAPIArtifacts(scope, undefined, new AbortController().signal)).rejects.toThrow(
    /страниц/,
  );
});
it("keeps decimal int64 IDs exact and admits only strict authored pointers", () => {
  expect(exactArtifactId("9223372036854775807")).toBe(true);
  expect(exactArtifactId("9223372036854775808")).toBe(false);
  expect(exactArtifactId("01")).toBe(false);
  expect(safeLegacyId("9007199254740993")).toBeUndefined();
  expect(safeLegacyId(9007199254740992)).toBeUndefined();
  expect(safeLegacyId("23")).toBe(23);
  expect(strictAuthoredPointer("/properties/a~1b/%literal")).toBe(true);
  expect(strictAuthoredPointer("/properties/a~2b")).toBe(false);
  expect(strictAuthoredPointer("/".repeat(65))).toBe(false);
});
it("reads a disappeared source node binding using its exact node ID filter", async () => {
  let input: unknown;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_url, init) => {
      input = JSON.parse(init.body);
      return json(200, {
        revisionId: scope.revisionId,
        semanticHash: hash,
        sourceSnapshotIds: scope.sourceSnapshotIds,
        pins: scope.artifactPins,
        nextCursor: "",
        items: [
          {
            binding: binding("absent-source"),
            resolution: { status: "orphaned", diagnostics: [], updateAvailable: false },
          },
        ],
      });
    }),
  );
  const result = await readAPIArtifacts(scope, "absent-source", new AbortController().signal);
  expect(result.items[0]?.resolution.status).toBe("orphaned");
  expect(input).toEqual({
    revisionId: scope.revisionId,
    sourceNodeId: "absent-source",
    limit: 100,
    cursor: "",
  });
});
