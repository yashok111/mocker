import { afterEach, expect, it, vi } from "vitest";
import { useState } from "react";
import { cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { analysisReadyReason } from "./backendAnalysisReads";
import { BackendImpactReport } from "./BackendImpactReport";
import { analysisTestDetail, analysisTestManifest } from "./backendAnalysisTestFixtures";
import { changeTestDetail, changeTestID } from "./backendChangeTestFixtures";
afterEach(() => {
  cleanup();
  sessionStorage.clear();
  vi.unstubAllGlobals();
});
it("accepts incompatible static verdict with complete typed coverage, but rejects stale, buffer, partial and typed address mismatch", () => {
  const d = analysisTestDetail(),
    m = analysisTestManifest(),
    p = changeTestDetail();
  expect(analysisReadyReason(d, m, p)).toBe("");
  expect(analysisReadyReason(d, m, p, true)).toMatch(/несохран/);
  expect(analysisReadyReason(d, { ...m, resultVersion: 1 }, p)).toMatch(/финальная/);
  expect(analysisReadyReason(d, { ...m, complete: false }, p)).toMatch(/Неполный/);
  expect(
    analysisReadyReason(
      d,
      {
        ...m,
        changedIds: [{ recordType: "node", id: "same" }],
        coveredChangedIds: [{ recordType: "edge", id: "same" }],
      },
      p,
    ),
  ).toMatch(/часть/);
  expect(
    analysisReadyReason(
      { ...d, input: { ...d.input, target: { revisionId: changeTestID } } },
      m,
      p,
    ),
  ).toMatch(/устарел/);
  expect(analysisReadyReason({ ...d, job: { ...d.job, status: "cancelled" } }, m, p)).toMatch(
    /финальная/,
  );
});
it("keeps exact result pin through section, filter and pagination and resets cursor on selection changes", async () => {
  const requests: URL[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input), "http://localhost");
      requests.push(url);
      return new Response(
        JSON.stringify({
          manifest: {
            ...analysisTestManifest(),
            resultVersion: Number(url.searchParams.get("resultVersion")),
          },
          section: url.searchParams.get("section"),
          items: [],
          nextCursor: url.searchParams.get("cursor") ? "" : "next",
        }),
        { status: 200 },
      );
    }),
  );
  const user = userEvent.setup(),
    detail = analysisTestDetail();
  function Harness() {
    const [version, setVersion] = useState(1);
    return (
      <>
        <button onClick={() => setVersion(2)}>Next version</button>
        <BackendImpactReport projectId={changeTestID} detail={detail} resultVersion={version} />
      </>
    );
  }
  renderWithProviders(<Harness />);
  await screen.findByText("Обход завершён · вывод: incompatible");
  await user.selectOptions(screen.getByLabelText("Раздел отчёта"), "witnesses");
  await waitFor(() => expect(requests.at(-1)?.searchParams.get("section")).toBe("witnesses"));
  await user.selectOptions(screen.getByLabelText("Достоверность"), "unknown");
  await waitFor(() => expect(requests.at(-1)?.searchParams.get("certainty")).toBe("unknown"));
  const next = screen.getByRole("button", { name: /Следующие.*отчёта/ });
  await user.click(next);
  await waitFor(() => expect(requests.at(-1)?.searchParams.get("cursor")).toBe("next"));
  expect(requests.at(-1)?.searchParams.get("resultVersion")).toBe("1");
  await user.click(screen.getByText("Next version"));
  await waitFor(() => expect(requests.at(-1)?.searchParams.get("resultVersion")).toBe("2"));
  expect(requests.at(-1)?.searchParams.get("cursor") ?? "").toBe("");
});
it("ready persists exact report and gap acknowledgments, preserves draft ID and updates association", async () => {
  const detail = analysisTestDetail(),
    manifest = {
      ...analysisTestManifest(),
      gaps: [{ id: "gap1", code: "partial_source", message: "Static source gap", objects: [] }],
    },
    proposal = changeTestDetail();
  const saved = vi.fn();
  let sent: Record<string, unknown> | undefined;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === "POST") {
        sent = JSON.parse(String(init.body));
        return new Response(
          JSON.stringify({
            proposal: {
              ...proposal.proposal,
              status: "ready",
              version: 2,
              readyReference: {
                report: sent!.report,
                acknowledgedGapIds: sent!.acknowledgedGapIds,
              },
            },
            revision: proposal.revision,
            semanticHash: proposal.revision.semanticHash,
            changes: [],
          }),
          { status: 200 },
        );
      }
      return new Response(
        JSON.stringify({ manifest, section: "findings", items: [], nextCursor: "" }),
        { status: 200 },
      );
    }),
  );
  renderWithProviders(
    <BackendImpactReport
      projectId={changeTestID}
      detail={detail}
      resultVersion={2}
      proposal={proposal}
      onSaved={saved}
    />,
  );
  const user = userEvent.setup();
  const button = await screen.findByRole("button", { name: "Отметить черновик готовым" });
  expect(button).toBeDisabled();
  await user.click(screen.getByLabelText(/Static source gap/));
  expect(button).toBeEnabled();
  await user.click(button);
  await waitFor(() => expect(saved).toHaveBeenCalledOnce());
  expect(saved.mock.calls[0]![0].revision.id).toBe(proposal.revision.id);
  expect(saved.mock.calls[0]![0].proposal.version).toBe(2);
  expect(sent).toMatchObject({
    proposalRevisionId: proposal.revision.id,
    expectedVersion: 1,
    report: {
      jobId: detail.job.id,
      resultVersion: 2,
      inputHash: manifest.analysisInputHash,
      resultHash: manifest.semanticResultHash,
    },
    acknowledgedGapIds: ["gap1"],
  });
});

it("opens a deleted dependency on its exact before revision and retains witness context", async () => {
  const detail = analysisTestDetail();
  detail.input.beforePins.viewSchemaVersion = "5";
  detail.input.beforePins.structuralSchemaVersion = "5";
  const requests: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const u = new URL(String(input), "http://localhost");
      requests.push(u.pathname);
      if (u.pathname.endsWith("/results"))
        return new Response(
          JSON.stringify({
            manifest: analysisTestManifest(),
            section: u.searchParams.get("section"),
            nextCursor: "",
            items:
              u.searchParams.get("section") === "witnesses"
                ? [
                    {
                      id: "b".repeat(64),
                      service: "orders",
                      kind: "calls",
                      certainty: "confirmed",
                      direction: "downstream",
                      depth: 1,
                      object: { recordType: "node", id: changeTestID },
                      detail: {
                        side: "before",
                        seed: { recordType: "node", id: changeTestID },
                        affected: { recordType: "node", id: changeTestID },
                        steps: [
                          {
                            kind: "calls",
                            id: "edge1",
                            from: { recordType: "node", id: changeTestID },
                            to: { recordType: "node", id: changeTestID },
                            evidence: [],
                          },
                        ],
                        status: "confirmed",
                      },
                    },
                  ]
                : [],
          }),
          { status: 200 },
        );
      if (u.pathname.endsWith(`/nodes/${changeTestID}`))
        return new Response(
          JSON.stringify({
            id: changeTestID,
            kind: "handler",
            name: "Deleted dependency",
            parentId: null,
            attributes: {},
            evidenceIds: [],
            externalKey: "handler",
            freshness: "fresh",
            ownership: "source",
          }),
          { status: 200 },
        );
      return new Response(JSON.stringify({ error: { code: "not_found", message: "not used" } }), {
        status: 404,
      });
    }),
  );
  renderWithProviders(
    <BackendImpactReport projectId={changeTestID} detail={detail} resultVersion={2} />,
  );
  const user = userEvent.setup();
  await user.selectOptions(screen.getByLabelText("Раздел отчёта"), "witnesses");
  await screen.findByText("Путь влияния: before · confirmed");
  await user.click(screen.getAllByRole("button", { name: "Осмотреть node · before" })[0]!);
  await screen.findByText("Deleted dependency");
  expect(requests).toContain(
    `/api/backend-projects/${changeTestID}/revisions/${detail.input.fromRevisionId}/nodes/${changeTestID}`,
  );
  expect(requests.some((path) => path.includes("change-proposals"))).toBe(false);
});

it.each(["node", "edge"] as const)(
  "keeps opaque source-claim %s addresses readable without sending them to graph inspection",
  async (recordType) => {
    const detail = analysisTestDetail();
    detail.input.beforePins.viewSchemaVersion = "5";
    detail.input.beforePins.structuralSchemaVersion = "5";
    const pseudo = `assertion/${changeTestID}/0197aaf9-5555-7000-8000-000000000003/b42-static`;
    const requests: { path: string; body: string }[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = new URL(String(input), "http://localhost");
        requests.push({ path: url.pathname, body: String(init?.body ?? "") });
        if (url.pathname.endsWith("/results"))
          return new Response(
            JSON.stringify({
              manifest: analysisTestManifest(),
              section: url.searchParams.get("section"),
              nextCursor: "",
              items: [
                {
                  id: "c".repeat(64),
                  service: "orders",
                  kind: "source_claim",
                  certainty: "confirmed",
                  direction: "both",
                  depth: 0,
                  object: { recordType, id: pseudo },
                  detail: {
                    object: { recordType, id: pseudo },
                    facet: "source_claim",
                    operation: "modified",
                    kind: "source_claim",
                    paths: ["externalKey"],
                    before: { providerNamespace: "b42-static", externalKey: "base-system" },
                    after: { providerNamespace: "b42-static", externalKey: "next-system" },
                  },
                },
              ],
            }),
            { status: 200 },
          );
        return new Response(
          JSON.stringify({
            error: { code: "not_found", message: "No graph object exists at an assertion address" },
          }),
          { status: 404 },
        );
      }),
    );
    renderWithProviders(
      <BackendImpactReport projectId={changeTestID} detail={detail} resultVersion={2} />,
    );
    const user = userEvent.setup();
    await user.selectOptions(screen.getByLabelText("Раздел отчёта"), "changes");
    await screen.findByText(new RegExp(`^${recordType} assertion/`));
    await user.click(screen.getByText("До"));
    expect(screen.getByText(/base-system/)).toBeInTheDocument();
    const inspect = screen.queryByRole("button", { name: `Осмотреть ${recordType} · before` });
    if (inspect) await user.click(inspect);
    expect(
      requests.filter((request) => request.path.includes(pseudo) || request.body.includes(pseudo)),
    ).toEqual([]);
    expect(screen.queryByRole("button", { name: `Осмотреть ${recordType} · before` })).toBeNull();
    expect(screen.queryByRole("button", { name: `Осмотреть ${recordType} · after` })).toBeNull();
  },
);
