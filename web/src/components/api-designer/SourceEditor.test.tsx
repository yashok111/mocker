import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { renderWithProviders } from "@/test/render";
import { SourceDiff } from "./SourceEditor";

describe("SourceDiff fallback focus", () => {
  it("selects the requested side when a pointer exists in both documents", () => {
    renderWithProviders(
      <SourceDiff
        original={'{"value":1}'}
        modified={'{"value":2}'}
        narrow={false}
        focusPointer="/value"
        focusSide="before"
      />,
    );
    const original = screen.getByLabelText<HTMLTextAreaElement>("Было");
    expect(original).toHaveFocus();
    expect(original.value.slice(original.selectionStart, original.selectionEnd)).toBe('"value":1');
  });

  it("keeps the default modified-first behavior", () => {
    renderWithProviders(
      <SourceDiff
        original={'{"value":1}'}
        modified={'{"value":2}'}
        narrow={false}
        focusPointer="/value"
      />,
    );
    expect(screen.getByLabelText("Стало")).toHaveFocus();
  });

  it("falls back to before for removed values", () => {
    renderWithProviders(
      <SourceDiff original={'{"value":1}'} modified="{}" narrow={false} focusPointer="/value" />,
    );
    expect(screen.getByLabelText("Было")).toHaveFocus();
  });

  it.each(["before", "after"] as const)(
    "reports a missing pointer on %s without selecting the opposite side",
    (focusSide) => {
      renderWithProviders(
        <SourceDiff
          original={focusSide === "before" ? "{}" : '{"value":1}'}
          modified={focusSide === "after" ? "{}" : '{"value":2}'}
          narrow={false}
          focusPointer="/value"
          focusSide={focusSide}
        />,
      );
      expect(screen.getByRole("status")).toHaveTextContent(
        `Элемент /value отсутствует на стороне «${focusSide === "before" ? "Было" : "Стало"}».`,
      );
      expect(screen.getByLabelText("Было")).not.toHaveFocus();
      expect(screen.getByLabelText("Стало")).not.toHaveFocus();
    },
  );

  it("clears the prior fallback selection when the next pointer is absent on the requested side", () => {
    const view = render(
      <MantineProvider>
        <SourceDiff
          original={'{"value":1}'}
          modified={'{"value":2,"other":3}'}
          narrow={false}
          focusPointer="/value"
          focusSide="before"
        />
      </MantineProvider>,
    );
    const before = screen.getByLabelText<HTMLTextAreaElement>("Было");
    expect(before.selectionStart).not.toBe(before.selectionEnd);
    view.rerender(
      <MantineProvider>
        <SourceDiff
          original={'{"value":1}'}
          modified={'{"value":2,"other":3}'}
          narrow={false}
          focusPointer="/other"
          focusSide="before"
        />
      </MantineProvider>,
    );
    expect(screen.getByRole("status")).toHaveTextContent(
      "Элемент /other отсутствует на стороне «Было».",
    );
    const next = screen.getByLabelText<HTMLTextAreaElement>("Было");
    expect(next).toBe(before);
    expect(next.selectionStart).toBe(next.selectionEnd);
  });
});
