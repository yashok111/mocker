import { afterEach, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderInRouter } from "@/test/render";
import { json, route } from "@/test/http";
import { DesignScenariosPage } from "./DesignScenariosPage";
import { emptyCanvas } from "./canvasModel";
import * as files from "./scenarioExportFiles";
afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
it("exports selected scenarios with history as a portable package", async () => {
  const bundle = {
    kind: "mocker.scenarios",
    formatVersion: 1,
    scenarios: [
      {
        revisions: [
          { document: emptyCanvas(), formDrafts: {}, source: "ui", summary: "", createdAt: 100 },
        ],
      },
    ],
  };
  const fetchMock = route({
    "GET /api/design-scenarios": () =>
      json(200, {
        scenarios: [
          { id: 1, name: "First", version: 2, draftRevisionId: 2 },
          { id: 2, name: "Other", version: 1, draftRevisionId: 3 },
        ],
      }),
    "POST /api/design-scenarios/transfer-export": () => json(200, bundle),
  });
  const download = vi.spyOn(files, "downloadScenarioBlob").mockImplementation(() => {});
  renderInRouter(<DesignScenariosPage />);
  await userEvent.click(await screen.findByRole("button", { name: "Экспорт пакета" }));
  await userEvent.click(screen.getByRole("checkbox", { name: "First" }));
  await userEvent.click(screen.getByRole("checkbox", { name: "Включить историю ревизий" }));
  await userEvent.click(screen.getByRole("button", { name: "Скачать пакет .mocker" }));
  const call = fetchMock.mock.calls.find(([input]) => String(input).endsWith("/transfer-export"));
  expect(JSON.parse(String(call?.[1]?.body))).toEqual({ scenarioIds: [1], includeHistory: true });
  expect(download).toHaveBeenCalledOnce();
  expect(JSON.parse(await download.mock.calls[0]![0].text())).toEqual(bundle);
});
