import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, fireEvent, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { BackendReplay } from "./BackendReplay";
const api = vi.hoisted(() => ({
  listBackendReplayTargets: vi.fn(),
  listBackendReplayProfiles: vi.fn(),
  listBackendReplayPackages: vi.fn(),
  listBackendReplayRuns: vi.fn(),
  getBackendReplayRun: vi.fn(),
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
it("keeps re-reading the runs list behind a closed panel only while the selected run is active", async () => {
  // Fake from before the mount so the intervals React Query schedules are
  // fake ones; shouldAdvanceTime keeps findBy*'s own polling alive.
  vi.useFakeTimers({ shouldAdvanceTime: true });
  try {
    let status = "running";
    api.listBackendReplayTargets.mockResolvedValue({ status: 200, data: [] });
    api.listBackendReplayProfiles.mockResolvedValue({ status: 200, data: [] });
    api.listBackendReplayPackages.mockResolvedValue({ status: 200, data: [] });
    api.listBackendReplayRuns.mockImplementation(async () => ({
      status: 200,
      data: [{ id: "run-1", status }],
    }));
    api.getBackendReplayRun.mockImplementation(async () => ({
      status: 200,
      data: { id: "run-1", status },
    }));
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <MantineProvider>
        <QueryClientProvider client={client}>
          <BackendReplay projectId="p" open={false} />
        </QueryClientProvider>
      </MantineProvider>,
    );
    const elapse = (ms: number) =>
      act(async () => {
        await vi.advanceTimersByTimeAsync(ms);
      });
    await screen.findAllByText("running · run-1");
    await elapse(2000 * 3);
    // Closed and nothing awaited: the mount's one read, no poll.
    expect(api.listBackendReplayRuns).toHaveBeenCalledTimes(1);

    fireEvent.change(screen.getByLabelText("Run и неизменяемый отчёт"), {
      target: { value: "run-1" },
    });
    await screen.findByText("Статус: running");
    await elapse(2000 * 3);
    const whileActive = api.listBackendReplayRuns.mock.calls.length;
    expect(whileActive).toBeGreaterThanOrEqual(3);

    // The run reaches its verdict through its own poll, and the list stops.
    status = "succeeded";
    await elapse(2000);
    await screen.findByText("Статус: succeeded");
    const settled = api.listBackendReplayRuns.mock.calls.length;
    await elapse(2000 * 3);
    expect(api.listBackendReplayRuns).toHaveBeenCalledTimes(settled);
    client.clear();
  } finally {
    vi.useRealTimers();
  }
});
