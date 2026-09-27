import { fireEvent, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { renderWithProviders } from "@/test/render";
import { addLocalOperation, emptyCanvas } from "./canvasModel";
import { CanvasCreateOperationModal } from "./CanvasCreateOperationModal";

describe("CanvasCreateOperationModal", () => {
  it("requires an explicit endpoint for a descriptive label", async () => {
    const onCreate = vi.fn();
    renderWithProviders(
      <CanvasCreateOperationModal
        document={emptyCanvas()}
        label="Получить клиента"
        onClose={vi.fn()}
        onCreate={onCreate}
      />,
    );
    expect(screen.getByLabelText("Путь")).toHaveValue("");
    const submit = screen.getByRole("button", { name: "Создать операцию" });
    expect(submit).toBeDisabled();
    await userEvent.selectOptions(screen.getByLabelText("Метод"), "GET");
    fireEvent.change(screen.getByLabelText("Путь"), { target: { value: "/users/{id}" } });
    expect(submit).toBeEnabled();
    await userEvent.click(submit);
    expect(onCreate).toHaveBeenCalledWith({
      method: "GET",
      path: "/users/{id}",
      responseStatus: "default",
    });
  });
  it("offers an existing matching operation in its named contract", async () => {
    const base = {
      ...emptyCanvas(),
      messages: [
        {
          id: "request",
          kind: "request" as const,
          fromId: "client",
          toId: "api",
          label: "GET /status",
          description: "",
        },
      ],
    };
    const document = addLocalOperation(base, "request", {
      method: "GET",
      path: "/status",
      responseStatus: "default",
    });
    const onCreate = vi.fn();
    renderWithProviders(
      <CanvasCreateOperationModal
        document={document}
        label="GET /status"
        onClose={vi.fn()}
        onCreate={onCreate}
      />,
    );
    await userEvent.selectOptions(
      screen.getByLabelText("Операция уже есть"),
      document.contracts[0]!.id,
    );
    await userEvent.click(screen.getByRole("button", { name: "Создать операцию" }));
    expect(onCreate).toHaveBeenCalledWith({
      method: "GET",
      path: "/status",
      responseStatus: "default",
      contractId: document.contracts[0]!.id,
    });
  });
});
