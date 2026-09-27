import { afterEach, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { json, route } from "@/test/http";
import { exampleCanvas } from "./canvasModel";
import type { DesignScenarioRevision } from "./designScenarioApi";
import { ScenarioResultsModal } from "./ScenarioResultsModal";

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const revision: DesignScenarioRevision = {
  id: 11,
  scenarioId: 7,
  version: 1,
  hash: "hash-11",
  source: "ui",
  summary: "",
  createdAt: 1000,
  document: exampleCanvas(),
  formDrafts: {},
};

it.each([
  ["postman", "Postman", "collection.json", '{"info":{"name":"Saved collection"}}'],
  ["curl", "cURL", "requests.sh", "#!/bin/sh\ncurl --request GET --url 'https://example.test'"],
])(
  "downloads %s from the selected revision without a contract filter",
  async (format, label, filename, content) => {
    const fetch = route({
      "GET /api/design-scenarios/7/revisions/11/export-options": () =>
        json(200, {
          scenarioId: 7,
          revisionId: 11,
          sourceHash: "hash-11",
          options: [{ format, ready: true, diagnostics: [] }],
        }),
      [`GET /api/design-scenarios/7/revisions/11/exports/${format}`]: () =>
        json(200, {
          scenarioId: 7,
          revisionId: 11,
          sourceHash: "hash-11",
          format,
          filename,
          content,
          mediaType: "text/plain",
          diagnostics: [],
        }),
    });
    const download = vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:download");
    vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {});
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    renderWithProviders(
      <ScenarioResultsModal
        opened
        scenarioId={7}
        revision={revision}
        latestRevisionId={12}
        onClose={vi.fn()}
        onLocate={vi.fn()}
        onPrepareContracts={vi.fn()}
        onRun={vi.fn()}
      />,
    );
    await userEvent.selectOptions(screen.getByLabelText("Формат результата"), format);
    expect(await screen.findByLabelText("Предпросмотр результата")).toHaveTextContent(
      content.replace(/\n/g, " "),
    );
    expect(screen.queryByLabelText("Весь API-контракт")).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: `Скачать ${label}` }));
    expect(await (download.mock.calls[0]![0] as Blob).text()).toBe(content);
    expect(
      fetch.mock.calls.every(
        ([input]) =>
          !String(input).includes("contractId=") && !String(input).includes("/revisions/12/"),
      ),
    ).toBe(true);
  },
);

it("shows missing HTTP inputs before download and opens execution settings", async () => {
  route({
    "GET /api/design-scenarios/7/revisions/11/export-options": () =>
      json(200, {
        scenarioId: 7,
        revisionId: 11,
        sourceHash: "hash-11",
        options: [
          {
            format: "curl",
            ready: false,
            diagnostics: [
              {
                code: "path_parameter_missing",
                severity: "error",
                message: "Укажите параметр пути id",
              },
            ],
          },
        ],
      }),
  });
  const onRun = vi.fn();
  renderWithProviders(
    <ScenarioResultsModal
      opened
      scenarioId={7}
      revision={revision}
      selection={{ format: "curl", contractId: "" }}
      onClose={vi.fn()}
      onLocate={vi.fn()}
      onPrepareContracts={vi.fn()}
      onRun={onRun}
    />,
  );
  expect(await screen.findByText("Укажите параметр пути id")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Скачать cURL" })).toBeDisabled();
  await userEvent.click(screen.getByRole("button", { name: "Открыть выполнение" }));
  expect(onRun).toHaveBeenCalledOnce();
});

it("previews and downloads Mermaid while offering API repair and diagnostic navigation", async () => {
  const target = { kind: "message" as const, id: revision.document.messages[0]!.id };
  route({
    "GET /api/design-scenarios/7/revisions/11/export-options": () =>
      json(200, {
        scenarioId: 7,
        revisionId: 11,
        sourceHash: "hash-11",
        options: [
          {
            format: "mermaid",
            ready: true,
            diagnostics: [
              { code: "description", severity: "warning", message: "Уточните сообщение", target },
            ],
          },
          {
            format: "openapi-json",
            contractId: revision.document.contracts[0]!.id,
            ready: false,
            diagnostics: [
              { code: "api", severity: "error", message: "Завершите редактирование API", target },
            ],
          },
        ],
      }),
    "GET /api/design-scenarios/7/revisions/11/exports/mermaid": () =>
      json(200, {
        scenarioId: 7,
        revisionId: 11,
        sourceHash: "hash-11",
        format: "mermaid",
        content: "sequenceDiagram\nA->>B: Hi",
        filename: "scenario.mmd",
        mediaType: "text/plain",
        diagnostics: [],
      }),
  });
  const download = vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:test");
  vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {});
  vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
  const onLocate = vi.fn();
  const onClose = vi.fn();
  renderWithProviders(
    <ScenarioResultsModal
      opened
      scenarioId={7}
      revision={revision}
      onClose={onClose}
      onLocate={onLocate}
      onPrepareContracts={vi.fn()}
      onRun={vi.fn()}
    />,
  );
  const dialog = await screen.findByRole("dialog", { name: "Получить результат" });
  expect(await within(dialog).findByLabelText("Предпросмотр результата")).toHaveTextContent(
    "sequenceDiagram",
  );
  await userEvent.click(within(dialog).getByRole("button", { name: "Скачать Mermaid" }));
  expect(await (download.mock.calls[0]![0] as Blob).text()).toBe("sequenceDiagram\nA->>B: Hi");
  await userEvent.selectOptions(within(dialog).getByLabelText("Формат результата"), "openapi-json");
  await userEvent.selectOptions(
    within(dialog).getByLabelText("Весь API-контракт"),
    revision.document.contracts[0]!.id,
  );
  expect(await within(dialog).findByText("Завершите редактирование API")).toBeInTheDocument();
  expect(within(dialog).getByRole("button", { name: "Описать API" })).toBeInTheDocument();
  await userEvent.click(within(dialog).getByRole("button", { name: "Перейти к объекту" }));
  expect(onLocate).toHaveBeenCalledWith(target);
  expect(onClose).toHaveBeenCalledOnce();
});

it("requires an explicit contract choice when several services are available", async () => {
  const second = { ...revision.document.contracts[0]!, id: "second-api", name: "Второй сервис" };
  const manyContracts = {
    ...revision,
    document: { ...revision.document, contracts: [...revision.document.contracts, second] },
  };
  const fetch = route({
    "GET /api/design-scenarios/7/revisions/11/export-options": () =>
      json(200, {
        scenarioId: 7,
        revisionId: 11,
        sourceHash: "hash-11",
        options: [
          {
            format: "openapi-json",
            contractId: revision.document.contracts[0]!.id,
            ready: true,
            diagnostics: [],
          },
          { format: "openapi-json", contractId: second.id, ready: true, diagnostics: [] },
        ],
      }),
    [`GET /api/design-scenarios/7/revisions/11/exports/openapi-json?contractId=${second.id}`]: () =>
      json(200, {
        scenarioId: 7,
        revisionId: 11,
        sourceHash: "hash-11",
        format: "openapi-json",
        content: "{}",
        filename: "second.json",
        mediaType: "application/json",
        diagnostics: [],
      }),
  });
  renderWithProviders(
    <ScenarioResultsModal
      opened
      scenarioId={7}
      revision={manyContracts}
      onClose={vi.fn()}
      onLocate={vi.fn()}
      onPrepareContracts={vi.fn()}
      onRun={vi.fn()}
    />,
  );
  const dialog = await screen.findByRole("dialog", { name: "Получить результат" });
  await userEvent.selectOptions(within(dialog).getByLabelText("Формат результата"), "openapi-json");
  const selector = within(dialog).getByLabelText("Весь API-контракт");
  expect(selector).toHaveValue("");
  expect(within(dialog).getByRole("button", { name: "Скачать OpenAPI JSON" })).toBeDisabled();
  expect(fetch.mock.calls.some(([input]) => String(input).includes("/exports/openapi-json"))).toBe(
    false,
  );
  await userEvent.selectOptions(selector, second.id);
  expect(await within(dialog).findByLabelText("Предпросмотр результата")).toHaveTextContent("{}");
  expect(
    fetch.mock.calls.some(([input]) => String(input).includes(`contractId=${second.id}`)),
  ).toBe(true);
});

it("keeps preview requests on revision 11 until explicit refresh", async () => {
  const fetch = route({
    "GET /api/design-scenarios/7/revisions/11/export-options": () =>
      json(200, {
        scenarioId: 7,
        revisionId: 11,
        sourceHash: "hash-11",
        options: [{ format: "mermaid", ready: true, diagnostics: [] }],
      }),
    "GET /api/design-scenarios/7/revisions/11/exports/mermaid": () =>
      json(200, {
        scenarioId: 7,
        revisionId: 11,
        sourceHash: "hash-11",
        format: "mermaid",
        content: "old",
        filename: "old.mmd",
        mediaType: "text/plain",
        diagnostics: [],
      }),
  });
  const onRefresh = vi.fn();
  const props = {
    opened: true,
    scenarioId: 7,
    revision,
    latestRevisionId: 12,
    onRefresh,
    onClose: vi.fn(),
    onLocate: vi.fn(),
    onPrepareContracts: vi.fn(),
    onRun: vi.fn(),
  };
  renderWithProviders(<ScenarioResultsModal {...props} />);
  expect(await screen.findByLabelText("Предпросмотр результата")).toHaveTextContent("old");
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Обновить до текущей ревизии" })).toBeInTheDocument(),
  );
  expect(fetch.mock.calls.every(([input]) => !String(input).includes("/revisions/12/"))).toBe(true);
  await userEvent.click(screen.getByRole("button", { name: "Обновить до текущей ревизии" }));
  expect(onRefresh).toHaveBeenCalledOnce();
});
