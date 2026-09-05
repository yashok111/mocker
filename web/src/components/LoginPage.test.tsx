import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { fill } from "@/test/user";
import { LoginPage } from "./LoginPage";
import { renderWithProviders } from "@/test/render";
import { authResponseFixture } from "@/test/fixtures";

type FetchArgs = [input: RequestInfo | URL, init?: RequestInit];

function stubFetch(...responses: Response[]) {
  const queue = [...responses];
  const fn = vi.fn<(...args: FetchArgs) => Promise<Response>>(() =>
    Promise.resolve(queue.shift() ?? new Response(null, { status: 500 })),
  );
  vi.stubGlobal("fetch", fn);
  return fn;
}

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function apiError(status: number, code: string, message = "x"): Response {
  return json(status, { error: { code, message } });
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("LoginPage", () => {
  it("refuses to send an empty name and never reaches the network", async () => {
    const fetchMock = stubFetch();
    renderWithProviders(<LoginPage onSuccess={vi.fn()} />);

    await userEvent.click(screen.getByTestId("login-submit"));

    expect(await screen.findByText("Введите имя")).toBeInTheDocument();
    // Client-side validation exists to save the round trip, so making one
    // anyway would defeat its only purpose.
    expect(fetchMock).not.toHaveBeenCalled();
    // The message belongs to the one field that failed. arktype's own
    // issue.message prefixes the path ("name Введите имя") and the resolver
    // strips it — a regression there would also be visible as the wrong
    // field turning red.
    expect(screen.getByTestId("login-password")).not.toHaveAttribute("data-error");
  });

  it("trims the name before sending it, matching the server's own rule", async () => {
    const fetchMock = stubFetch(json(200, authResponseFixture()));
    renderWithProviders(<LoginPage onSuccess={vi.fn()} />);

    await userEvent.type(screen.getByTestId("login-name"), "  alex  ");
    await userEvent.type(screen.getByTestId("login-password"), "hunter2");
    await userEvent.click(screen.getByTestId("login-submit"));

    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    const init = fetchMock.mock.calls[0]?.[1];
    expect(JSON.parse(String(init?.body))).toEqual({ name: "alex", password: "hunter2" });
  });

  it("reports a wrong password and a wrong name identically", async () => {
    stubFetch(apiError(401, "unauthorized", "invalid credentials"));
    renderWithProviders(<LoginPage onSuccess={vi.fn()} />);

    await userEvent.type(screen.getByTestId("login-name"), "alex");
    await userEvent.type(screen.getByTestId("login-password"), "wrong");
    await userEvent.click(screen.getByTestId("login-submit"));

    // The server answers the SAME 401 for both on purpose; a message that told
    // them apart would leak which half was right.
    expect(await screen.findByRole("alert")).toHaveTextContent("Неверный пароль или имя");
  });

  it("names the rate limit rather than showing a generic failure", async () => {
    stubFetch(apiError(429, "rate_limited", "too many attempts"));
    renderWithProviders(<LoginPage onSuccess={vi.fn()} />);

    await userEvent.type(screen.getByTestId("login-name"), "alex");
    await userEvent.type(screen.getByTestId("login-password"), "hunter2");
    await userEvent.click(screen.getByTestId("login-submit"));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Слишком много попыток. Подождите минуту",
    );
  });

  it("reports an unreachable server without a status it does not have", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => Promise.reject(new TypeError("Failed to fetch"))),
    );
    renderWithProviders(<LoginPage onSuccess={vi.fn()} />);

    await userEvent.type(screen.getByTestId("login-name"), "alex");
    await userEvent.type(screen.getByTestId("login-password"), "hunter2");
    await userEvent.click(screen.getByTestId("login-submit"));

    expect(await screen.findByRole("alert")).toHaveTextContent("Сервер не ответил");
  });

  it("calls onSuccess only after a 200", async () => {
    const onSuccess = vi.fn();
    stubFetch(apiError(401, "unauthorized"), json(200, authResponseFixture()));
    renderWithProviders(<LoginPage onSuccess={onSuccess} />);

    await userEvent.type(screen.getByTestId("login-name"), "alex");
    await userEvent.type(screen.getByTestId("login-password"), "wrong");
    await userEvent.click(screen.getByTestId("login-submit"));
    await screen.findByRole("alert");
    expect(onSuccess).not.toHaveBeenCalled();

    await userEvent.click(screen.getByTestId("login-submit"));
    await waitFor(() => expect(onSuccess).toHaveBeenCalledTimes(1));
  });

  it("rejects a name longer than the server would accept, in runes", async () => {
    const fetchMock = stubFetch();
    renderWithProviders(<LoginPage onSuccess={vi.fn()} />);

    // 65 emoji is 65 runes — over the cap — but 130 UTF-16 units, so a length
    // check would have flagged it far earlier and for the wrong reason.
    await fill(screen.getByTestId("login-name"), "🙂".repeat(65));
    await userEvent.type(screen.getByTestId("login-password"), "hunter2");
    await userEvent.click(screen.getByTestId("login-submit"));

    expect(await screen.findByText(/Имя слишком длинное/)).toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("says what the name and the password are (A21, U10)", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => new Promise<Response>(() => {})),
    );
    renderWithProviders(<LoginPage onSuccess={vi.fn()} />);
    expect(await screen.findByTestId("login-hint")).toHaveTextContent(
      "Пароль один на всю установку",
    );
  });
});
