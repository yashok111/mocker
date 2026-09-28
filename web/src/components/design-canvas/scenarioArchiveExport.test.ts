import { createHash } from "node:crypto";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { unzipSync, strFromU8 } from "fflate";
import { exampleCanvas } from "./canvasModel";
import type { DesignScenarioRevision } from "./designScenarioApi";
import { loadScenarioArtifact } from "./scenarioExportApi";
import { exportSequenceImage } from "./sequenceImageExport";
import { buildScenarioArchive } from "./scenarioArchiveExport";

vi.mock("./scenarioExportApi", () => ({ loadScenarioArtifact: vi.fn() }));
vi.mock("./sequenceImageExport", () => ({ exportSequenceImage: vi.fn() }));
afterEach(() => vi.unstubAllGlobals());
const revision: DesignScenarioRevision = {
  id: 11,
  scenarioId: 7,
  version: 2,
  hash: "saved-hash",
  source: "ui",
  summary: "",
  createdAt: 1000,
  document: exampleCanvas(),
  formDrafts: {},
};
const warning = { code: "pending", severity: "warning" as const, message: "Сохранённый контракт" };
const content = '# Документ 界\n{"id":9007199254740993}\n';
beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(loadScenarioArtifact).mockResolvedValue({
    scenarioId: 7,
    revisionId: 11,
    sourceHash: "saved-hash",
    format: "markdown",
    filename: "scenario-7-r11.md",
    mediaType: "text/markdown;charset=utf-8",
    content,
    diagnostics: [warning],
  });
  vi.mocked(exportSequenceImage).mockResolvedValue(
    new Blob(["<svg>точные bytes</svg>"], { type: "image/svg+xml;charset=utf-8" }),
  );
});

it("packages exact saved bytes and images with version, hashes and warnings", async () => {
  const result = await buildScenarioArchive(revision, [{ format: "markdown" }, { format: "svg" }]);
  expect(result.type).toBe("application/zip");
  const files = unzipSync(new Uint8Array(await result.arrayBuffer()));
  expect(Object.keys(files)).toEqual(["scenario-7-r11.md", "scenario-7-r11.svg", "manifest.json"]);
  expect(strFromU8(files["scenario-7-r11.md"]!)).toBe(content);
  const manifest = JSON.parse(strFromU8(files["manifest.json"]!));
  expect(manifest).toMatchObject({
    schemaVersion: 1,
    kind: "mocker-scenario-artifacts",
    restorable: false,
    scenario: {
      id: 7,
      revisionId: 11,
      version: 2,
      sourceHash: "saved-hash",
      title: revision.document.title,
    },
  });
  for (const entry of manifest.files) {
    expect(entry.bytes).toBe(files[entry.path]!.byteLength);
    expect(entry.sha256).toBe(createHash("sha256").update(files[entry.path]!).digest("hex"));
  }
  expect(manifest.files[0].diagnostics).toEqual([warning]);
  expect(exportSequenceImage).toHaveBeenCalledWith(revision.document, "svg");
  expect(loadScenarioArtifact).toHaveBeenCalledWith(7, 11, "markdown", undefined);
  const again = await buildScenarioArchive(revision, [{ format: "markdown" }, { format: "svg" }]);
  expect(new Uint8Array(await again.arrayBuffer())).toEqual(
    new Uint8Array(await result.arrayBuffer()),
  );
});

it("adds a pinned AsyncAPI contract with the server filename and exact bytes", async () => {
  const eventRevision: DesignScenarioRevision = {
    ...revision,
    document: {
      ...revision.document,
      formatVersion: 3,
      eventModel: {
        servers: [],
        channels: [],
        messages: [],
        schemas: [],
        contracts: [
          {
            id: "orders-events",
            name: "Orders events",
            description: "",
            participantId: "orders",
            version: "1.0.0",
            operations: [],
          },
        ],
      },
    },
  };
  const source = '{"x-id":9007199254740993123456789}\n';
  vi.mocked(loadScenarioArtifact).mockResolvedValueOnce({
    scenarioId: 7,
    revisionId: 11,
    sourceHash: "saved-hash",
    format: "asyncapi-json",
    filename: "scenario-7-r11.asyncapi-1.json",
    mediaType: "application/json",
    content: source,
    diagnostics: [],
  });
  const blob = await buildScenarioArchive(eventRevision, [
    { format: "asyncapi-json", contractId: "orders-events" },
  ]);
  const files = unzipSync(new Uint8Array(await blob.arrayBuffer()));
  expect(strFromU8(files["scenario-7-r11.asyncapi-1.json"]!)).toBe(source);
  expect(JSON.parse(strFromU8(files["manifest.json"]!)).files[0]).toMatchObject({
    format: "asyncapi-json",
    contractId: "orders-events",
  });
});

it("creates SHA-256 manifests on plain HTTP where WebCrypto is unavailable", async () => {
  vi.stubGlobal("crypto", {});
  const blob = await buildScenarioArchive(revision, [{ format: "markdown" }]);
  const files = unzipSync(new Uint8Array(await blob.arrayBuffer()));
  const manifest = JSON.parse(strFromU8(files["manifest.json"]!));
  expect(manifest.files[0].sha256).toBe(
    createHash("sha256").update(files["scenario-7-r11.md"]!).digest("hex"),
  );
});

it("validates selection before fetching and rejects unsafe filenames or mixed revisions", async () => {
  await expect(buildScenarioArchive(revision, [])).rejects.toThrow("Выберите");
  await expect(
    buildScenarioArchive(revision, [{ format: "markdown" }, { format: "markdown" }]),
  ).rejects.toThrow("повтор");
  await expect(
    buildScenarioArchive(
      revision,
      Array.from({ length: 33 }, () => ({ format: "svg" as const })),
    ),
  ).rejects.toThrow("32");
  await expect(
    buildScenarioArchive(revision, [{ format: "markdown", contractId: "api" }]),
  ).rejects.toThrow("контракт");
  expect(loadScenarioArtifact).not.toHaveBeenCalled();
  const original = await loadScenarioArtifact(7, 11, "markdown");
  for (const patch of [
    { revisionId: 12 },
    { sourceHash: "other" },
    { scenarioId: 8 },
    { filename: "../unsafe.md" },
    { format: "html" as const },
  ]) {
    vi.mocked(loadScenarioArtifact).mockResolvedValue({ ...original, ...patch });
    await expect(buildScenarioArchive(revision, [{ format: "markdown" }])).rejects.toThrow();
  }
});

it("fails atomically on errors, cancellation and aggregate size including manifest", async () => {
  vi.mocked(exportSequenceImage).mockRejectedValueOnce(new Error("Image failed"));
  await expect(
    buildScenarioArchive(revision, [{ format: "markdown" }, { format: "svg" }]),
  ).rejects.toThrow("Image failed");
  let cancelled = false;
  const artifact = await loadScenarioArtifact(7, 11, "markdown");
  vi.mocked(loadScenarioArtifact).mockImplementationOnce(async () => {
    cancelled = true;
    return artifact;
  });
  await expect(
    buildScenarioArchive(revision, [{ format: "markdown" }, { format: "svg" }], {
      isCancelled: () => cancelled,
    }),
  ).rejects.toThrow("отменена");
  expect(exportSequenceImage).toHaveBeenCalledTimes(1);
  await expect(
    buildScenarioArchive(revision, [{ format: "markdown" }], { maxBytes: 10 }),
  ).rejects.toThrow("размер");
  await expect(
    buildScenarioArchive(revision, [{ format: "markdown" }], { maxBytes: 200 }),
  ).rejects.toThrow("размер");
});

it("uses numeric contract filenames and keeps the selected contract identity in the manifest", async () => {
  const contractId = "../контракт/unsafe";
  const saved = {
    ...revision,
    document: {
      ...revision.document,
      contracts: [{ ...revision.document.contracts[0]!, id: contractId }],
    },
  };
  vi.mocked(loadScenarioArtifact).mockResolvedValue({
    scenarioId: 7,
    revisionId: 11,
    sourceHash: "saved-hash",
    format: "openapi-json",
    filename: "scenario-7-r11.api-1.json",
    mediaType: "application/json",
    content: '{"n":9007199254740993}',
    diagnostics: [],
  });
  const blob = await buildScenarioArchive(saved, [{ format: "openapi-json", contractId }]);
  const files = unzipSync(new Uint8Array(await blob.arrayBuffer()));
  expect(Object.keys(files)).toEqual(["scenario-7-r11.api-1.json", "manifest.json"]);
  const manifest = JSON.parse(strFromU8(files["manifest.json"]!));
  expect(manifest.files[0].contractId).toBe(contractId);
  expect(strFromU8(files["scenario-7-r11.api-1.json"]!)).toBe('{"n":9007199254740993}');
});
