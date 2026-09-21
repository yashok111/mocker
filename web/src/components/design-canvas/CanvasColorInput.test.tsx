import { useState } from "react";
import { fireEvent, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { renderWithProviders } from "@/test/render";
import { CanvasColorInput } from "./CanvasColorInput";

describe("CanvasColorInput", () => {
  it("keeps a complete typed hex and follows external undo and reset", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    function Harness() {
      const [color, setColor] = useState<string | undefined>("#123456");
      return (
        <>
          <CanvasColorInput
            label="Цвет карточки"
            value={color}
            onChange={(value) => {
              onChange(value);
              setColor(value);
            }}
          />
          <button onClick={() => setColor("#123456")}>Undo</button>
        </>
      );
    }
    renderWithProviders(<Harness />);
    const input = screen.getByLabelText("Цвет карточки");
    await user.clear(input);
    await user.type(input, "#abcdef");
    expect(onChange).toHaveBeenCalledExactlyOnceWith("#abcdef");
    await user.click(screen.getByRole("button", { name: "Undo" }));
    expect(input).toHaveValue("#123456");
    await user.click(screen.getByRole("button", { name: "Сбросить цвет карточки" }));
    expect(input).toHaveValue("");
    expect(onChange.mock.lastCall).toEqual([undefined]);
  });

  it("commits a palette selection", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    renderWithProviders(
      <CanvasColorInput label="Цвет карточки" value={undefined} onChange={onChange} />,
    );
    await user.click(screen.getByLabelText("Цвет карточки"));
    await user.click(screen.getByRole("button", { name: "#d3f9d8" }));
    expect(onChange).toHaveBeenCalledExactlyOnceWith("#d3f9d8");
  });

  it("clearing and leaving the field resets to the default", () => {
    const onChange = vi.fn();
    renderWithProviders(
      <CanvasColorInput label="Цвет карточки" value="#123456" onChange={onChange} />,
    );
    const input = screen.getByLabelText("Цвет карточки");
    fireEvent.change(input, { target: { value: "" } });
    expect(onChange).not.toHaveBeenCalled();
    fireEvent.blur(input);
    expect(onChange).toHaveBeenCalledExactlyOnceWith(undefined);
  });
});
