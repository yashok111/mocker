import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { exampleCanvas } from "./canvasModel";
import { CanvasContractConversionModal } from "./CanvasContractConversionModal";

afterEach(() => vi.unstubAllGlobals());

function unboundDocument() {
  const document = exampleCanvas();
  const request = document.messages.find((message) => message.operation)!;
  document.messages = [
    { ...request, operation: undefined, label: "Войти" },
    {
      id: "reply",
      fromId: request.toId,
      toId: request.fromId,
      kind: "response" as const,
      label: "Токен",
      description: "",
      replyToId: request.id,
    },
  ];
  document.fragments = [];
  document.contracts = [];
  return document;
}

describe("CanvasContractConversionModal", () => {
  it("creates a contract identifier without randomUUID on HTTP", async () => {
    const document = exampleCanvas();
    vi.stubGlobal("crypto", { getRandomValues: crypto.getRandomValues.bind(crypto) });
    const create = vi.fn().mockResolvedValue(undefined);
    renderWithProviders(
      <CanvasContractConversionModal
        document={document}
        expectedVersion={1}
        onClose={vi.fn()}
        onCreate={create}
      />,
    );

    await userEvent.click(screen.getByRole("button", { name: "Создать API-проект" }));

    await waitFor(() => expect(create).toHaveBeenCalledOnce());
    expect(create.mock.calls[0]![0][0].contract.id).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/,
    );
  });

  it("requires explicit method, path and reply status and allows excluding calls", async () => {
    const create = vi.fn().mockResolvedValue(undefined);
    renderWithProviders(
      <CanvasContractConversionModal
        document={unboundDocument()}
        expectedVersion={3}
        onClose={vi.fn()}
        onCreate={create}
      />,
    );
    const submit = screen.getByRole("button", { name: "Создать API-проект" });
    expect(submit).toBeDisabled();
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Метод" }), "POST");
    await userEvent.type(screen.getByRole("textbox", { name: "Путь" }), "/auth/login");
    expect(submit).toBeDisabled();
    await userEvent.type(screen.getByRole("textbox", { name: "HTTP-статус: Токен" }), "200");
    expect(submit).toBeEnabled();
    await userEvent.click(screen.getByRole("checkbox", { name: "Включить: Войти" }));
    expect(submit).toBeDisabled();
    await userEvent.click(screen.getByRole("checkbox", { name: "Включить: Войти" }));
    await userEvent.click(submit);
    expect(create).toHaveBeenCalledWith(
      expect.arrayContaining([
        expect.objectContaining({
          type: "create_contract",
          contract: expect.objectContaining({
            document: expect.objectContaining({
              paths: expect.objectContaining({
                "/auth/login": expect.objectContaining({
                  post: expect.objectContaining({ responses: { "200": { description: "Токен" } } }),
                }),
              }),
            }),
          }),
        }),
      ]),
      3,
    );
  });

  it("prevents closing or changing inputs while submitting and keeps values after errors", async () => {
    let reject: (reason: Error) => void = () => {};
    const create = vi.fn(
      () =>
        new Promise<void>((_, fail) => {
          reject = fail;
        }),
    );
    const close = vi.fn();
    renderWithProviders(
      <CanvasContractConversionModal
        document={exampleCanvas()}
        expectedVersion={5}
        onClose={close}
        onCreate={create}
      />,
    );
    const name = screen.getByRole("textbox", { name: "Название API" });
    await userEvent.clear(name);
    await userEvent.type(name, "Заказы API");
    await userEvent.click(screen.getByRole("button", { name: "Создать API-проект" }));
    expect(name).toBeDisabled();
    expect(screen.getByRole("button", { name: "Отмена" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Закрыть предпросмотр" })).toBeDisabled();
    await userEvent.keyboard("{Escape}");
    expect(close).not.toHaveBeenCalled();
    reject(new Error("Сервер недоступен"));
    expect(await screen.findByRole("alert")).toHaveTextContent("Сервер не ответил");
    await waitFor(() => expect(name).toBeEnabled());
    expect(name).toHaveValue("Заказы API");
    expect(create).toHaveBeenCalledTimes(1);
  });
});
