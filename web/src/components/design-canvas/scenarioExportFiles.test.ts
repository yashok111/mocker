import { afterEach, expect, it, vi } from "vitest";
import { downloadScenarioArtifact } from "./scenarioExportFiles";

afterEach(() => vi.restoreAllMocks());

it("downloads the exact artifact text and revokes its URL", async () => {
  const create = vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:artifact");
  const revoke = vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {});
  const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
  downloadScenarioArtifact({
    content: '{"big":9007199254740993}',
    filename: "api.json",
    mediaType: "application/json",
  });
  const blob = create.mock.calls[0]![0] as Blob;
  expect(await blob.text()).toBe('{"big":9007199254740993}');
  expect(blob.type).toBe("application/json");
  expect(click).toHaveBeenCalledOnce();
  await vi.waitFor(() => expect(revoke).toHaveBeenCalledWith("blob:artifact"));
});
