import { afterEach, expect, it, vi } from "vitest";
import { cleanup, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { json } from "@/test/http";
import { ViewChooser } from "./ViewChooser";
import { projectId, revisionId, mapPage } from "./testFixtures";
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
it("searches every scenario catalog page and opens its exact pin", async () => {
  const pin = {
    id: "0197aaf9-5555-7000-8000-000000000088",
    version: 4,
    contentHash: "a".repeat(64),
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const u = new URL(String(input), "http://localhost");
      if (u.pathname.endsWith("/explore/query")) return json(200, mapPage());
      if (u.pathname.endsWith("/diagrams"))
        return json(200, {
          catalogVersion: 1,
          nextCursor:
            u.searchParams.get("kind") === "interactions" && !u.searchParams.get("cursor")
              ? "second"
              : "",
          items:
            u.searchParams.get("cursor") === "second"
              ? [
                  {
                    id: pin.id,
                    pin,
                    kind: "interactions",
                    name: "Получить ссылку на Литрес",
                    target: { revisionId },
                    targetHash: "a".repeat(64),
                  },
                ]
              : [],
        });
      return json(404, {});
    }),
  );
  const navigate = vi.fn();
  renderWithProviders(
    <ViewChooser
      projectId={projectId}
      target={{ revisionId }}
      view="scenarios"
      onNavigate={navigate}
    />,
  );
  await userEvent.type(await screen.findByLabelText("Поиск сценариев"), "Литрес");
  await userEvent.click(screen.getByRole("button", { name: /Получить ссылку на Литрес/ }));
  expect(navigate).toHaveBeenCalledWith(
    expect.objectContaining({
      diagramId: pin.id,
      diagramVersion: 4,
      diagramHash: pin.contentHash,
      wbView: "scenarios",
    }),
  );
});
