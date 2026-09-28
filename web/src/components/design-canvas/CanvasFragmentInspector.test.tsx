import { MantineProvider } from "@mantine/core";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { CanvasFragmentInspector } from "./CanvasFragmentInspector";
import { createFormDraftStore } from "../api-designer/forms/formDraftStore";
import type { CanvasDocument } from "./types";

const document: CanvasDocument = {
  formatVersion: 2,
  title: "Flow",
  participants: [],
  messages: [
    { id: "a", fromId: "x", toId: "y", kind: "note", label: "A", description: "" },
    { id: "b", fromId: "x", toId: "y", kind: "note", label: "B", description: "" },
  ],
  fragments: [
    {
      id: "f",
      kind: "alt",
      label: "Choice",
      fromMessageId: "a",
      toMessageId: "b",
      branches: [
        { id: "first", label: "yes", fromMessageId: "a", toMessageId: "a" },
        { id: "last", label: "else", fromMessageId: "b", toMessageId: "b" },
      ],
    },
  ],
  contracts: [],
};

describe("CanvasFragmentInspector execution settings", () => {
  it("keeps unfinished condition fields visible and applies a string comparison", () => {
    const onChange = vi.fn();
    const formStore = createFormDraftStore();
    render(
      <MantineProvider env="test">
        <CanvasFragmentInspector
          document={document}
          fragment={document.fragments[0]!}
          onChange={onChange}
          formStore={formStore}
        />
      </MantineProvider>,
    );
    fireEvent.change(screen.getByLabelText("Режим ветки 1"), { target: { value: "condition" } });
    fireEvent.change(screen.getByLabelText("Переменная ветки 1"), { target: { value: "token" } });
    expect(screen.getByLabelText("Переменная ветки 1")).toHaveValue("token");
    expect(onChange).not.toHaveBeenCalled();
    expect(formStore.getSnapshot().dirty).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "Применить выполнение ветки 1" }));
    expect(onChange.mock.lastCall?.[0].fragments[0].branches[0].execution).toEqual({
      condition: { variable: "token", operator: "equals", value: "" },
    });
    expect(formStore.getSnapshot().dirty).toBe(false);
  });

  it("restores unfinished input after selection changes and refreshes after an authoritative update", () => {
    const formStore = createFormDraftStore();
    const onChange = vi.fn();
    const renderInspector = (fragment = document.fragments[0]!) => (
      <MantineProvider env="test">
        <CanvasFragmentInspector
          document={{ ...document, fragments: [fragment] }}
          fragment={fragment}
          onChange={onChange}
          formStore={formStore}
        />
      </MantineProvider>
    );
    const view = render(renderInspector());
    fireEvent.change(screen.getByLabelText("Режим ветки 1"), { target: { value: "condition" } });
    fireEvent.change(screen.getByLabelText("Переменная ветки 1"), { target: { value: "token" } });
    view.unmount();
    const reopened = render(renderInspector());
    expect(screen.getByLabelText("Переменная ветки 1")).toHaveValue("token");
    expect(formStore.getSnapshot().dirty).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "Применить выполнение ветки 1" }));
    const applied = onChange.mock.lastCall![0].fragments[0];
    reopened.rerender(renderInspector(applied));
    const updated = {
      ...applied,
      branches: applied.branches.map((branch: { id: string }) =>
        branch.id === "first"
          ? { ...branch, execution: { condition: { variable: "other", operator: "exists" } } }
          : branch,
      ),
    };
    reopened.rerender(renderInspector(updated));
    expect(screen.getByLabelText("Переменная ветки 1")).toHaveValue("other");
  });

  it("resets visible input when an authoritative replacement clears pending drafts", () => {
    const formStore = createFormDraftStore();
    render(
      <MantineProvider env="test">
        <CanvasFragmentInspector
          document={document}
          fragment={document.fragments[0]!}
          onChange={vi.fn()}
          formStore={formStore}
        />
      </MantineProvider>,
    );
    fireEvent.change(screen.getByLabelText("Режим ветки 1"), { target: { value: "condition" } });
    fireEvent.change(screen.getByLabelText("Переменная ветки 1"), { target: { value: "token" } });
    act(() => formStore.clear());
    expect(screen.getByLabelText("Режим ветки 1")).toHaveValue("none");
  });

  it("clears a pending opt condition when changing its kind to alt", () => {
    const formStore = createFormDraftStore();
    const fragment = { ...document.fragments[0]!, kind: "opt" as const, branches: undefined };
    render(
      <MantineProvider env="test">
        <CanvasFragmentInspector
          document={{ ...document, fragments: [fragment] }}
          fragment={fragment}
          onChange={vi.fn()}
          formStore={formStore}
        />
      </MantineProvider>,
    );
    fireEvent.change(screen.getByLabelText("Режим блока"), { target: { value: "condition" } });
    expect(formStore.getSnapshot().dirty).toBe(true);
    fireEvent.change(screen.getByLabelText("Тип блока"), { target: { value: "alt" } });
    expect(formStore.getSnapshot().dirty).toBe(false);
  });

  it("clears only a merged-away branch execution draft", () => {
    const formStore = createFormDraftStore();
    const fragment = {
      ...document.fragments[0]!,
      toMessageId: "c",
      branches: [
        document.fragments[0]!.branches![0]!,
        { id: "middle", label: "middle", fromMessageId: "b", toMessageId: "b" },
        { id: "last", label: "last", fromMessageId: "c", toMessageId: "c" },
      ],
    };
    const updated = {
      ...document,
      messages: [...document.messages, { ...document.messages[1]!, id: "c" }],
      fragments: [fragment],
    };
    const draft = { source: "pending", propertySource: "saved" };
    formStore.set("/canvas-fragment/f/branch/middle/execution", draft);
    formStore.set("/canvas-fragment/f/branch/last/execution", draft);
    render(
      <MantineProvider env="test">
        <CanvasFragmentInspector
          document={updated}
          fragment={fragment}
          onChange={vi.fn()}
          formStore={formStore}
        />
      </MantineProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Объединить ветку 2" }));
    expect(formStore.get("/canvas-fragment/f/branch/middle/execution")).toBeUndefined();
    expect(formStore.get("/canvas-fragment/f/branch/last/execution")).toEqual(draft);
  });
});
