import { createHash } from "node:crypto";
import { afterEach, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { json } from "@/test/http";
import { BackendPortable } from "./BackendPortable";
import { readPortableBundle } from "./backendPortableTransfer";

const project = "11111111-1111-4111-8111-111111111111";
const sessionID = "22222222-2222-4222-8222-222222222222";
const imported = "33333333-3333-4333-8333-333333333333";
function canonical(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(canonical).join(",")}]`;
  if (value && typeof value === "object")
    return `{${Object.entries(value)
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([k, v]) => `${JSON.stringify(k)}:${canonical(v)}`)
      .join(",")}}`;
  return JSON.stringify(value);
}
function fixture() {
  const body = '[{"wide":9007199254740993}]';
  const manifest = {
    format: "backend-portable-v1",
    originInstallationId: project,
    schemas: ["5", "artifact-context-v3"],
    selection: {
      projectId: project,
      target: { revisionId: project },
      targetHash: "a".repeat(64),
      diagramViews: [],
    },
    chunks: [
      {
        index: 0,
        sha256: createHash("sha256").update(body).digest("hex"),
        bytes: Buffer.byteLength(body),
        records: 1,
      },
    ],
  };
  return {
    body,
    manifest,
    manifestHash: createHash("sha256").update(canonical(manifest)).digest("hex"),
    file: new File([JSON.stringify({ manifest, chunks: [body] })], "bundle.json", {
      type: "application/json",
    }),
  };
}
afterEach(() => {
  vi.unstubAllGlobals();
  localStorage.clear();
});

it("preserves chunk numeric text and commit request across lost reply and remount", async () => {
  const data = fixture();
  const commits: string[] = [];
  const puts: string[] = [];
  const session = (version: number, state: string) => ({
    id: sessionID,
    direction: "import",
    version,
    state,
    manifestHash: data.manifestHash,
  });
  vi.stubGlobal("fetch", async (url: string, init?: RequestInit) => {
    if (url.endsWith("/chunks")) {
      puts.push(String(init?.body));
      return json(200, session(2, "staging"));
    }
    if (url.endsWith("/preview"))
      return json(200, {
        session: session(3, "ready"),
        candidateHash: "b".repeat(64),
        projectId: imported,
        target: { revisionId: imported },
        targetHash: "c".repeat(64),
        idMap: [],
        unresolved: [
          {
            namespace: { scope: "foreign", installationId: project },
            pin: { kind: "design_scenario", id: "1", revisionId: "1", contentHash: "d".repeat(64) },
          },
        ],
        recordCount: 1,
      });
    if (url.endsWith("/commit")) {
      commits.push(String(init?.body));
      if (commits.length === 1) throw new TypeError("lost reply");
      return json(200, {
        session: session(4, "committed"),
        project: { id: imported, name: "Copy" },
        target: { revisionId: imported },
        targetHash: "c".repeat(64),
        candidateHash: "b".repeat(64),
        idMap: [],
      });
    }
    return json(200, session(1, "staging"));
  });
  const view = renderWithProviders(<BackendPortable projectId={project} />);
  await userEvent.upload(
    view.container.querySelector<HTMLInputElement>('input[type="file"]')!,
    data.file,
  );
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Загрузить chunks" })).toBeEnabled(),
  );
  await userEvent.click(screen.getByRole("button", { name: "Загрузить chunks" }));
  await waitFor(() => expect(puts).toHaveLength(1));
  expect(JSON.parse(puts[0]!).body).toBe(data.body);
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Preview импорта" })).toBeEnabled(),
  );
  await userEvent.click(screen.getByRole("button", { name: "Preview импорта" }));
  await screen.findByText(/Неразрешённые foreign refs: 1/);
  await userEvent.click(screen.getByRole("button", { name: "Создать проект атомарно" }));
  await screen.findByRole("button", { name: "Повторить portable-запрос" });
  view.unmount();
  renderWithProviders(<BackendPortable projectId={project} />);
  await userEvent.click(screen.getByRole("button", { name: "Повторить portable-запрос" }));
  const link = await screen.findByRole("link", { name: "Открыть импортированный проект" });
  expect(link).toHaveAttribute("href", `/backend-projects/${imported}`);
  expect(commits).toHaveLength(2);
  expect(commits[1]).toBe(commits[0]);
});

it("invalidates approval after editing mappings and rejects tampered chunk hashes before upload", async () => {
  const data = fixture();
  const bad = {
    manifest: data.manifest,
    chunks: [data.body.replace("9007199254740993", "9007199254740994")],
  };
  await expect(readPortableBundle(JSON.stringify(bad))).rejects.toThrow(/SHA-256/);
  const session = {
    id: sessionID,
    direction: "import",
    version: 3,
    state: "ready",
    manifestHash: data.manifestHash,
  };
  localStorage.setItem(
    `mocker-portable-import-v1:${project}`,
    JSON.stringify({
      manifestHash: data.manifestHash,
      session,
      nextChunk: 1,
      candidate: {
        hash: "b".repeat(64),
        projectId: imported,
        request: JSON.stringify(["", []]),
        recordCount: 1,
        unresolvedCount: 0,
      },
    }),
  );
  renderWithProviders(<BackendPortable projectId={project} />);
  expect(screen.getByRole("button", { name: "Создать проект атомарно" })).toBeEnabled();
  await userEvent.type(screen.getByLabelText("Название нового проекта"), "Changed");
  expect(screen.getByRole("button", { name: "Создать проект атомарно" })).toBeDisabled();
});

it("unlocks export selection after a confirmed quota rejection", async () => {
  const data = fixture();
  vi.stubGlobal("fetch", async (url: string) => {
    if (url.endsWith("/selection")) return json(200, data.manifest.selection);
    return json(413, { error: { code: "limit_exceeded", message: "Export quota" } });
  });
  const view = renderWithProviders(<BackendPortable projectId={project} />);
  await userEvent.click(screen.getByRole("button", { name: "Скачать portable-пакет" }));
  await waitFor(() =>
    expect(
      screen.getByLabelText("Точный target и сохранённые виды для экспорта (JSON)"),
    ).toBeEnabled(),
  );
  expect(localStorage.getItem(`mocker-portable-export-v1:${project}`)).toBeNull();
  view.unmount();
  renderWithProviders(<BackendPortable projectId={project} />);
  expect(screen.getByRole("button", { name: "Скачать portable-пакет" })).toBeEnabled();
});
