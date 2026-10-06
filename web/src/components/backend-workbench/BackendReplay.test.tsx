import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { BackendReplay } from "./BackendReplay";
const api = vi.hoisted(() => ({
  listBackendReplayTargets: vi.fn(),
  listBackendReplayProfiles: vi.fn(),
  listBackendReplayPackages: vi.fn(),
  listBackendReplayRuns: vi.fn(),
  getBackendReplayProfile: vi.fn(),
  getBackendReplayPackage: vi.fn(),
  startBackendReplay: vi.fn(),
  connectBackendReplayProfile: vi.fn(),
}));
vi.mock("@/api/generated/backend-projects/backend-projects", () => api);
vi.mock("./BackendTestProfiles", () => ({
  BackendTestProfiles: ({ onSelect }: { onSelect: (v: string) => void }) => (
    <button onClick={() => onSelect("profile:2")}>Select profile</button>
  ),
}));
it("reads the selected exact versions and uses the exact package for editing without executing", async () => {
  const profile = { pin: { id: "profile", version: 2, contentHash: "ph" } };
  const pkg = {
    pin: { id: "package", version: 3, contentHash: "kh" },
    package: { profile: profile.pin, name: "exact" },
    provenance: {},
  };
  api.listBackendReplayTargets.mockResolvedValue({ status: 200, data: [] });
  api.listBackendReplayProfiles.mockResolvedValue({ status: 200, data: [profile] });
  api.listBackendReplayPackages.mockResolvedValue({
    status: 200,
    data: [{ ...pkg, package: { ...pkg.package, name: "catalog" } }],
  });
  api.listBackendReplayRuns.mockResolvedValue({ status: 200, data: [] });
  api.getBackendReplayProfile.mockResolvedValue({ status: 200, data: profile });
  api.getBackendReplayPackage.mockResolvedValue({ status: 200, data: pkg });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider>
      <QueryClientProvider client={client}>
        <BackendReplay projectId="p" />
      </QueryClientProvider>
    </MantineProvider>,
  );
  expect(api.getBackendReplayPackage).not.toHaveBeenCalled();
  fireEvent.click(screen.getByText("Select profile"));
  await screen.findByText("package · v3 · kh");
  fireEvent.change(screen.getByLabelText("Сохранённая версия пакета"), {
    target: { value: "package:3" },
  });
  await waitFor(() =>
    expect(api.getBackendReplayPackage).toHaveBeenCalledWith(
      "p",
      "package",
      3,
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    ),
  );
  await waitFor(() =>
    expect(api.getBackendReplayProfile).toHaveBeenCalledWith(
      "p",
      "profile",
      2,
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    ),
  );
  fireEvent.click(screen.getByText("Подготовить новую версию выбранного пакета"));
  expect(screen.getByLabelText("Точный пакет JSON, включая diagram bindings")).toHaveValue(
    JSON.stringify(pkg.package, null, 2),
  );
  expect(api.startBackendReplay).not.toHaveBeenCalled();
  expect(api.connectBackendReplayProfile).not.toHaveBeenCalled();
  client.clear();
});
