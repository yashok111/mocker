import { afterEach, expect, it, vi } from "vitest";
import { downloadBackendSVG } from "./backendSVGDownload";

afterEach(() => vi.unstubAllGlobals());
it("downloads the exact generated SVG route as a Blob with a visible filename", async () => {
  const fetch = vi.fn(
    async (_url: string, _init?: RequestInit) =>
      new Response('<svg xmlns="http://www.w3.org/2000/svg"/>', {
        status: 200,
        headers: { "Content-Type": "image/svg+xml" },
      }),
  );
  vi.stubGlobal("fetch", fetch);
  const create = vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:svg");
  const revoke = vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {});
  const click = vi
    .spyOn(HTMLAnchorElement.prototype, "click")
    .mockImplementation(function (this: HTMLAnchorElement) {
      expect(this.download).toBe("backend-view-22222222-2222-4222-8222-222222222222-v2.svg");
    });
  await downloadBackendSVG({
    projectId: "11111111-1111-4111-8111-111111111111",
    viewId: "22222222-2222-4222-8222-222222222222",
    viewVersion: 2,
  });
  expect(fetch.mock.calls[0]?.[0]).toBe(
    "/api/backend-projects/11111111-1111-4111-8111-111111111111/diagram-views/22222222-2222-4222-8222-222222222222/versions/2/svg",
  );
  expect(create).toHaveBeenCalledWith(expect.any(Blob));
  expect(click).toHaveBeenCalledOnce();
  await new Promise((resolve) => setTimeout(resolve, 5));
  expect(revoke).toHaveBeenCalledWith("blob:svg");
});
