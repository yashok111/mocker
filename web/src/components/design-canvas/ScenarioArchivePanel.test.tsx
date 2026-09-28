import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { renderWithProviders } from "@/test/render";
import type { ScenarioExportOptions } from "@/api/generated/schemas";
import { exampleCanvas } from "./canvasModel";
import type { DesignScenarioRevision } from "./designScenarioApi";
import { buildScenarioArchive } from "./scenarioArchiveExport";
import { downloadScenarioBlob } from "./scenarioExportFiles";
import { ScenarioArchivePanel } from "./ScenarioArchivePanel";

vi.mock("./scenarioArchiveExport", async (importOriginal) => ({
  ...(await importOriginal<typeof import("./scenarioArchiveExport")>()),
  buildScenarioArchive: vi.fn(),
}));
vi.mock("./scenarioExportFiles", () => ({ downloadScenarioBlob: vi.fn() }));
const revision: DesignScenarioRevision = {
  id: 11,
  scenarioId: 7,
  version: 2,
  hash: "saved",
  source: "ui",
  summary: "",
  createdAt: 1000,
  document: exampleCanvas(),
  formDrafts: {},
};
const contract = revision.document.contracts[0]!;
const options: ScenarioExportOptions = {
  scenarioId: 7,
  revisionId: 11,
  sourceHash: "saved",
  options: [
    { format: "mermaid", ready: true, diagnostics: [] },
    { format: "markdown", ready: true, diagnostics: [] },
    { format: "openapi-json", contractId: contract.id, ready: true, diagnostics: [] },
    {
      format: "curl",
      ready: false,
      diagnostics: [{ code: "missing", severity: "error", message: "Укажите base URL" }],
    },
  ],
};
beforeEach(() => {
  vi.mocked(buildScenarioArchive).mockResolvedValue(new Blob(["zip"], { type: "application/zip" }));
});
afterEach(() => {
  vi.clearAllMocks();
});

it("selects ready formats and contracts, exposes blocked reasons and downloads only selected files", async () => {
  renderWithProviders(
    <ScenarioArchivePanel revision={revision} options={options} onLocate={vi.fn()} />,
  );
  expect(screen.getByRole("checkbox", { name: "Mermaid" })).toBeChecked();
  expect(screen.getByRole("checkbox", { name: "cURL" })).toBeDisabled();
  expect(screen.getByText("Укажите base URL")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("checkbox", { name: "Markdown" }));
  await userEvent.click(screen.getByRole("checkbox", { name: `OpenAPI JSON · ${contract.name}` }));
  await userEvent.click(screen.getByRole("checkbox", { name: "SVG" }));
  await userEvent.click(screen.getByRole("button", { name: "Скачать ZIP" }));
  await waitFor(() => expect(downloadScenarioBlob).toHaveBeenCalledOnce());
  expect(buildScenarioArchive).toHaveBeenCalledWith(
    revision,
    [{ format: "mermaid" }, { format: "openapi-json", contractId: contract.id }, { format: "svg" }],
    expect.any(Object),
  );
  expect(downloadScenarioBlob).toHaveBeenCalledWith(expect.any(Blob), "scenario-7-r11.zip");
});

it("forwards an archive diagnostic pointer to its event field", async () => {
  const target = { kind: "event-message" as const, id: "created" };
  const pointer = "/eventModel/messages/0/examples/1/payloadJSON";
  const onLocate = vi.fn();
  renderWithProviders(
    <ScenarioArchivePanel
      revision={revision}
      options={{
        ...options,
        options: [
          {
            format: "asyncapi-json",
            contractId: "events",
            ready: false,
            diagnostics: [
              {
                code: "event_example_invalid",
                severity: "error",
                message: "Исправьте пример",
                target,
                pointer,
              },
            ],
          },
        ],
      }}
      onLocate={onLocate}
    />,
  );
  await userEvent.click(screen.getByRole("button", { name: "Перейти к объекту" }));
  expect(onLocate).toHaveBeenCalledWith(target, pointer);
});

it("disables an empty selection and displays build errors without partial download", async () => {
  renderWithProviders(
    <ScenarioArchivePanel revision={revision} options={options} onLocate={vi.fn()} />,
  );
  await userEvent.click(screen.getByRole("checkbox", { name: "Mermaid" }));
  await userEvent.click(screen.getByRole("checkbox", { name: "Markdown" }));
  expect(screen.getByRole("button", { name: "Скачать ZIP" })).toBeDisabled();
  await userEvent.click(screen.getByRole("checkbox", { name: "SVG" }));
  vi.mocked(buildScenarioArchive).mockRejectedValueOnce(new Error("PNG слишком большой"));
  await userEvent.click(screen.getByRole("button", { name: "Скачать ZIP" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("PNG слишком большой");
  expect(downloadScenarioBlob).not.toHaveBeenCalled();
});

it("keeps the order in which formats were selected", async () => {
  renderWithProviders(
    <ScenarioArchivePanel revision={revision} options={options} onLocate={vi.fn()} />,
  );
  for (const name of ["Mermaid", "Markdown", "SVG", "Markdown"]) {
    await userEvent.click(screen.getByRole("checkbox", { name }));
  }
  await userEvent.click(screen.getByRole("button", { name: "Скачать ZIP" }));
  await waitFor(() => expect(buildScenarioArchive).toHaveBeenCalledOnce());
  expect(vi.mocked(buildScenarioArchive).mock.calls[0]![1]).toEqual([
    { format: "svg" },
    { format: "markdown" },
  ]);
});

it("cancels a pending archive on unmount so closing or refreshing cannot download stale bytes", async () => {
  let resolve!: (blob: Blob) => void;
  vi.mocked(buildScenarioArchive).mockReturnValueOnce(
    new Promise((r) => {
      resolve = r;
    }),
  );
  const view = renderWithProviders(
    <ScenarioArchivePanel revision={revision} options={options} onLocate={vi.fn()} />,
  );
  await userEvent.click(screen.getByRole("button", { name: "Скачать ZIP" }));
  expect(screen.getByRole("checkbox", { name: "Mermaid" })).toBeDisabled();
  view.unmount();
  expect(vi.mocked(buildScenarioArchive).mock.calls[0]![2]!.isCancelled!()).toBe(true);
  await act(async () => {
    resolve(new Blob(["old zip"]));
  });
  expect(downloadScenarioBlob).not.toHaveBeenCalled();
});
