import { expect, it, vi, afterEach } from "vitest";
import { json } from "@/test/http";
import { readArtifactPage, completeArtifactSet, readPinnedScenario } from "./backendArtifactReads";
const hash = "a".repeat(64);
const pin = { kind: "design_scenario", id: "12", revisionId: "23", contentHash: hash };
const scope = {
  projectId: "project",
  revisionId: "base",
  semanticHash: hash,
  sourceSnapshotIds: ["snap"],
  artifactPins: [pin],
};
const binding = {
  artifactKind: "design_scenario" as const,
  artifactId: "12",
  selector: { kind: "participant" as const, participantId: "p" },
  sourceNodeIds: ["same"],
  sourceLabels: ["Frozen source"],
  objectHash: hash,
  lastKnownLabel: "Deleted participant",
  origin: "manual" as const,
  reason: "old",
};
const page = {
  revisionId: "base",
  semanticHash: hash,
  sourceSnapshotIds: ["snap"],
  pins: [pin],
  selectedPin: pin,
  hashPolicy: "design-scenario-envelope-v1" as const,
  view: "sequence" as const,
  apiBindings: [],
  editorBindings: [binding],
  bindingsComplete: true as const,
  items: [],
  nextCursor: "next",
  resolution: { status: "broken", diagnostics: [], updateAvailable: false },
  diagnostics: [],
  coverage: {
    itemsReturned: 0,
    totalItems: 0,
    nodesReturned: 0,
    edgesReturned: 0,
    diagnosticsReturned: 0,
    truncatedReasons: [],
  },
  complete: false,
};
const input = {
  revisionId: "base",
  artifact: { kind: "design_scenario", id: "12" },
  view: "sequence",
} as const;
afterEach(() => vi.unstubAllGlobals());
it("retains full orphan roster even on empty projected pages and strips server fields", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(() => Promise.resolve(json(200, page))),
  );
  const result = await readArtifactPage(scope, input, new AbortController().signal);
  const command = completeArtifactSet(result, "23", "intent");
  expect(command.editorBindings).toEqual([{ selector: binding.selector, sourceNodeIds: ["same"] }]);
  expect(command).not.toHaveProperty("apiBindings");
});
it.each(["revisionId", "semanticHash", "pins", "selectedPin", "view", "bindingsComplete"])(
  "rejects mismatched %s",
  async (field) => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => Promise.resolve(json(200, { ...page, [field]: false }))),
    );
    await expect(readArtifactPage(scope, input, new AbortController().signal)).rejects.toThrow();
  },
);
it("refuses changing full roster between pages", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(() => Promise.resolve(json(200, { ...page, editorBindings: [] }))),
  );
  await expect(
    readArtifactPage(scope, { ...input, cursor: "next" }, new AbortController().signal, page),
  ).rejects.toThrow();
});
it("supports all four exact views and checks explicit embedded scope", async () => {
  for (const view of ["sequence", "states", "response_rules", "event_model"] as const) {
    const request =
      view === "states" || view === "response_rules"
        ? { ...input, view, embeddedContractId: "copy" }
        : { ...input, view };
    vi.stubGlobal(
      "fetch",
      vi.fn(() =>
        Promise.resolve(
          json(200, {
            ...page,
            view,
            ...("embeddedContractId" in request
              ? { embeddedContractId: request.embeddedContractId }
              : {}),
          }),
        ),
      ),
    );
    expect((await readArtifactPage(scope, request, new AbortController().signal)).view).toBe(view);
  }
});
it("refuses unsupported snapshot that pretends its stored hash is checked", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(() =>
      Promise.resolve(
        json(200, {
          scenarioId: "12",
          revisionId: "23",
          version: "1",
          storedContentHash: hash,
          documentHash: hash,
          documentJSON: "{}",
          formDraftsJSON: "null",
          hashPolicy: "design-scenario-envelope-v1",
          typedStatus: "unsupported",
          envelopeVerification: "unavailable",
          contentHash: hash,
        }),
      ),
    ),
  );
  await expect(
    readPinnedScenario("12", "23", hash, new AbortController().signal),
  ).rejects.toThrow();
});
it("last API unlink is a generic empty API set retaining models", () => {
  const apiPage = {
    ...page,
    selectedPin: { ...pin, kind: "api_design" },
    editorBindings: [
      {
        ...binding,
        artifactKind: "api_design" as const,
        selector: { kind: "state_diagram" as const, diagramId: "d" },
      },
    ],
  } as import("@/api/generated/schemas").ArtifactProjectionPage;
  const command = completeArtifactSet(apiPage, "23", "unlink", undefined, []);
  expect(command).toEqual({
    type: "set_artifact_pin",
    artifact: { kind: "api_design", id: "12" },
    revisionId: "23",
    editorBindings: [
      { selector: { kind: "state_diagram", diagramId: "d" }, sourceNodeIds: ["same"] },
    ],
    apiBindings: [],
    reason: "unlink",
  });
});
it.each(["copy", "linked"])("preserves exact %s origin version boundary", async (mode) => {
  const item = {
    id: "p",
    kind: "participant",
    label: "p",
    locator: {
      pin,
      view: "sequence",
      owner: { pointer: "/participants/0" },
      embedded: {
        contractId: "copy",
        mode,
        documentHash: hash,
        origin: { designId: "9007199254740993", revisionId: "9007199254740995", version: "0" },
        originStatus: mode,
      },
    },
    objectHash: hash,
    sourceNodeIds: [],
    data: {
      kind: "participant",
      participant: { id: "p", name: "p", kind: "service", description: "" },
    },
    diagnostics: [],
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(() => Promise.resolve(json(200, { ...page, items: [item] }))),
  );
  const read = readArtifactPage(scope, input, new AbortController().signal);
  if (mode === "copy") expect((await read).items[0]?.locator.embedded?.origin?.version).toBe("0");
  else await expect(read).rejects.toThrow();
});
it("aborts a late page before exposing its stale context", async () => {
  let resolve!: (r: Response) => void;
  vi.stubGlobal(
    "fetch",
    vi.fn(() => new Promise<Response>((r) => (resolve = r))),
  );
  const controller = new AbortController();
  const read = readArtifactPage(scope, input, controller.signal);
  controller.abort();
  resolve(json(200, page));
  await expect(read).rejects.toThrow();
});
