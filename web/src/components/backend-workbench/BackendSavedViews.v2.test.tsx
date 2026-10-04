import { afterEach, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { json } from "@/test/http";
import { exactCandidate, exactEnvelope, exactHash, exactIDs } from "@/test/backendExact";
import type {
  BackendReadTarget,
  BackendSavedViewResponse,
  BackendSavedViewState,
  BackendSavedViewV2,
} from "@/api/generated/schemas";
import { BackendSavedViews } from "./BackendSavedViews";
import { useBackendSavedViewSession } from "./useBackendSavedViewSession";
afterEach(() => vi.unstubAllGlobals());
const target = {
  changeProposal: { proposalId: exactIDs.project, proposalRevisionId: exactIDs.snapshot },
};
const state: BackendSavedViewState = {
  kind: "flow",
  scope: {},
  filters: { search: "", accessKind: "", reverseAccessKind: "" },
  selection: null,
  positions: [],
  collapsedGroupIds: [],
};
const saved: BackendSavedViewV2 = {
  id: exactIDs.node,
  projectId: exactIDs.project,
  name: "Exact",
  version: 3,
  documentVersion: "saved-view-v2",
  target,
  pins: {
    revisionId: exactIDs.revision,
    semanticHash: exactHash,
    proposal: null,
    effective: exactEnvelope(target).pins,
  },
  state,
  createdAt: "2026-10-03",
  updatedAt: "2026-10-03",
};
function Host({
  initial,
  captureTarget = target,
  version,
}: {
  initial?: BackendSavedViewResponse;
  captureTarget?: BackendReadTarget;
  version?: "saved-view-v2";
}) {
  const session = useBackendSavedViewSession(exactIDs.project, initial);
  return (
    <>
      <BackendSavedViews projectId={exactIDs.project} session={session} onOpen={vi.fn()} />
      <button
        onClick={() => {
          session.capture(captureTarget, state, version);
          session.setName("Exact");
        }}
      >
        Capture
      </button>
      <output aria-label="Точный источник">{JSON.stringify(session.target)}</output>
      <output aria-label="Сохранённая версия">{session.saved?.version}</output>
    </>
  );
}
function server(
  reply: (body: Record<string, unknown>, call: number) => Response | Promise<Response>,
) {
  const bodies: string[] = [];
  const fetcher = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
    if (init?.method !== "POST") return json(200, { items: [], nextCursor: "" });
    bodies.push(String(init.body));
    return reply(JSON.parse(String(init.body)), bodies.length);
  });
  vi.stubGlobal("fetch", fetcher);
  return bodies;
}
it("creates full saved views with explicit v2 and preserves exact bytes after a lost response", async () => {
  const bodies = server((_body, call) => {
    if (call === 1) throw new TypeError("lost response");
    return json(200, saved);
  });
  renderWithProviders(<Host />);
  await userEvent.click(screen.getByRole("button", { name: "Capture" }));
  await userEvent.click(screen.getByRole("button", { name: "Сохранить" }));
  await userEvent.click(await screen.findByRole("button", { name: "Повторить сохранение" }));
  await waitFor(() => expect(bodies).toHaveLength(2));
  expect(bodies[1]).toBe(bodies[0]);
  expect(JSON.parse(bodies[0]!)).toMatchObject({ documentVersion: "saved-view-v2", target, state });
  await waitFor(() => expect(screen.getByLabelText("Сохранённая версия")).toHaveTextContent("3"));
});
it("keeps v2 when saving an opened historical full view", async () => {
  const bodies = server(() => json(200, { ...saved, version: 4 }));
  renderWithProviders(<Host initial={saved} />);
  await userEvent.click(screen.getByRole("button", { name: "Сохранить" }));
  await waitFor(() => expect(bodies).toHaveLength(1));
  expect(JSON.parse(bodies[0]!)).toMatchObject({
    documentVersion: "saved-view-v2",
    expectedVersion: 3,
  });
  expect(JSON.parse(bodies[0]!)).not.toHaveProperty("target");
});
it("requires save-as-new for another immutable full draft", async () => {
  const next = {
    changeProposal: { ...target.changeProposal, proposalRevisionId: exactIDs.repository },
  };
  const bodies = server(() => json(200, { ...saved, id: exactIDs.evidence, target: next }));
  renderWithProviders(<Host initial={saved} captureTarget={next} />);
  await userEvent.click(screen.getByRole("button", { name: "Capture" }));
  expect(screen.getByRole("button", { name: "Сохранить" })).toBeDisabled();
  await userEvent.click(screen.getByRole("button", { name: "Сохранить как новый" }));
  await waitFor(() => expect(bodies).toHaveLength(1));
  expect(JSON.parse(bodies[0]!)).toMatchObject({ documentVersion: "saved-view-v2", target: next });
});
it("uses explicit v2 for a native source6 capture hint", async () => {
  const source = { revisionId: exactIDs.revision };
  const bodies = server(() =>
    json(200, {
      ...saved,
      target: source,
      pins: { ...saved.pins, effective: exactEnvelope(source).pins },
    }),
  );
  renderWithProviders(<Host captureTarget={source} version="saved-view-v2" />);
  await userEvent.click(screen.getByRole("button", { name: "Capture" }));
  await userEvent.click(screen.getByRole("button", { name: "Сохранить" }));
  await waitFor(() => expect(bodies).toHaveLength(1));
  expect(JSON.parse(bodies[0]!)).toMatchObject({
    documentVersion: "saved-view-v2",
    target: source,
  });
});
it("refuses candidate persistence before sending a write", async () => {
  const bodies = server(() => json(200, saved));
  renderWithProviders(<Host captureTarget={exactCandidate} />);
  await userEvent.click(screen.getByRole("button", { name: "Capture" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(/подготовленн.*граф/);
  expect(screen.getByRole("button", { name: "Сохранить" })).toBeDisabled();
  expect(bodies).toHaveLength(0);
});
it("rejects a successful response for another target without adopting it", async () => {
  server(() =>
    json(200, {
      ...saved,
      target: {
        changeProposal: { ...target.changeProposal, proposalRevisionId: exactIDs.repository },
      },
    }),
  );
  renderWithProviders(<Host />);
  await userEvent.click(screen.getByRole("button", { name: "Capture" }));
  await userEvent.click(screen.getByRole("button", { name: "Сохранить" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(/друг|контекст/);
  expect(screen.getByLabelText("Точный источник")).toHaveTextContent(exactIDs.snapshot);
  expect(screen.getByLabelText("Сохранённая версия")).toBeEmptyDOMElement();
});
