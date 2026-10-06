import { afterEach, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { json } from "@/test/http";
import { BackendMaterialization } from "./BackendMaterialization";

afterEach(() => {
  vi.unstubAllGlobals();
  localStorage.clear();
});
it("requires explicit partial consent and invalidates the preview on an edit", async () => {
  const bodies: Record<string, unknown>[] = [];
  vi.stubGlobal("fetch", async (_url: string, init?: RequestInit) => {
    bodies.push(JSON.parse(String(init?.body)));
    return json(200, {
      input: bodies.at(-1),
      effects: [],
      coverage: [{ sourceId: "x", status: "excluded", reason: "opaque guard" }],
      diagnostics: [],
      equivalence: "partial_simulation",
      candidateHash: "a".repeat(64),
      canApply: true,
    });
  });
  renderWithProviders(<BackendMaterialization projectId="test" />);
  const consent = screen.getByRole("checkbox");
  await userEvent.click(consent);
  expect(screen.getByRole("button", { name: "Предпросмотр материализации" })).toBeDisabled();
  await userEvent.type(screen.getByLabelText(/Исключённые ID/), "x");
  await userEvent.type(screen.getByLabelText(/Причина перевода/), "opaque guard");
  await userEvent.click(screen.getByRole("button", { name: "Предпросмотр материализации" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Применить к черновикам" })).toBeEnabled(),
  );
  expect(bodies[0]).toMatchObject({
    partialSimulation: true,
    excludedIds: ["x"],
    reason: "opaque guard",
  });
  await userEvent.type(screen.getByLabelText(/Причина перевода/), " changed");
  expect(screen.getByRole("button", { name: "Применить к черновикам" })).toBeDisabled();
  await userEvent.click(consent);
  await userEvent.click(screen.getByRole("button", { name: "Предпросмотр материализации" }));
  await waitFor(() => expect(bodies).toHaveLength(2));
  expect(bodies[1]).toMatchObject({ partialSimulation: false, excludedIds: [] });
});
it("retains identical apply bytes and key across an uncertain response and remount", async () => {
  const bodies: string[] = [];
  vi.stubGlobal("fetch", async (url: string, init?: RequestInit) => {
    if (url.endsWith("/preview"))
      return json(200, {
        input: {},
        effects: [],
        coverage: [],
        diagnostics: [],
        equivalence: "structural_projection",
        candidateHash: "a".repeat(64),
        canApply: true,
      });
    bodies.push(String(init?.body));
    if (bodies.length === 1) throw new TypeError("lost reply");
    return json(200, {
      id: "receipt",
      owners: [],
      coverage: [],
      equivalence: "structural_projection",
    });
  });
  const view = renderWithProviders(<BackendMaterialization projectId="retry" />);
  await userEvent.click(screen.getByRole("button", { name: "Предпросмотр материализации" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Применить к черновикам" })).toBeEnabled(),
  );
  await userEvent.click(screen.getByRole("button", { name: "Применить к черновикам" }));
  await screen.findByText(/Результат ещё не подтверждён/);
  view.unmount();
  renderWithProviders(<BackendMaterialization projectId="retry" />);
  await userEvent.click(screen.getByRole("button", { name: "Повторить применение" }));
  await screen.findByRole("status");
  expect(bodies).toHaveLength(2);
  expect(bodies[1]).toBe(bodies[0]);
  expect(localStorage.getItem("mocker-materialization-v1:retry")).toBeNull();
});
