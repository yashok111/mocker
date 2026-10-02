import { afterEach, describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderInRouter } from "@/test/render";
import { exampleCanvas } from "./canvasModel";
import { parseMockerScenario } from "./scenarioMockerFile";
import { ScenarioMockerExport } from "./ScenarioMockerExport";
import * as files from "./scenarioExportFiles";

afterEach(() => vi.restoreAllMocks());
describe("scenario file export", () => {
  it("downloads the current document and field buffers", async () => {
    const download = vi.spyOn(files, "downloadScenarioBlob").mockImplementation(() => {});
    const document = exampleCanvas();
    document.title = "Текущие изменения";
    document.contracts.forEach((contract) => {
      contract.mode = "copy";
    });
    renderInRouter(<ScenarioMockerExport document={document} formDrafts={{ note: "draft" }} />);
    await userEvent.click(await screen.findByRole("button", { name: "Экспорт .mocker" }));
    await userEvent.click(screen.getByRole("button", { name: "Скачать .mocker" }));
    expect(download).toHaveBeenCalledOnce();
    const [blob, filename] = download.mock.calls[0]!;
    expect(filename).toBe("scenario.mocker");
    expect(parseMockerScenario(await blob.text())).toEqual({
      document,
      formDrafts: { note: "draft" },
    });
  });
  it("reports an invalid document without downloading a broken file", async () => {
    const download = vi.spyOn(files, "downloadScenarioBlob").mockImplementation(() => {});
    const document = exampleCanvas();
    document.messages[0]!.fromId = "missing";
    renderInRouter(<ScenarioMockerExport document={document} formDrafts={{}} />);
    await userEvent.click(await screen.findByRole("button", { name: "Экспорт .mocker" }));
    await userEvent.click(screen.getByRole("button", { name: "Скачать .mocker" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(/fromId/);
    expect(download).not.toHaveBeenCalled();
  });
});
