import { afterEach, expect, it, vi } from "vitest";
import { createCardClicks } from "./cardClicks";
afterEach(() => vi.useRealTimers());
it("keeps the card in place until a pointer gesture resolves, entering once without opening the inspector", () => {
  vi.useFakeTimers();
  const select = vi.fn(),
    enter = vi.fn();
  const clicks = createCardClicks(select, enter);
  clicks.click("right-card", 1);
  vi.advanceTimersByTime(300);
  expect(select).not.toHaveBeenCalled();
  clicks.click("right-card", 2);
  clicks.doubleClick("right-card");
  vi.runAllTimers();
  expect(select).not.toHaveBeenCalled();
  expect(enter).toHaveBeenCalledExactlyOnceWith("right-card");
});
it("selects a single pointer click, keeps keyboard activation immediate and cancels on leaving a view", () => {
  vi.useFakeTimers();
  const select = vi.fn(),
    enter = vi.fn();
  const clicks = createCardClicks(select, enter);
  clicks.click("single", 1);
  vi.runAllTimers();
  expect(select).toHaveBeenLastCalledWith("single");
  clicks.click("keyboard", 0);
  expect(select).toHaveBeenLastCalledWith("keyboard");
  clicks.click("departed", 1);
  clicks.cancel();
  vi.runAllTimers();
  expect(select).toHaveBeenCalledTimes(2);
  expect(enter).not.toHaveBeenCalled();
});
