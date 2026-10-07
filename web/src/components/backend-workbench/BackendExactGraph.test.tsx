import { afterEach, expect, it, vi } from "vitest";
import { useState } from "react";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { json } from "@/test/http";
import {
  exactClaim,
  exactCoverage,
  exactEnvelope,
  exactIDs,
  exactNode,
  exactSource,
} from "@/test/backendExact";
import { BackendExactGraph } from "./BackendExactGraph";
import { BackendGraphInventory } from "./BackendGraphInventory";
import type { BackendReadTarget } from "@/api/generated/schemas";

afterEach(() => vi.unstubAllGlobals());
const full: BackendReadTarget = {
  changeProposal: { proposalId: exactIDs.project, proposalRevisionId: exactIDs.snapshot },
};
function server(target: BackendReadTarget, source6 = true) {
  const source = source6 ? exactSource() : { ...exactSource(), sourceVector: undefined };
  const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname.endsWith("/coverage"))
      return json(200, { ...exactEnvelope(target), ...exactCoverage, source });
    if (url.pathname.endsWith("/graph/query")) {
      const q = JSON.parse(String(init?.body));
      return json(200, {
        ...exactEnvelope(target),
        nodes: q.recordType === "nodes" ? [exactNode()] : [],
        edges: [],
        nextCursor: "",
      });
    }
    if (url.pathname.includes("/nodes/"))
      return json(200, {
        ...exactEnvelope(target),
        node: { ...exactNode(), source },
        origins: [
          {
            recordType: "node",
            subjectId: exactIDs.node,
            selector: { kind: "name" },
            kind: "intent",
            commandId: exactIDs.evidence,
            reason: "Rename for new API",
            sourceClaims: [],
            evidenceIds: [],
          },
        ],
        proposalPins: null,
        proposalProjection: null,
      });
    if (url.pathname.endsWith("/assertions"))
      return json(200, {
        ...exactEnvelope(target),
        documentVersion: "source-assertions-v1",
        basis: target.changeProposal ? "baseline" : "source",
        items: [exactClaim()],
        nextCursor: "",
      });
    if (url.pathname.endsWith("/evidence"))
      return json(200, { ...exactEnvelope(target), items: [], nextCursor: "", source });
    return json(500, { error: { message: url.pathname } });
  });
  vi.stubGlobal("fetch", fetcher);
  return fetcher;
}
it("mounts the source6 reader from the existing source inventory and offers representation kinds", async () => {
  const target = { revisionId: exactIDs.revision };
  server(target);
  renderWithProviders(
    <BackendGraphInventory
      projectId={exactIDs.project}
      revisionId={exactIDs.revision}
      schemaVersion="6"
    />,
  );
  expect(await screen.findByRole("option", { name: "representation_field" })).toBeInTheDocument();
  await userEvent.click(await screen.findByRole("button", { name: "Открыть объект Orders" }));
  expect(await screen.findByText("compiler · handler")).toBeInTheDocument();
});
it("shows desired origins separately and never fetches source6 claims for a full proposal on source5", async () => {
  const fetcher = server(full, false);
  renderWithProviders(<BackendExactGraph projectId={exactIDs.project} target={full} />);
  await userEvent.click(await screen.findByRole("button", { name: "Открыть объект Orders" }));
  expect(await screen.findByText(/Rename for new API/)).toBeInTheDocument();
  expect(screen.getByText(/Отдельные утверждения доступны для source6/)).toBeInTheDocument();
  expect(screen.getByText(/Основания базового источника/)).toBeInTheDocument();
  expect(fetcher.mock.calls.some(([url]) => String(url).includes("/assertions"))).toBe(false);
  expect(
    fetcher.mock.calls
      .filter(([url]) => !String(url).endsWith("/graph/query"))
      .every(([url]) => String(url).includes("/change-proposals/")),
  ).toBe(true);
});
it("opens a new annotation focus in the same exact source graph", async () => {
  server({ revisionId: exactIDs.revision });
  function Host() {
    const [focus, setFocus] = useState(false);
    return (
      <>
        <button onClick={() => setFocus(true)}>Открыть из заметки</button>
        <BackendExactGraph
          projectId={exactIDs.project}
          target={{ revisionId: exactIDs.revision }}
          focusTarget={focus ? { recordType: "node", id: exactIDs.node } : undefined}
        />
      </>
    );
  }
  renderWithProviders(<Host />);
  await screen.findByRole("button", { name: "Открыть объект Orders" });
  await userEvent.click(screen.getByRole("button", { name: "Открыть из заметки" }));
  expect(await screen.findByRole("region", { name: "Инспектор объекта" })).toBeInTheDocument();
});
it("does not show a node response arriving after a full target changes", async () => {
  let finish: ((value: Response) => void) | undefined;
  const fetcher = server(full);
  const original = fetcher.getMockImplementation()!;
  fetcher.mockImplementation(async (input, init) =>
    String(input).includes("/nodes/")
      ? new Promise((resolve) => {
          finish = resolve;
        })
      : original(input, init),
  );
  const next = {
    changeProposal: { proposalId: exactIDs.project, proposalRevisionId: exactIDs.repository },
  };
  function Host() {
    const [target, setTarget] = useState(full);
    return (
      <>
        <button onClick={() => setTarget(next)}>Другой черновик</button>
        <BackendExactGraph projectId={exactIDs.project} target={target} />
      </>
    );
  }
  renderWithProviders(<Host />);
  await userEvent.click(await screen.findByRole("button", { name: "Открыть объект Orders" }));
  await waitFor(() => expect(finish).toBeDefined());
  server(next);
  await userEvent.click(screen.getByRole("button", { name: "Другой черновик" }));
  finish?.(
    json(200, {
      ...exactEnvelope(full),
      node: { ...exactNode(), name: "OLD DELAYED" },
      origins: [],
      proposalPins: null,
      proposalProjection: null,
    }),
  );
  await screen.findByRole("button", { name: "Открыть объект Orders" });
  expect(screen.queryByText("OLD DELAYED")).not.toBeInTheDocument();
  expect(screen.queryByRole("region", { name: "Инспектор объекта" })).not.toBeInTheDocument();
});

it("clears node-only search when selecting the edge inventory", async () => {
  const fetcher = server({ revisionId: exactIDs.revision });
  renderWithProviders(
    <BackendExactGraph projectId={exactIDs.project} target={{ revisionId: exactIDs.revision }} />,
  );
  await screen.findByRole("button", { name: "Открыть объект Orders" });
  await userEvent.type(screen.getByRole("textbox", { name: "Название объекта" }), "abc");
  await userEvent.selectOptions(screen.getByRole("combobox", { name: "Записи графа" }), "edges");
  await userEvent.click(screen.getByRole("button", { name: "Найти объекты" }));
  // 6006fa8 added the «Сервис модели» filter, fed by its own graph read
  // (kind "service"); only the inventory reads are counted here.
  const inventoryReads = () =>
    fetcher.mock.calls.filter(
      ([url, init]) =>
        String(url).endsWith("/graph/query") && JSON.parse(String(init?.body)).kind !== "service",
    );
  await waitFor(() => expect(inventoryReads().length).toBe(2));
  const body = JSON.parse(String(inventoryReads().at(-1)?.[1]?.body));
  expect(body.recordType).toBe("edges");
  expect(Object.hasOwn(body, "search")).toBe(false);
  expect(screen.queryByRole("textbox", { name: "Название объекта" })).not.toBeInTheDocument();
});

it("shows the saved desired edge name without changing its kind or source identity", async () => {
  const fetcher = server(full);
  const original = fetcher.getMockImplementation()!;
  fetcher.mockImplementation(async (input, init) => {
    if (
      String(input).includes("/assertions") &&
      new URL(String(input), "http://localhost").searchParams.get("id") === exactIDs.evidence
    )
      return json(200, {
        ...exactEnvelope(full),
        documentVersion: "source-assertions-v1",
        basis: "baseline",
        items: [],
        nextCursor: "",
      });
    if (
      String(input).endsWith("/graph/query") &&
      JSON.parse(String(init?.body)).recordType === "edges"
    )
      return json(200, {
        ...exactEnvelope(full),
        nodes: [],
        edges: [
          {
            id: exactIDs.evidence,
            kind: "contains",
            from: exactIDs.node,
            to: exactIDs.repository,
            attributes: {},
            evidenceIds: [],
            source: { ...exactSource(), identities: [] },
          },
        ],
        edgeNames: [{ id: exactIDs.evidence, name: "Desired relation label" }],
        nextCursor: "",
      });
    return original(input, init);
  });
  renderWithProviders(<BackendExactGraph projectId={exactIDs.project} target={full} />);
  await screen.findByRole("button", { name: "Открыть объект Orders" });
  await userEvent.selectOptions(screen.getByRole("combobox", { name: "Записи графа" }), "edges");
  await userEvent.click(screen.getByRole("button", { name: "Найти объекты" }));
  await userEvent.click(
    await screen.findByRole("button", { name: /Открыть связь Desired relation label/ }),
  );
  expect(
    await screen.findByRole("heading", { name: "Desired relation label" }),
  ).toBeInTheDocument();
});
