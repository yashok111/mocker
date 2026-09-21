import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { json, route } from "@/test/http";
import { exampleCanvas } from "./canvasModel";
import { ScenarioValidationAction } from "./ScenarioValidationAction";

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("ScenarioValidationAction", () => {
  it("validates the current document without saving it and shows success", async () => {
    const document = exampleCanvas();
    const fetchMock = route({
      "POST /api/design-scenarios/12/validate": () => json(200, { diagnostics: [] }),
    });
    renderWithProviders(<ScenarioValidationAction id={12} document={document} />);
    await userEvent.click(screen.getByRole("button", { name: "Проверить сценарий" }));
    expect(await screen.findByText("Ошибок и предупреждений нет")).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))).toEqual({ document });
  });

  it("shows diagnostic severity and location", async () => {
    route({
      "POST /api/design-scenarios/12/validate": () =>
        json(200, {
          diagnostics: [
            {
              pointer: "/messages/0/operation",
              message: "Операция не найдена",
              severity: "warning",
            },
          ],
        }),
    });
    renderWithProviders(<ScenarioValidationAction id={12} document={exampleCanvas()} />);
    await userEvent.click(screen.getByRole("button", { name: "Проверить сценарий" }));
    expect(await screen.findByText("Операция не найдена")).toBeInTheDocument();
    expect(screen.getByText("/messages/0/operation")).toBeInTheDocument();
    expect(screen.queryByText("Ошибок и предупреждений нет")).not.toBeInTheDocument();
  });

  it("does not show a late result as validation of a newer document", async () => {
    let resolve: ((response: Response) => void) | undefined;
    vi.stubGlobal(
      "fetch",
      vi.fn(
        () =>
          new Promise<Response>((done) => {
            resolve = done;
          }),
      ),
    );
    function Harness() {
      const [document, setDocument] = useState(exampleCanvas);
      return (
        <>
          <button onClick={() => setDocument({ ...document, title: "Новая версия" })}>
            Изменить документ
          </button>
          <ScenarioValidationAction id={12} document={document} />
        </>
      );
    }
    renderWithProviders(<Harness />);
    await userEvent.click(screen.getByRole("button", { name: "Проверить сценарий" }));
    await waitFor(() => expect(resolve).toBeDefined());
    fireEvent.click(screen.getByText("Изменить документ"));
    resolve?.(json(200, { diagnostics: [] }));
    expect(
      await screen.findByText("Сценарий изменился. Запустите проверку ещё раз."),
    ).toBeInTheDocument();
    expect(screen.queryByText("Ошибок и предупреждений нет")).not.toBeInTheDocument();
  });

  it("shows a failed validation request and permits retry", async () => {
    route({
      "POST /api/design-scenarios/12/validate": () =>
        json(500, {
          error: { code: "internal", message: "" },
        }),
    });
    renderWithProviders(<ScenarioValidationAction id={12} document={exampleCanvas()} />);
    await userEvent.click(screen.getByRole("button", { name: "Проверить сценарий" }));
    expect(await screen.findByText("Ошибка на сервере. Попробуйте ещё раз")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Проверить снова" })).toBeEnabled();
  });
});
