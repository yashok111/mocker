// @vitest-environment node
import { describe, expect, it } from "vitest";
import { emptyCanvas } from "./canvasModel";
import { parseMockerTransfer, serializeMockerScenario } from "./scenarioMockerFile";
const rev = (title: string) => ({
  document: { ...emptyCanvas(), title },
  formDrafts: {},
  summary: title,
  source: "ui",
  createdAt: 100,
});
const bundle = () => ({
  kind: "mocker.scenarios",
  formatVersion: 1,
  scenarios: [{ revisions: [rev("First"), rev("Second")] }, { revisions: [rev("Other")] }],
});
describe("scenario transfer package", () => {
  it.each(["я".repeat(3000), "x".repeat(6000), "🙂".repeat(6000)])(
    "preserves existing long revision summaries",
    (summary) => {
      const value = bundle();
      value.scenarios[0]!.revisions[0]!.summary = summary;
      expect(parseMockerTransfer(JSON.stringify(value)).bundle).toEqual(value);
    },
  );
  it("still enforces total size for long summaries", () => {
    const value = bundle();
    value.scenarios[0]!.revisions[0]!.summary = "я".repeat(1_000_000);
    expect(() => parseMockerTransfer(JSON.stringify(value))).toThrow(/2 МБ/);
  });
  it("preserves all scenarios and chronological history", () => {
    expect(parseMockerTransfer(JSON.stringify(bundle())).bundle).toEqual(bundle());
  });
  it("accepts the original single-scenario format", () => {
    const result = parseMockerTransfer(serializeMockerScenario(emptyCanvas(), {}));
    expect(result.single).toBe(true);
    expect(result.bundle.scenarios[0]!.revisions[0]!.document).toEqual(emptyCanvas());
  });
  it("rejects an invalid later revision before import", () => {
    const value = bundle();
    value.scenarios[1]!.revisions[0]!.document.formatVersion = 99 as 1;
    expect(() => parseMockerTransfer(JSON.stringify(value))).toThrow();
  });
  it.each([
    [],
    Array.from({ length: 21 }, () => ({ revisions: [rev("x")] })),
    [{ revisions: [] }],
    [{ revisions: Array.from({ length: 201 }, () => rev("x")) }],
  ])("enforces package limits", (scenarios) => {
    expect(() => parseMockerTransfer(JSON.stringify({ ...bundle(), scenarios }))).toThrow();
  });
  it("rejects bad history metadata", () => {
    const value = bundle();
    value.scenarios[0]!.revisions[0]!.source = "unknown";
    expect(() => parseMockerTransfer(JSON.stringify(value))).toThrow(/истории/);
  });
});
