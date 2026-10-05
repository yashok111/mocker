import { afterEach, expect, it, vi } from "vitest";
import { act, useState } from "react";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { readFileSync } from "node:fs";
import { renderWithProviders } from "@/test/render";
import { json } from "@/test/http";
import { BackendArchitecture } from "./BackendArchitecture";
import type { BackendDiagramVersion, BackendBusinessMapDocument } from "@/api/generated/schemas";
vi.mock("./BackendArchitectureGraph", () => ({ BackendArchitectureGraph: () => null }));
afterEach(() => {
  vi.unstubAllGlobals();
  sessionStorage.clear();
});
it("ignores a delayed C4 dependency read after the business-map semantic pin changes", async () => {
  const projectId = "47000000-0000-4000-8000-000000000901",
    did = "47000000-0000-4000-8000-000000000902",
    aid = "47000000-0000-4000-8000-000000000903";
  const document = JSON.parse(
    readFileSync("../internal/backendmodel/testdata/diagrams/business_map.json", "utf8"),
  ) as BackendBusinessMapDocument;
  document.payload.architecture = { id: aid, version: 1, contentHash: "b".repeat(64) };
  const pin = { id: did, version: 1, contentHash: "a".repeat(64) };
  const v = {
    projectId,
    pin,
    document,
    targetHash: "c".repeat(64),
    author: "test",
    createdAt: "2026-10-05T00:00:00Z",
    gaps: [],
    provenance: { format: "backend-diagram-provenance-v1", action: "create", elements: [] },
    provenanceHash: "d".repeat(64),
  } as BackendDiagramVersion;
  const next = structuredClone(v);
  next.pin = { ...pin, version: 2, contentHash: "e".repeat(64) };
  if (next.document.kind === "business_map")
    next.document.payload.elements[0]!.label = "Customer next";
  let resolve: ((response: Response) => void) | undefined;
  const navigate = vi.fn();
  vi.stubGlobal("fetch", async (url: string) => {
    if (url.includes(`/diagrams/${aid}/`))
      return new Promise<Response>((r) => {
        resolve = r;
      });
    if (url.includes(`/diagrams/${did}/versions/2`)) return json(200, next);
    if (url.includes(`/diagrams/${did}/versions/1`)) return json(200, v);
    if (url.includes("/diagrams/query"))
      return json(200, {
        pin,
        targetHash: v.targetHash,
        items: [],
        total: 0,
        nextCursor: "",
        gaps: [],
        truncated: false,
      });
    if (url.endsWith(`/backend-projects/${projectId}`))
      return json(200, { id: projectId, currentRevisionId: "revision", name: "test" });
    return json(200, { items: [], nextCursor: "", catalogVersion: 0 });
  });
  const props = { projectId, onNavigate: navigate, onDetailedNavigate: vi.fn() };
  let advance: () => void = () => {};
  function Harness() {
    const [version, setVersion] = useState(1);
    advance = () => setVersion(2);
    return (
      <BackendArchitecture
        {...props}
        search={{
          diagramId: did,
          diagramVersion: version,
          diagramHash: version === 1 ? pin.contentHash : next.pin.contentHash,
        }}
      />
    );
  }
  renderWithProviders(<Harness />);
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Открыть точную C4 архитектуру" }));
  await waitFor(() => expect(resolve).toBeDefined());
  act(() => advance());
  await screen.findByRole("button", { name: "Customer next" });
  resolve!(
    json(200, {
      ...v,
      pin: document.payload.architecture,
      document: {
        format: "backend-diagram-v1",
        kind: "architecture",
        target: document.target,
        payload: { primarySystemId: "system", elements: [], links: [] },
      },
    }),
  );
  await new Promise((r) => setTimeout(r, 0));
  expect(navigate).not.toHaveBeenCalled();
});
