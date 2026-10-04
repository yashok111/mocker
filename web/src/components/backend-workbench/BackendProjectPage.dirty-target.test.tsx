import { afterEach, expect, it, vi } from "vitest";
import { cleanup, waitFor } from "@testing-library/react";
import { renderInRouter } from "@/test/render";
import { exactIDs } from "@/test/backendExact";
import { BackendProjectPage } from "./BackendProjectPage";

const state = vi.hoisted(() => ({
  blocker: undefined as undefined | { shouldBlockFn: (value: unknown) => boolean },
  dirty: true,
  pending: null as unknown,
}));
vi.mock("@tanstack/react-router", async (original) => ({
  ...(await original<typeof import("@tanstack/react-router")>()),
  useBlocker: (options: typeof state.blocker) => {
    state.blocker = options;
  },
}));
vi.mock("./useBackendSavedViewSession", () => ({
  useBackendSavedViewSession: () => ({ dirty: state.dirty, pending: state.pending }),
}));
vi.mock("./BackendFlowGraph", () => ({ BackendFlowGraph: () => null }));
vi.mock("./BackendDatabaseGraph", () => ({ BackendDatabaseGraph: () => null }));

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  state.blocker = undefined;
  state.dirty = true;
  state.pending = null;
});

const source = { revisionId: exactIDs.revision };
const full = {
  ...source,
  changeProposalId: exactIDs.project,
  proposalRevisionId: exactIDs.snapshot,
};
const transitions = [
  { name: "source to full", from: source, to: full },
  { name: "full to source", from: full, to: source },
  {
    name: "another draft",
    from: full,
    to: { ...full, proposalRevisionId: exactIDs.repository },
  },
  {
    name: "another proposal",
    from: full,
    to: { ...full, changeProposalId: exactIDs.repository },
  },
];

it.each(transitions)("confirms $name on the same source base", async ({ from, to }) => {
  vi.stubGlobal(
    "fetch",
    vi.fn(() => new Promise<Response>(() => {})),
  );
  const confirm = vi.fn(() => false);
  vi.stubGlobal("confirm", confirm);
  renderInRouter(<BackendProjectPage projectId={exactIDs.project} />);
  await waitFor(() => expect(state.blocker).toBeDefined());
  const current = { pathname: `/backend-projects/${exactIDs.project}`, search: from };
  const next = { ...current, search: to };
  expect(state.blocker!.shouldBlockFn({ current, next: current })).toBe(false);
  expect(confirm).not.toHaveBeenCalled();
  expect(state.blocker!.shouldBlockFn({ current, next, action: "PUSH" })).toBe(true);
  expect(confirm).toHaveBeenCalledTimes(1);
  confirm.mockReturnValue(true);
  expect(state.blocker!.shouldBlockFn({ current, next, action: "PUSH" })).toBe(false);
});

it.each(["BACK", "FORWARD"])("guards unknown saved-view attempts during %s", async (action) => {
  state.dirty = false;
  state.pending = { clientCommandId: "unknown-save" };
  vi.stubGlobal(
    "fetch",
    vi.fn(() => new Promise<Response>(() => {})),
  );
  const confirm = vi.fn(() => false);
  vi.stubGlobal("confirm", confirm);
  renderInRouter(<BackendProjectPage projectId={exactIDs.project} />);
  await waitFor(() => expect(state.blocker).toBeDefined());
  const current = { pathname: `/backend-projects/${exactIDs.project}`, search: full };
  const next = { ...current, search: { ...full, proposalRevisionId: exactIDs.repository } };
  expect(state.blocker!.shouldBlockFn({ current, next, action })).toBe(true);
  expect(confirm).toHaveBeenCalledTimes(1);
});

it("allows a clean workspace to change the full target without prompting", async () => {
  state.dirty = false;
  vi.stubGlobal(
    "fetch",
    vi.fn(() => new Promise<Response>(() => {})),
  );
  const confirm = vi.fn(() => false);
  vi.stubGlobal("confirm", confirm);
  renderInRouter(<BackendProjectPage projectId={exactIDs.project} />);
  await waitFor(() => expect(state.blocker).toBeDefined());
  const current = { pathname: `/backend-projects/${exactIDs.project}`, search: source };
  expect(state.blocker!.shouldBlockFn({ current, next: { ...current, search: full } })).toBe(false);
  expect(confirm).not.toHaveBeenCalled();
});
