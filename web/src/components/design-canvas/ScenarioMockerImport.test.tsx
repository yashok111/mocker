import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderInRouter } from "@/test/render";
import { json, route } from "@/test/http";
import { exampleCanvas } from "./canvasModel";
import { parseMockerScenario, serializeMockerScenario } from "./scenarioMockerFile";
import { DesignScenariosPage } from "./DesignScenariosPage";

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
const file = () =>
  new File([serializeMockerScenario(exampleCanvas(), { note: "draft" })], "example.mocker", {
    type: "application/json",
  });
async function openImport() {
  await userEvent.click(await screen.findByRole("button", { name: "Импорт .mocker" }));
  return screen.getByLabelText("Файл .mocker");
}
const posts = (fetchMock: ReturnType<typeof route>) =>
  fetchMock.mock.calls.filter(([, init]) => init?.method === "POST");

describe("scenario file import", () => {
  it("previews the file, creates a new scenario, and opens it", async () => {
    const fetchMock = route({
      "GET /api/design-scenarios": () => json(200, { scenarios: [] }),
      "POST /api/design-scenarios": () => json(201, { scenario: { id: 24 } }),
    });
    renderInRouter(<DesignScenariosPage />);
    const uploaded = file();
    await userEvent.upload(await openImport(), uploaded);
    expect(await screen.findByText(exampleCanvas().title)).toBeInTheDocument();
    expect(posts(fetchMock)).toHaveLength(0);
    await userEvent.click(screen.getByRole("button", { name: "Импортировать сценарий" }));
    expect(await screen.findByTestId("test-router-elsewhere")).toBeInTheDocument();
    expect(JSON.parse(String(posts(fetchMock)[0]?.[1]?.body))).toMatchObject(
      parseMockerScenario(await uploaded.text()),
    );
  });
  it("clears a previously valid file when invalid input is selected", async () => {
    const fetchMock = route({ "GET /api/design-scenarios": () => json(200, { scenarios: [] }) });
    renderInRouter(<DesignScenariosPage />);
    const input = await openImport();
    await userEvent.upload(input, file());
    await screen.findByText(exampleCanvas().title);
    await userEvent.upload(input, new File(["{"], "bad.mocker"));
    expect(await screen.findByRole("alert")).toHaveTextContent(/JSON/);
    expect(screen.getByRole("button", { name: "Импортировать сценарий" })).toBeDisabled();
    expect(posts(fetchMock)).toHaveLength(0);
  });
  it("rejects oversized files before reading them", async () => {
    route({ "GET /api/design-scenarios": () => json(200, { scenarios: [] }) });
    renderInRouter(<DesignScenariosPage />);
    const huge = new File(["x".repeat(2_000_001)], "large.mocker");
    const read = vi.spyOn(huge, "text");
    await userEvent.upload(await openImport(), huge);
    expect(await screen.findByRole("alert")).toHaveTextContent(/2 МБ/);
    expect(read).not.toHaveBeenCalled();
  });
  it("allows retry after an API failure", async () => {
    const fetchMock = route({
      "GET /api/design-scenarios": () => json(200, { scenarios: [] }),
      "POST /api/design-scenarios": () => json(500, {}),
    });
    renderInRouter(<DesignScenariosPage />);
    await userEvent.upload(await openImport(), file());
    await screen.findByText(exampleCanvas().title);
    await userEvent.click(screen.getByRole("button", { name: "Импортировать сценарий" }));
    await screen.findByRole("alert");
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Импортировать сценарий" })).toBeEnabled(),
    );
    await userEvent.click(screen.getByRole("button", { name: "Импортировать сценарий" }));
    await waitFor(() => expect(posts(fetchMock)).toHaveLength(2));
  });
});

it("imports a package atomically with optional API relinking and shows the result", async () => {
  const snapshot = {
    document: exampleCanvas(),
    formDrafts: {},
    summary: "v1",
    source: "ui",
    createdAt: 100,
  };
  const bundle = {
    kind: "mocker.scenarios",
    formatVersion: 1,
    scenarios: [
      { revisions: [snapshot, { ...snapshot, summary: "v2" }] },
      { revisions: [snapshot] },
    ],
  };
  const fetchMock = route({
    "GET /api/design-scenarios": () => json(200, { scenarios: [] }),
    "POST /api/design-scenarios/transfer-import": () =>
      json(201, {
        scenarios: [
          { id: 30, name: "Imported" },
          { id: 31, name: "Other" },
        ],
        linkedContracts: 2,
        copiedContracts: 1,
      }),
  });
  renderInRouter(<DesignScenariosPage />);
  await userEvent.upload(
    await openImport(),
    new File([JSON.stringify(bundle)], "scenarios.mocker"),
  );
  await screen.findByText("Сценариев: 2, ревизий: 3.");
  await userEvent.click(screen.getByRole("checkbox", { name: "Связать совпадающие API" }));
  await userEvent.click(screen.getByRole("button", { name: "Импортировать сценарий" }));
  expect(await screen.findByText("Создано сценариев: 2.")).toBeInTheDocument();
  expect(screen.getByText(/Связей с API: 2/)).toBeInTheDocument();
  expect(posts(fetchMock)).toHaveLength(1);
  expect(JSON.parse(String(posts(fetchMock)[0]![1]?.body))).toMatchObject({
    relink: true,
    bundle: {
      scenarios: [
        { revisions: [{ summary: "v1" }, { summary: "v2" }] },
        { revisions: [{ summary: "v1" }] },
      ],
    },
  });
  await userEvent.click(screen.getByRole("button", { name: "Открыть Imported" }));
  expect(await screen.findByTestId("test-router-elsewhere")).toBeInTheDocument();
});
