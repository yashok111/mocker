import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { AppShell } from "./AppShell";
import { renderInRouter } from "@/test/render";
import { userFixture } from "@/test/fixtures";
import { json, route } from "@/test/http";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

// A20: the header's server status over the two probes. The three words
// and the one trap — after a failed refetch TanStack Query keeps the last
// good answer in `data`, so the word must come from the error state, not
// from stale data.
describe("AppShell server status", () => {
  it("says «готов» when /readyz answers ok", async () => {
    route({
      "GET /readyz": () => json(200, { ok: true }),
      "GET /healthz": () => json(200, { ok: true }),
    });
    renderInRouter(<AppShell user={userFixture()}>x</AppShell>);
    await waitFor(() =>
      expect(screen.getByTestId("server-status")).toHaveTextContent("сервер: готов"),
    );
  });

  it("names the database when /readyz is a 503 and /healthz still answers", async () => {
    route({
      "GET /readyz": () =>
        json(503, { error: { code: "internal", message: "database not ready" } }),
      "GET /healthz": () => json(200, { ok: true }),
    });
    renderInRouter(<AppShell user={userFixture()}>x</AppShell>);
    await waitFor(() =>
      expect(screen.getByTestId("server-status")).toHaveTextContent(
        "сервер: жив, база данных не готова",
      ),
    );
  });

  it("says «недоступен» on any other failure, including after a good answer went stale", async () => {
    let calls = 0;
    route({
      "GET /readyz": () => {
        calls += 1;
        return calls === 1
          ? json(200, { ok: true })
          : json(502, { error: { code: "internal", message: "bad gateway" } });
      },
      "GET /healthz": () => json(502, { error: { code: "internal", message: "bad gateway" } }),
    });
    const { queryClient } = renderInRouter(<AppShell user={userFixture()}>x</AppShell>);
    await waitFor(() =>
      expect(screen.getByTestId("server-status")).toHaveTextContent("сервер: готов"),
    );
    await queryClient.invalidateQueries();
    await waitFor(() =>
      expect(screen.getByTestId("server-status")).toHaveTextContent("сервер: недоступен"),
    );
  });
});
