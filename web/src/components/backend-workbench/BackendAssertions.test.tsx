import { afterEach, expect, it, vi } from "vitest";
import { useState } from "react";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { json } from "@/test/http";
import { exactCandidate, exactClaim, exactEnvelope, exactIDs } from "@/test/backendExact";
import { BackendAssertions } from "./BackendAssertions";

afterEach(() => vi.unstubAllGlobals());
it("keeps losing provider claims expandable, pages exactly and opens their proof", async () => {
  const onEvidence = vi.fn();
  const fetcher = vi.fn(async (input: RequestInfo | URL) => {
    const url = new URL(String(input), "http://localhost");
    const next = url.searchParams.get("cursor") === "next-claim";
    return json(200, {
      ...exactEnvelope(),
      documentVersion: "source-assertions-v1",
      basis: "candidate",
      items: [exactClaim(next ? "reflection" : "compiler")],
      nextCursor: next ? "" : "next-claim",
    });
  });
  vi.stubGlobal("fetch", fetcher);
  renderWithProviders(
    <BackendAssertions
      projectId={exactIDs.project}
      target={exactCandidate}
      recordType="node"
      id={exactIDs.node}
      supported
      onEvidence={onEvidence}
    />,
  );
  await userEvent.click(await screen.findByText("compiler · handler"));
  expect(screen.getByText(/<em>Orders<\/em>/)).toBeInTheDocument();
  expect(screen.queryByRole("emphasis")).not.toBeInTheDocument();
  await userEvent.click(
    screen.getByRole("button", { name: `Открыть основание ${exactIDs.evidence}` }),
  );
  expect(onEvidence).toHaveBeenCalledWith(exactIDs.evidence);
  await userEvent.click(screen.getByRole("button", { name: "Следующие утверждения" }));
  expect(await screen.findByText("reflection · handler")).toBeInTheDocument();
  const url = new URL(String(fetcher.mock.calls.at(-1)?.[0]), "http://localhost");
  expect(url.pathname).toContain(`/imports/${exactIDs.project}/candidate/assertions`);
  expect(url.searchParams.get("importVersion")).toBe("7");
  expect(url.searchParams.get("candidateHash")).toBe(exactCandidate.importCandidate.candidateHash);
  expect(url.searchParams.get("id")).toBe(exactIDs.node);
  expect(url.searchParams.get("cursor")).toBe("next-claim");
});
it("does not request assertions for unsupported source5 or legacy targets", async () => {
  const fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
  renderWithProviders(
    <BackendAssertions
      projectId={exactIDs.project}
      target={{ revisionId: exactIDs.revision }}
      recordType="node"
      id={exactIDs.node}
      supported={false}
      onEvidence={vi.fn()}
    />,
  );
  expect(screen.getByText(/Отдельные утверждения доступны для source6/)).toBeInTheDocument();
  await waitFor(() => expect(fetcher).not.toHaveBeenCalled());
});
it("rejects a wrong target before showing provider data", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      json(200, {
        ...exactEnvelope({ revisionId: exactIDs.revision }),
        documentVersion: "source-assertions-v1",
        basis: "candidate",
        items: [exactClaim()],
        nextCursor: "",
      }),
    ),
  );
  renderWithProviders(
    <BackendAssertions
      projectId={exactIDs.project}
      target={exactCandidate}
      recordType="node"
      id={exactIDs.node}
      supported
      onEvidence={vi.fn()}
    />,
  );
  expect(await screen.findByRole("alert")).toHaveTextContent(/другой граф|источник/);
  expect(screen.queryByText("compiler · handler")).not.toBeInTheDocument();
});

it("checks the exact assertion hash selected from a qualified identity", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      json(200, {
        ...exactEnvelope(),
        documentVersion: "source-assertions-v1",
        basis: "candidate",
        items: [exactClaim()],
        nextCursor: "",
      }),
    ),
  );
  renderWithProviders(
    <BackendAssertions
      projectId={exactIDs.project}
      target={exactCandidate}
      recordType="node"
      id={exactIDs.node}
      supported
      repositoryId={exactIDs.repository}
      providerNamespace="compiler"
      assertionHash={"b".repeat(64)}
      onEvidence={vi.fn()}
    />,
  );
  expect(await screen.findByRole("alert")).toHaveTextContent(/утверждение.*идентичност/);
  expect(screen.queryByText("compiler · handler")).not.toBeInTheDocument();
});

it("does not reuse cached provider claims for a different assertion hash", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      json(200, {
        ...exactEnvelope(),
        documentVersion: "source-assertions-v1",
        basis: "candidate",
        items: [exactClaim()],
        nextCursor: "",
      }),
    ),
  );
  function Host() {
    const [hash, setHash] = useState("a".repeat(64));
    return (
      <>
        <button onClick={() => setHash("b".repeat(64))}>Другая идентичность</button>
        <BackendAssertions
          projectId={exactIDs.project}
          target={exactCandidate}
          recordType="node"
          id={exactIDs.node}
          supported
          repositoryId={exactIDs.repository}
          providerNamespace="compiler"
          assertionHash={hash}
          onEvidence={vi.fn()}
        />
      </>
    );
  }
  renderWithProviders(<Host />);
  await screen.findByText("compiler · handler");
  await userEvent.click(screen.getByRole("button", { name: "Другая идентичность" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(/утверждение.*идентичност/);
  expect(screen.queryByText("compiler · handler")).not.toBeInTheDocument();
});
