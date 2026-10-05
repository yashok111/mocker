import { afterEach, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { readFileSync } from "node:fs";
import { renderWithProviders } from "@/test/render";
import { BackendLifecycle, BackendLifecycleEditor } from "./BackendLifecycle";
import type { BackendLifecycleDocument } from "@/api/generated/schemas";
const source = readFileSync("../internal/backendmodel/testdata/diagrams/lifecycle.json", "utf8");
vi.mock("../state-diagram/StateGraph", () => ({
  default: ({ readOnly }: { readOnly: boolean }) => (
    <div>{readOnly ? "Read-only lifecycle graph" : "Editable graph"}</div>
  ),
}));
it("keeps missing transitions unverified and supports keyboard inspection", async () => {
  const d = JSON.parse(source) as BackendLifecycleDocument;
  const select = vi.fn();
  const user = userEvent.setup();
  renderWithProviders(
    <BackendLifecycle
      payload={d.payload}
      gaps={[]}
      selection={null}
      onSelect={select}
      onOpen={vi.fn()}
      disabled={false}
    />,
  );
  expect(screen.getByText(/отсутствие перехода не доказывает/)).toBeVisible();
  const state = screen.getByRole("button", { name: "Paid" });
  state.focus();
  await user.keyboard("{Enter}");
  expect(select).toHaveBeenCalledWith({ type: "element", id: d.payload.states[1]!.id });
  expect(screen.queryByRole("button", { name: /Симулировать|Применить/ })).not.toBeInTheDocument();
  expect(await screen.findByText("Read-only lifecycle graph")).toBeVisible();
});
it("shows opaque guards and exact trigger refs without evaluating", async () => {
  const d = JSON.parse(source) as BackendLifecycleDocument;
  const open = vi.fn();
  const user = userEvent.setup();
  renderWithProviders(
    <BackendLifecycle
      payload={d.payload}
      gaps={[]}
      selection={{ type: "link", id: d.payload.transitions[1]!.id }}
      onSelect={vi.fn()}
      onOpen={open}
      disabled={false}
    />,
  );
  expect(screen.getByText(/Непрозрачное условие: external approval/)).toBeVisible();
  await user.click(screen.getByRole("button", { name: "Открыть triggers 1" }));
  expect(open).toHaveBeenCalledWith(d.payload.transitions[1]!.triggers[0]);
});
it("separates semantic save from desired-rule authoring and blocks invalid JSON", async () => {
  const d = JSON.parse(source) as BackendLifecycleDocument;
  const save = vi.fn();
  const user = userEvent.setup();
  renderWithProviders(
    <BackendLifecycleEditor
      document={d}
      onChange={vi.fn()}
      onSave={save}
      onCancel={vi.fn()}
      busy={false}
    />,
  );
  await user.click(screen.getByRole("button", { name: "Сохранить lifecycle" }));
  expect(save).toHaveBeenCalledOnce();
  expect(screen.getByText(/не создают исходные переходы/)).toBeVisible();
  const input = screen.getByLabelText("Lifecycle JSON");
  await user.clear(input);
  expect(screen.getByRole("button", { name: "Сохранить lifecycle" })).toBeDisabled();
});

it("recovers from malformed rule Ref through the same operation field", async () => {
  const d = JSON.parse(source) as BackendLifecycleDocument;
  const user = userEvent.setup();
  const change = vi.fn();
  renderWithProviders(
    <BackendLifecycleEditor
      document={d}
      onChange={change}
      onSave={vi.fn()}
      onCancel={vi.fn()}
      busy={false}
    />,
  );
  await user.selectOptions(screen.getByLabelText("Из состояния"), d.payload.states[0]!.id);
  await user.selectOptions(screen.getByLabelText("В состояние"), d.payload.states[1]!.id);
  const { fill } = await import("@/test/user");
  await fill(screen.getByLabelText("Основание lifecycle"), "Explicit desired rule");
  const field = screen.getByLabelText("Точная операция правила (Ref JSON)");
  await fill(field, "not JSON");
  await user.click(screen.getByRole("button", { name: "Добавить авторское правило" }));
  expect(screen.getByRole("alert")).toHaveTextContent("Укажите точный Ref JSON операции");
  await user.clear(field);
  await fill(field, JSON.stringify(d.payload.rules[0]!.trigger));
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Добавить авторское правило" }));
  expect(change).toHaveBeenCalledOnce();
});

afterEach(() => vi.unstubAllGlobals());
it("retains lifecycle pins and enables both layout coordinates", async () => {
  const { BackendArchitecture } = await import("./BackendArchitecture");
  const { json } = await import("@/test/http");
  const doc = JSON.parse(source) as BackendLifecycleDocument;
  const projectId = "10000000-0000-4000-8000-000000000001";
  const pin = {
    id: "10000000-0000-4000-8000-000000000002",
    version: 1,
    contentHash: "a".repeat(64),
  };
  const revisionId = "revisionId" in doc.target ? doc.target.revisionId : "";
  doc.payload.transitions[0]!.refs = [{ kind: "record", recordType: "node", id: revisionId }];
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
            kind: "lifecycle",
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
        diagramSelection: `element:${doc.payload.transitions[0]!.id}`,
      }}
      onNavigate={navigate}
      onDetailedNavigate={detailed}
    />,
  );
  await screen.findByRole("heading", { name: "Жизненный цикл сущности" });
  expect(screen.getByLabelText("Координата Y")).toBeEnabled();
  await userEvent.setup().click(screen.getByRole("button", { name: "Открыть точное основание 1" }));
  expect(detailed).toHaveBeenCalledWith({
    revisionId: revisionId,
    recordId: revisionId,
    recordType: "node",
  });
  expect(calls.some((url) => url.includes("/versions/9"))).toBe(false);
  expect(screen.getByRole("button", { name: "Сохранить новый lifecycle вид" })).toBeEnabled();
});
