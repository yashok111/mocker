import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import MonacoDiff from "./MonacoDiff";

const state = vi.hoisted(() => ({
  events: [] as string[],
  modelCount: 0,
  options: null as Record<string, unknown> | null,
  focused: [] as string[],
  reset: [] as string[],
}));

vi.mock("./setup", () => ({
  monaco: {
    editor: {
      createModel: vi.fn().mockImplementation((value: string) => {
        const name = state.modelCount % 2 === 0 ? "original-model" : "modified-model";
        state.modelCount += 1;
        return {
          dispose: () => state.events.push(name),
          getValue: () => value,
          setValue: vi.fn(),
        };
      }),
      createDiffEditor: vi.fn((_container: HTMLElement, options: Record<string, unknown>) => {
        state.options = options;
        return {
          dispose: () => state.events.push("diff-editor"),
          getModifiedEditor: () => ({
            getModel: () => ({
              getPositionAt: (offset: number) => ({ lineNumber: 1, column: offset + 1 }),
            }),
            revealRangeInCenter: () => state.focused.push("after"),
            setSelection: vi.fn(),
            setPosition: () => state.reset.push("after"),
          }),
          getOriginalEditor: () => ({
            getModel: () => ({
              getPositionAt: (offset: number) => ({ lineNumber: 1, column: offset + 1 }),
            }),
            revealRangeInCenter: () => state.focused.push("before"),
            setSelection: vi.fn(),
            setPosition: () => state.reset.push("before"),
          }),
          setModel: (model: unknown) => state.events.push(model === null ? "detach" : "attach"),
          updateOptions: vi.fn(),
        };
      }),
    },
  },
}));

describe("MonacoDiff lifecycle", () => {
  beforeEach(() => {
    state.events = [];
    state.modelCount = 0;
    state.options = null;
    state.focused = [];
    state.reset = [];
  });

  it("detaches the widget before disposing it and its models", () => {
    const view = render(<MonacoDiff original="{}" modified="{}" narrow={false} />);

    expect(state.events).toEqual(["attach"]);
    view.unmount();

    expect(state.events).toEqual([
      "attach",
      "detach",
      "diff-editor",
      "original-model",
      "modified-model",
    ]);
  });

  it("uses inline diff below the workbench canvas width", () => {
    const view = render(<MonacoDiff original="{}" modified="{}" narrow={false} />);

    expect(state.options).toMatchObject({
      renderSideBySideInlineBreakpoint: 700,
      hideUnchangedRegions: { enabled: true },
    });
    view.unmount();
  });

  it("honors the explicit before side and preserves modified-first default", () => {
    const view = render(
      <MonacoDiff
        original={'{"value":1}'}
        modified={'{"value":2}'}
        narrow={false}
        focusPointer="/value"
        focusSide="before"
      />,
    );
    expect(state.focused).toEqual(["before"]);
    view.rerender(
      <MonacoDiff
        original={'{"value":1}'}
        modified={'{"value":2}'}
        narrow={false}
        focusPointer="/value"
      />,
    );
    expect(state.focused).toEqual(["before", "after"]);
  });

  it.each(["before", "after"] as const)(
    "reports an absent pointer on %s without revealing the opposite editor",
    (focusSide) => {
      render(
        <MonacoDiff
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
      expect(state.focused).toEqual([]);
    },
  );

  it("clears earlier selections when the requested next pointer is missing", () => {
    const view = render(
      <MonacoDiff
        original={'{"value":1}'}
        modified={'{"value":2,"other":3}'}
        narrow={false}
        focusPointer="/value"
        focusSide="before"
      />,
    );
    expect(state.focused).toEqual(["before"]);
    view.rerender(
      <MonacoDiff
        original={'{"value":1}'}
        modified={'{"value":2,"other":3}'}
        narrow={false}
        focusPointer="/other"
        focusSide="before"
      />,
    );
    expect(state.focused).toEqual(["before"]);
    expect(state.reset).toEqual(["before", "after"]);
    expect(screen.getByRole("status")).toHaveTextContent(
      "Элемент /other отсутствует на стороне «Было».",
    );
  });
});
