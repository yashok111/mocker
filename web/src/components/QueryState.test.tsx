import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryState, type QueryStateQuery } from "./QueryState";
import { renderWithProviders } from "@/test/render";
import { ApiFailure } from "@/api/client";

// The four states are asserted against a hand-built query-result shape rather
// than a real useQuery: this component reads exactly four fields, and driving
// a real query through pending → error → success would test TanStack's state
// machine instead of this ladder's markup and its two data-testids, which are
// what fifteen screens' tests depend on.
function q(over: Partial<QueryStateQuery> = {}): QueryStateQuery {
  return { isPending: false, isError: false, error: null, refetch: vi.fn(), ...over };
}

describe("QueryState", () => {
  it("shows the loading sentence in a live region while any query is pending", () => {
    renderWithProviders(
      <QueryState queries={[q(), q({ isPending: true })]} testIdPrefix="things">
        <div data-testid="things-content" />
      </QueryState>,
    );

    expect(screen.getByText("Загрузка…")).toBeInTheDocument();
    expect(screen.queryByTestId("things-content")).not.toBeInTheDocument();
    expect(screen.queryByTestId("things-error")).not.toBeInTheDocument();
  });

  it("describes the FIRST failed query and offers «Повторить»", () => {
    renderWithProviders(
      <QueryState
        queries={[
          q({ isError: true, error: new ApiFailure("no such workspace", 404, "not_found") }),
          q({ isError: true, error: new ApiFailure("boom", 500, "internal") }),
        ]}
        testIdPrefix="things"
      >
        <div data-testid="things-content" />
      </QueryState>,
    );

    expect(screen.getByTestId("things-error")).toHaveTextContent("Не найдено");
    expect(screen.getByTestId("things-retry")).toHaveTextContent("Повторить");
    expect(screen.queryByTestId("things-content")).not.toBeInTheDocument();
  });

  it("pending wins over failed, so a half-loaded screen shows one state", () => {
    renderWithProviders(
      <QueryState
        queries={[
          q({ isError: true, error: new ApiFailure("boom", 500, "internal") }),
          q({ isPending: true }),
        ]}
        testIdPrefix="things"
      >
        <div data-testid="things-content" />
      </QueryState>,
    );

    expect(screen.getByText("Загрузка…")).toBeInTheDocument();
    expect(screen.queryByTestId("things-error")).not.toBeInTheDocument();
  });

  it("refetches EVERY query on retry when the screen gives no onRetry", async () => {
    const first = vi.fn();
    const second = vi.fn();
    renderWithProviders(
      <QueryState
        queries={[
          q({ isError: true, error: new ApiFailure("boom", 500, "internal"), refetch: first }),
          q({ refetch: second }),
        ]}
        testIdPrefix="things"
      >
        <div data-testid="things-content" />
      </QueryState>,
    );

    await userEvent.click(screen.getByTestId("things-retry"));

    expect(first).toHaveBeenCalledTimes(1);
    expect(second).toHaveBeenCalledTimes(1);
  });

  it("calls onRetry INSTEAD of refetching when the screen supplies one", async () => {
    const refetch = vi.fn();
    const onRetry = vi.fn();
    renderWithProviders(
      <QueryState
        queries={[q({ isError: true, error: new ApiFailure("boom", 500, "internal"), refetch })]}
        testIdPrefix="things"
        onRetry={onRetry}
      >
        <div data-testid="things-content" />
      </QueryState>,
    );

    await userEvent.click(screen.getByTestId("things-retry"));

    expect(onRetry).toHaveBeenCalledTimes(1);
    expect(refetch).not.toHaveBeenCalled();
  });

  it("renders the screen's own children once every query has answered", () => {
    renderWithProviders(
      <QueryState queries={[q(), q()]} testIdPrefix="things">
        <div data-testid="things-content" />
      </QueryState>,
    );

    expect(screen.getByTestId("things-content")).toBeInTheDocument();
    expect(screen.queryByText("Загрузка…")).not.toBeInTheDocument();
    expect(screen.queryByTestId("things-error")).not.toBeInTheDocument();
  });
});
