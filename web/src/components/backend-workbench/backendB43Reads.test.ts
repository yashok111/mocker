import { afterEach, expect, it, vi } from "vitest";
import { readAnalysis } from "./backendAnalysisReads";
import { analysisTestDetail } from "./backendAnalysisTestFixtures";
import { changeTestID } from "./backendChangeTestFixtures";
afterEach(() => vi.unstubAllGlobals());
it("rejects a job whose immutable context names a different kind", async () => {
  const detail = analysisTestDetail();
  detail.job.kind = "conformance";
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response(JSON.stringify(detail))),
  );
  await expect(readAnalysis(changeTestID, changeTestID)).rejects.toThrow();
});
