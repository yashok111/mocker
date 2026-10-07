import { afterEach, expect, it, vi } from "vitest";
import { cleanup, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { json } from "@/test/http";
import { Results } from "./Results";
import { analysisTestDetail, analysisTestManifest } from "../backendAnalysisTestFixtures";
import { changeTestID } from "../backendChangeTestFixtures";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it.each([undefined, 1])(
  "offers the completed report without advancing opened version %s",
  async (opened) => {
    const detail = analysisTestDetail();
    const reports: number[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const url = new URL(String(input), "http://localhost");
        if (url.pathname.endsWith("/results")) {
          const resultVersion = Number(url.searchParams.get("resultVersion"));
          reports.push(resultVersion);
          return json(200, {
            manifest: { ...analysisTestManifest(), resultVersion },
            items: [],
            nextCursor: "",
          });
        }
        if (url.pathname.endsWith(`/analyses/${changeTestID}`)) return json(200, detail);
        return json(200, { items: [], nextCursor: "" });
      }),
    );
    const navigate = vi.fn();
    renderWithProviders(
      <Results
        projectId={changeTestID}
        target={{ revisionId: changeTestID }}
        search={{
          wbPanel: "checks",
          wbResult: changeTestID,
          wbResultKind: "check",
          wbResultVersion: opened,
        }}
        onNavigate={navigate}
      />,
    );
    const open = await screen.findByRole("button", { name: "Открыть результат · версия 2" });
    expect(navigate).not.toHaveBeenCalled();
    expect(reports.every((v) => v === opened)).toBe(true);
    await userEvent.click(open);
    expect(navigate).toHaveBeenLastCalledWith(
      expect.objectContaining({ wbResult: changeTestID, wbResultVersion: 2 }),
    );
  },
);
