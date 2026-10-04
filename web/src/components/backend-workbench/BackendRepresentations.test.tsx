import { afterEach, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { json } from "@/test/http";
import { exactEnvelope, exactIDs, exactNode } from "@/test/backendExact";
import { BackendRepresentationFields, BackendRepresentationValue } from "./BackendRepresentations";
afterEach(() => vi.unstubAllGlobals());
const target = { revisionId: exactIDs.revision };
const field = {
  ...exactNode(),
  kind: "representation_field" as const,
  parentId: exactIDs.repository,
  name: "dto.name",
  attributes: {
    selector: [{ property: "profile" }, { property: "name" }],
    nativeType: { status: "known" as const, value: "string" },
    nullable: { status: "unknown" as const, reason: "Declaration unavailable" },
    cardinality: { status: "known" as const, value: "one" as const },
    analysisStatus: "partial" as const,
    gaps: ["Mapping not observed"],
  },
};
it("lists fields by explicit parent and pages without joining same-name owners", async () => {
  const inspect = vi.fn();
  const fetcher = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
    const body = JSON.parse(String(init?.body));
    return json(200, {
      ...exactEnvelope(target),
      nodes: [field],
      edges: [],
      nextCursor: body.cursor ? "" : "next",
    });
  });
  vi.stubGlobal("fetch", fetcher);
  renderWithProviders(
    <BackendRepresentationFields
      projectId={exactIDs.project}
      target={target}
      ownerId={exactIDs.repository}
      onInspect={inspect}
    />,
  );
  expect(await screen.findByText("profile.name")).toBeInTheDocument();
  expect(screen.getByText("Declaration unavailable")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Открыть поле dto.name" }));
  expect(inspect).toHaveBeenCalledWith(exactIDs.node);
  await userEvent.click(screen.getByRole("button", { name: "Следующие поля представления" }));
  const last = JSON.parse(String(fetcher.mock.calls.at(-1)?.[1]?.body));
  expect(last).toMatchObject({
    revisionId: exactIDs.revision,
    kind: "representation_field",
    parentId: exactIDs.repository,
    cursor: "next",
  });
  expect(last.search).toBeUndefined();
});
it("uses the exact representation value reference for lineage", async () => {
  const select = vi.fn();
  renderWithProviders(<BackendRepresentationValue node={field} onValueSelect={select} />);
  await userEvent.click(screen.getByRole("button", { name: "Происхождение поля dto.name" }));
  expect(select).toHaveBeenCalledWith({ kind: "representation_field", nodeId: exactIDs.node });
  expect(screen.getByText("Mapping not observed")).toBeInTheDocument();
});
