import { afterEach, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { readFileSync } from "node:fs";
const source = readFileSync("../internal/backendmodel/testdata/diagrams/interaction.json", "utf8");
import { BackendInteractions, BackendInteractionsEditor } from "./BackendInteractions";
import type { BackendInteractionDocument } from "@/api/generated/schemas";
vi.mock("./BackendInteractionGraph", () => ({
  BackendInteractionGraph: () => <div>Static canvas</div>,
}));
afterEach(() => vi.unstubAllGlobals());
it("provides keyboard inspection and honest boundaries without scenario actions", async () => {
  const doc = JSON.parse(source) as BackendInteractionDocument;
  const select = vi.fn();
  const open = vi.fn();
  const user = userEvent.setup();
  renderWithProviders(
    <BackendInteractions
      payload={doc.payload}
      gaps={[]}
      selection={null}
      onSelect={select}
      onOpen={open}
      disabled={false}
    />,
  );
  expect(screen.getByText(/Порядок отображения не означает порядок исполнения/)).toBeVisible();
  const button = screen.getByRole("button", { name: "B: payment success" });
  button.focus();
  await user.keyboard("{Enter}");
  expect(select).toHaveBeenCalledWith({ type: "element", id: doc.payload.steps[1]!.id });
  expect(
    screen.queryByRole("button", { name: /Выполнить|Применить сценарий/ }),
  ).not.toBeInTheDocument();
});
it("semantic editor keeps unresolved send explicit", async () => {
  const doc = JSON.parse(source) as BackendInteractionDocument;
  const user = userEvent.setup();
  const save = vi.fn();
  renderWithProviders(
    <BackendInteractionsEditor
      document={doc}
      onChange={vi.fn()}
      onSave={save}
      onCancel={vi.fn()}
      busy={false}
    />,
  );
  await user.click(screen.getByRole("button", { name: "Сохранить interactions" }));
  expect(save).toHaveBeenCalledOnce();
  expect(screen.getByLabelText("Interactions JSON")).toHaveValue(
    JSON.stringify(doc.payload, null, 2),
  );
});

it("restores an old semantic pin and opens exact evidence without advancing to catalog head", async () => {
  const { BackendArchitecture } = await import("./BackendArchitecture");
  const { json } = await import("@/test/http");
  const doc = JSON.parse(source) as BackendInteractionDocument;
  const projectId = "10000000-0000-4000-8000-000000000001";
  const pin = {
    id: "10000000-0000-4000-8000-000000000002",
    version: 1,
    contentHash: "a".repeat(64),
  };
  const revisionId = "revisionId" in doc.target ? doc.target.revisionId : "";
  doc.payload.steps[0]!.refs = [{ kind: "record", recordType: "node", id: revisionId }];
  const value = {
    pin,
    projectId,
    document: doc,
    targetHash: "b".repeat(64),
    author: "reviewer",
    createdAt: "2026-10-05",
    gaps: [],
    provenance: { format: "backend-diagram-provenance-v1", action: "create", elements: [] },
    provenanceHash: "c".repeat(64),
  };
  const detailed = vi.fn();
  const navigate = vi.fn();
  const calls: string[] = [];
  vi.stubGlobal("fetch", async (url: string) => {
    calls.push(url);
    if (url.includes("/diagrams/query")) {
      return json(200, {
        pin,
        targetHash: value.targetHash,
        items: [],
        total: 0,
        nextCursor: "",
        gaps: [],
        truncated: false,
      });
    }
    if (url.includes(`/diagrams/${pin.id}/versions/1`)) return json(200, value);
    if (url.includes("/diagrams?"))
      return json(200, {
        items: [
          {
            kind: "interactions",
            id: pin.id,
            pin: { ...pin, version: 9 },
            targetHash: value.targetHash,
          },
        ],
        catalogVersion: 1,
        nextCursor: "",
      });
    if (url.endsWith(projectId))
      return json(200, { id: projectId, currentRevisionId: revisionId, name: "Orders" });
    return json(200, { items: [], nextCursor: "", catalogVersion: 1 });
  });
  renderWithProviders(
    <BackendArchitecture
      projectId={projectId}
      search={{
        diagramId: pin.id,
        diagramVersion: 1,
        diagramHash: pin.contentHash,
        diagramSelection: `element:${doc.payload.steps[0]!.id}`,
      }}
      onNavigate={navigate}
      onDetailedNavigate={detailed}
    />,
  );
  await screen.findByRole("heading", { name: "Динамические взаимодействия" });
  await userEvent.setup().click(screen.getByRole("button", { name: "Открыть точное основание 1" }));
  expect(detailed).toHaveBeenCalledWith({
    revisionId: revisionId,
    recordId: revisionId,
    recordType: "node",
  });
  expect(calls.some((url) => url.includes("/versions/9"))).toBe(false);
  expect(screen.getByRole("button", { name: "Сохранить новый interactions вид" })).toBeEnabled();
});

it("keeps malformed row JSON out of the rendered document", async () => {
  const { useState } = await import("react");
  const { fill } = await import("@/test/user");
  const doc = JSON.parse(source) as BackendInteractionDocument;
  function Harness() {
    const [value, setValue] = useState(doc);
    return (
      <BackendInteractionsEditor
        document={value}
        onChange={setValue}
        onSave={vi.fn()}
        onCancel={vi.fn()}
        busy={false}
      />
    );
  }
  renderWithProviders(<Harness />);
  await userEvent.clear(screen.getByLabelText("Interactions JSON"));
  await fill(
    screen.getByLabelText("Interactions JSON"),
    JSON.stringify({ participants: [null], steps: [], branches: [], order: [], scopeRefs: [] }),
  );
  expect(screen.getByText("Исправьте JSON перед сохранением")).toBeVisible();
  expect(screen.getByRole("button", { name: "Сохранить interactions" })).toBeDisabled();
});
