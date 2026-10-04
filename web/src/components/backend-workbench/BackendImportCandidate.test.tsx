import { afterEach, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { json } from "@/test/http";
import {
  exactCandidate,
  exactClaim,
  exactCoverage,
  exactEnvelope,
  exactHash,
  exactIDs,
  exactNode,
  exactSource,
} from "@/test/backendExact";
import { BackendImportCandidate } from "./BackendImportCandidate";

afterEach(() => vi.unstubAllGlobals());
function server(coverage?: () => Response | Promise<Response>) {
  const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname.endsWith("/coverage"))
      return coverage
        ? coverage()
        : json(200, { ...exactEnvelope(), ...exactCoverage, source: exactSource() });
    if (url.pathname.endsWith("/graph/query")) {
      const body = JSON.parse(String(init?.body));
      return json(200, {
        ...exactEnvelope(),
        nodes: body.recordType === "nodes" ? [exactNode()] : [],
        edges: [],
        nextCursor: "",
      });
    }
    if (url.pathname.includes("/nodes/"))
      return json(200, {
        ...exactEnvelope(),
        node: exactNode(),
        origins: [],
        proposalPins: null,
        proposalProjection: null,
      });
    if (url.pathname.endsWith("/assertions"))
      return json(200, {
        ...exactEnvelope(),
        documentVersion: "source-assertions-v1",
        basis: "candidate",
        items: [exactClaim()],
        nextCursor: "",
      });
    if (url.pathname.endsWith("/evidence"))
      return json(200, { ...exactEnvelope(), items: [], nextCursor: "", source: exactSource() });
    return json(500, { error: { message: url.pathname } });
  });
  vi.stubGlobal("fetch", fetcher);
  return fetcher;
}
it("inspects the exact READY candidate with identities, coverage and assertions", async () => {
  const fetcher = server();
  renderWithProviders(
    <BackendImportCandidate projectId={exactIDs.project} target={exactCandidate} />,
  );
  expect(await screen.findByText("Delivery not proven")).toBeInTheDocument();
  expect(screen.getByText(/Подготовленный граф/)).toBeInTheDocument();
  await userEvent.click(await screen.findByRole("button", { name: "Открыть объект Orders" }));
  expect(await screen.findByText("compiler · handler")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /Утверждение reflection/ })).toBeInTheDocument();
  const graphCalls = fetcher.mock.calls.filter(([url]) => String(url).endsWith("/graph/query"));
  expect(graphCalls.length).toBeGreaterThan(0);
  for (const [, init] of graphCalls) {
    const body = JSON.parse(String(init?.body));
    expect(body.importCandidate).toEqual(exactCandidate.importCandidate);
    expect(body.revisionId).toBeUndefined();
  }
  expect(fetcher.mock.calls.every(([url]) => !String(url).includes("/revisions/"))).toBe(true);
});
it("clears selection and ignores late responses when the candidate pin is removed", async () => {
  let finish: ((response: Response) => void) | undefined;
  server(
    () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  );
  const view = renderWithProviders(
    <BackendImportCandidate projectId={exactIDs.project} target={exactCandidate} />,
  );
  await waitFor(() => expect(finish).toBeDefined());
  view.rerender(<BackendImportCandidate projectId={exactIDs.project} target={null} />);
  finish?.(json(200, { ...exactEnvelope(), ...exactCoverage, source: exactSource() }));
  expect(screen.queryByText("Delivery not proven")).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Открыть объект Orders" })).not.toBeInTheDocument();
});
it("explains stale candidate conflict without reading a source revision", async () => {
  const fetcher = server(() =>
    json(409, { error: { code: "backend_conflict", message: "Candidate superseded" } }),
  );
  renderWithProviders(
    <BackendImportCandidate projectId={exactIDs.project} target={exactCandidate} />,
  );
  expect(await screen.findByRole("alert")).toHaveTextContent("Candidate superseded");
  expect(screen.queryByText("Delivery not proven")).not.toBeInTheDocument();
  expect(fetcher.mock.calls.every(([url]) => !String(url).includes("/revisions/"))).toBe(true);
});
it("rejects unsafe candidate versions before making a request", () => {
  const fetcher = server();
  renderWithProviders(
    <BackendImportCandidate
      projectId={exactIDs.project}
      target={{
        importCandidate: {
          importId: exactIDs.project,
          importVersion: Number.MAX_SAFE_INTEGER + 1,
          candidateHash: exactHash,
        },
      }}
    />,
  );
  expect(screen.getByRole("alert")).toBeInTheDocument();
  expect(fetcher).not.toHaveBeenCalled();
});
