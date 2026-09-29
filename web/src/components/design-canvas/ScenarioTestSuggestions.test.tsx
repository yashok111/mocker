import { MantineProvider } from "@mantine/core";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ScenarioTestSuggestions, type TestSuggestions } from "./ScenarioTestSuggestions";
import type { CanvasDocument } from "./types";

const document: CanvasDocument = {
  formatVersion: 1,
  title: "Заказ",
  participants: [],
  messages: [],
  contracts: [],
  fragments: [
    { id: "notify", kind: "opt", label: "Уведомление", fromMessageId: "a", toMessageId: "a" },
  ],
};
const result: TestSuggestions = {
  revisionId: 42,
  runCount: 0,
  sampleLimit: 50,
  checkedCandidates: 2,
  truncated: false,
  unresolved: [],
  cases: [
    {
      id: "branch-test-1",
      name: "Без уведомления",
      variables: { notify: "no" },
      targets: [{ fragmentId: "notify", outcome: "skipped" }],
    },
  ],
};
function setup(suggest = vi.fn().mockResolvedValue(result)) {
  const props = {
    document,
    revisionId: 42,
    refreshToken: 0,
    disabled: false,
    suggest,
    onRun: vi.fn(),
  };
  const ui = render(<ScenarioTestSuggestions {...props} />, {
    wrapper: ({ children }) => <MantineProvider env="test">{children}</MantineProvider>,
  });
  return { ...ui, props };
}
describe("ScenarioTestSuggestions", () => {
  it("previews uncovered targets and passes exact variables to an explicit run", async () => {
    const { props } = setup();
    expect(props.suggest).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("button", { name: "Подобрать тесты" })).not.toBeInTheDocument();
    expect(await screen.findByText("Без уведомления")).toBeInTheDocument();
    expect(screen.getByText(/Уведомление.*пропустить/)).toBeInTheDocument();
    expect(props.onRun).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Запустить вариант" }));
    expect(props.onRun).toHaveBeenCalledWith(result.cases[0]);
  });
  it("does not refetch on unrelated rerenders and aborts on leaving the tab", async () => {
    const suggest = vi.fn().mockImplementation(() => new Promise<TestSuggestions>(() => {}));
    const { props, rerender, unmount } = setup(suggest);
    expect(screen.getByRole("status")).toHaveTextContent("Подбираем тесты…");
    const signal = suggest.mock.calls[0]![1] as AbortSignal;
    rerender(<ScenarioTestSuggestions {...props} suggest={(...args) => suggest(...args)} />);
    expect(suggest).toHaveBeenCalledTimes(1);
    unmount();
    expect(signal.aborted).toBe(true);
  });
  it("ignores a late result after the revision changes", async () => {
    let resolve!: (value: TestSuggestions) => void;
    const suggest = vi.fn().mockImplementation(
      () =>
        new Promise<TestSuggestions>((r) => {
          resolve = r;
        }),
    );
    const { props, rerender } = setup(suggest);
    const resolveOld = resolve;
    const signal = suggest.mock.calls[0]![1] as AbortSignal;
    rerender(<ScenarioTestSuggestions {...props} revisionId={43} />);
    await act(async () => resolveOld(result));
    expect(signal.aborted).toBe(true);
    expect(screen.queryByText("Без уведомления")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Запустить вариант" })).not.toBeInTheDocument();
  });
  it("clears suggestions when inputs are unsaved or coverage refreshes", async () => {
    const { props, rerender } = setup();
    await screen.findByText("Без уведомления");
    rerender(<ScenarioTestSuggestions {...props} disabled />);
    expect(props.suggest).toHaveBeenCalledTimes(1);
    expect(screen.queryByText("Без уведомления")).not.toBeInTheDocument();
    rerender(<ScenarioTestSuggestions {...props} refreshToken={1} />);
    await screen.findByText("Без уведомления");
    expect(props.suggest).toHaveBeenCalledTimes(2);
    rerender(<ScenarioTestSuggestions {...props} refreshToken={2} />);
    await screen.findByText("Без уведомления");
    expect(props.suggest).toHaveBeenCalledTimes(3);
  });
  it("shows generation errors and unresolved reasons without a fake run", async () => {
    const suggest = vi
      .fn()
      .mockRejectedValueOnce(new Error("Завершите формы"))
      .mockResolvedValueOnce({
        ...result,
        cases: [],
        truncated: true,
        unresolved: [
          {
            target: result.cases[0]!.targets[0],
            code: "search_limit",
            reason: "Достигнут предел подбора.",
          },
        ],
      });
    setup(suggest);
    await screen.findByText("Завершите формы");
    fireEvent.click(screen.getByRole("button", { name: "Повторить" }));
    await screen.findByText("Достигнут предел подбора.");
    expect(screen.queryByRole("button", { name: "Запустить вариант" })).not.toBeInTheDocument();
  });
  it("rejects a server response for another revision", async () => {
    setup(vi.fn().mockResolvedValue({ ...result, revisionId: 41 }));
    await screen.findByText(/другой ревизии/);
    expect(screen.queryByText("Без уведомления")).not.toBeInTheDocument();
  });
  it("explains when every branch has already been observed", async () => {
    setup(vi.fn().mockResolvedValue({ ...result, cases: [] }));
    await waitFor(() => expect(screen.getByText(/Все ветки уже наблюдались/)).toBeInTheDocument());
  });
});
