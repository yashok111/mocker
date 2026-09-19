import { render } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import MonacoDiff from "./MonacoDiff";

const state = vi.hoisted(() => ({
  events: [] as string[],
  modelCount: 0,
  options: null as Record<string, unknown> | null,
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
          getModifiedEditor: vi.fn(),
          getOriginalEditor: vi.fn(),
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
});
