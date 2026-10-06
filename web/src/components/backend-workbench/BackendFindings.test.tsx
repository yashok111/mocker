import { afterEach, expect, it, vi } from "vitest";
import { cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { BackendFindings } from "./BackendFindings";
import { changeTestID, changeHash } from "./backendChangeTestFixtures";
afterEach(() => {
  cleanup();
  sessionStorage.clear();
  vi.unstubAllGlobals();
});
const item = {
  finding: {
    fingerprint: changeHash,
    basisHash: changeHash,
    rule: "unused_table",
    ruleVersion: "1",
    severity: "review",
    certainty: "unknown",
    subjects: [],
    evidenceIds: [],
    prerequisites: ["complete_current_access_inventory"],
    gaps: ["partial_source"],
    message: "Coverage incomplete",
    targetHash: changeHash,
  },
  analysis: { jobId: changeTestID, resultVersion: 7 },
  review: {
    fingerprint: changeHash,
    version: 2,
    basisHash: changeHash,
    status: "open",
    history: [
      {
        version: 2,
        basisHash: changeHash,
        status: "open",
        author: "diagnostics",
        at: "2026-10-06",
        reason: "New basis",
      },
    ],
  },
};
it("pins result version and replays the same review after uncertain failure", async () => {
  const bodies: string[] = [];
  const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    if (init?.method === "PUT") {
      bodies.push(String(init.body));
      if (bodies.length === 1) throw new TypeError("lost reply");
      return new Response(JSON.stringify(item.review), { status: 200 });
    }
    expect(url.searchParams.get("resultVersion")).toBe("7");
    return new Response(JSON.stringify({ items: [item], nextCursor: "" }), { status: 200 });
  });
  vi.stubGlobal("fetch", fetch);
  renderWithProviders(
    <BackendFindings
      projectId={changeTestID}
      jobId={changeTestID}
      resultVersion={7}
      target={{ revisionId: changeTestID }}
    />,
  );
  expect(await screen.findByText("Coverage incomplete")).toBeInTheDocument();
  expect(screen.getByText(/partial_source/)).toBeInTheDocument();
  const user = userEvent.setup();
  await user.type(screen.getByLabelText("Причина решения"), "Human review");
  await user.click(screen.getByRole("button", { name: "Сохранить review" }));
  await user.click(await screen.findByRole("button", { name: "Повторить тот же review" }));
  await waitFor(() => expect(bodies).toHaveLength(2));
  expect(bodies[1]).toBe(bodies[0]);
  expect(JSON.parse(bodies[0]!)).toMatchObject({
    expectedVersion: 2,
    basisHash: changeHash,
    status: "accepted_risk",
  });
});
it("keeps historical basis read-only and shows review history", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(
          JSON.stringify({
            items: [{ ...item, review: { ...item.review, basisHash: "b".repeat(64) } }],
            nextCursor: "",
          }),
          { status: 200 },
        ),
    ),
  );
  renderWithProviders(
    <BackendFindings
      projectId={changeTestID}
      jobId={changeTestID}
      resultVersion={7}
      target={{ revisionId: changeTestID }}
    />,
  );
  expect(await screen.findByText(/Эта occurrence историческая/)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Сохранить review" })).toBeDisabled();
  expect(screen.getByText(/История решений/)).toBeInTheDocument();
});
