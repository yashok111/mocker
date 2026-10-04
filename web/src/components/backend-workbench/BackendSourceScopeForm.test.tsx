import { afterEach, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { BackendSourceScopeForm } from "./BackendSourceScopeForm";
import { sourceProfiles } from "./backendSourceInputs";
import {
  syncHash,
  syncIds,
  syncInventory,
  syncManifest,
  syncSnapshot,
} from "./backendSyncTestFixtures";

afterEach(() => vi.restoreAllMocks());
async function upload(user: ReturnType<typeof userEvent.setup>, manifest = syncManifest) {
  await user.upload(
    screen.getByLabelText("Манифест JSON"),
    new File([JSON.stringify(manifest)], "manifest.json", { type: "application/json" }),
  );
  await user.upload(
    screen.getByLabelText("Inventory JSON — девять категорий"),
    new File([JSON.stringify(syncInventory)], "inventory.json", { type: "application/json" }),
  );
  await waitFor(() =>
    expect(screen.getByLabelText("Я проверил область, манифест и inventory")).toBeEnabled(),
  );
}

it.each(["add_repository", "add_provider", "reconcile", "migrate_provider"] as const)(
  "submits exact %s scope through DOM controls",
  async (kind) => {
    const user = userEvent.setup(),
      onBegin = vi.fn();
    renderWithProviders(
      <BackendSourceScopeForm
        baseRevisionId={syncIds.revision}
        projectVersion={3}
        baseSchema="6"
        emptyBase={false}
        partitions={[
          {
            repositoryId: syncIds.repository,
            snapshotId: syncIds.snapshot,
            provider: syncManifest.provider,
          },
        ]}
        onBegin={onBegin}
      />,
    );
    await user.selectOptions(screen.getByLabelText("Область синхронизации"), kind);
    await user.selectOptions(screen.getByLabelText("Полнота выбранной области"), "complete");
    if (kind === "migrate_provider")
      await user.type(screen.getByLabelText(/Причина миграции/), "Reviewed migration");
    const manifest = structuredClone(syncManifest);
    if (kind === "add_provider" || kind === "migrate_provider")
      manifest.provider.namespace = "next";
    await upload(user, manifest);
    await user.click(screen.getByLabelText("Я проверил область, манифест и inventory"));
    await user.click(screen.getByRole("button", { name: "Начать синхронизацию" }));
    const scope =
      kind === "add_repository"
        ? { kind }
        : kind === "add_provider"
          ? { kind, repositoryId: syncIds.repository }
          : kind === "reconcile"
            ? { kind, repositoryId: syncIds.repository, providerNamespace: "ast" }
            : {
                kind,
                repositoryId: syncIds.repository,
                fromProviderNamespace: "ast",
                fromSnapshotId: syncIds.snapshot,
                reason: "Reviewed migration",
              };
    expect(onBegin).toHaveBeenCalledWith(
      expect.objectContaining({
        sourceScope: scope,
        expectedVersion: 3,
        baseRevisionId: syncIds.revision,
        syncPolicy: "whole-source-v1",
      }),
    );
    expect(onBegin.mock.calls[0]?.[0]).not.toHaveProperty("changeManifest");
  },
);

it("requires retained source5 extension, clears it on a composed partition and resets policy on return", async () => {
  const user = userEvent.setup();
  renderWithProviders(
    <BackendSourceScopeForm
      baseRevisionId={syncIds.revision}
      projectVersion={3}
      baseSchema="6"
      emptyBase={false}
      partitions={[
        {
          repositoryId: syncIds.repository,
          snapshotId: syncIds.snapshot,
          provider: { ...syncManifest.provider, profiles: sourceProfiles.slice(0, 5) },
        },
        {
          repositoryId: syncIds.repository,
          snapshotId: syncIds.incoming,
          provider: { ...syncManifest.provider, namespace: "composed" },
        },
      ]}
      onBegin={vi.fn()}
    />,
  );
  await user.click(screen.getByLabelText("Явно расширить events-service-v1 до composed-source-v1"));
  expect(screen.getByRole("option", { name: /Incremental:/ })).toBeDisabled();
  await user.selectOptions(screen.getByLabelText("Сохранённый раздел"), "1");
  expect(
    screen.queryByLabelText("Явно расширить events-service-v1 до composed-source-v1"),
  ).not.toBeInTheDocument();
  await user.selectOptions(
    screen.getByLabelText("Политика синхронизации"),
    "incremental-source-v1",
  );
  expect(screen.getByLabelText("ChangeManifest JSON")).toBeInTheDocument();
  await user.selectOptions(screen.getByLabelText("Сохранённый раздел"), "0");
  expect(screen.getByLabelText("Политика синхронизации")).toHaveValue("whole-source-v1");
  expect(
    screen.getByLabelText("Явно расширить events-service-v1 до composed-source-v1"),
  ).not.toBeChecked();
  expect(screen.queryByLabelText("ChangeManifest JSON")).not.toBeInTheDocument();
});

it("rejects oversized and unsafe files before any begin call", async () => {
  const user = userEvent.setup(),
    onBegin = vi.fn();
  renderWithProviders(
    <BackendSourceScopeForm
      baseRevisionId={syncIds.revision}
      projectVersion={3}
      baseSchema="6"
      emptyBase={false}
      partitions={[]}
      onBegin={onBegin}
    />,
  );
  const file = new File(["{}"], "large.json", { type: "application/json" });
  Object.defineProperty(file, "size", { value: 9 * 1024 * 1024 });
  const read = vi.spyOn(file, "text");
  await user.upload(screen.getByLabelText("Манифест JSON"), file);
  expect(await screen.findByRole("alert")).toHaveTextContent("превышает");
  expect(read).not.toHaveBeenCalled();
  await user.upload(
    screen.getByLabelText("Inventory JSON — девять категорий"),
    new File(['[{"knownCount":9007199254740993}]'], "unsafe.json", { type: "application/json" }),
  );
  expect(await screen.findByText(/Документ содержит числа/)).toBeInTheDocument();
  expect(onBegin).not.toHaveBeenCalled();
});

it("submits an explicit retained-five-profile extension without selecting unrelated partitions", async () => {
  const user = userEvent.setup(),
    onBegin = vi.fn();
  const retained = {
    repositoryId: syncIds.repository,
    snapshotId: syncIds.snapshot,
    provider: { ...syncManifest.provider, profiles: sourceProfiles.slice(0, 5) },
  };
  renderWithProviders(
    <BackendSourceScopeForm
      baseRevisionId={syncIds.revision}
      projectVersion={3}
      baseSchema="6"
      emptyBase={false}
      partitions={[
        retained,
        {
          repositoryId: syncIds.later,
          snapshotId: syncIds.incoming,
          provider: { ...syncManifest.provider, namespace: "unrelated" },
        },
      ]}
      onBegin={onBegin}
    />,
  );
  await user.click(screen.getByLabelText("Явно расширить events-service-v1 до composed-source-v1"));
  await user.selectOptions(screen.getByLabelText("Полнота выбранной области"), "complete");
  await upload(user);
  await user.click(screen.getByLabelText("Я проверил область, манифест и inventory"));
  await user.click(screen.getByRole("button", { name: "Начать синхронизацию" }));
  expect(onBegin).toHaveBeenCalledWith(
    expect.objectContaining({
      sourceScope: {
        kind: "reconcile",
        repositoryId: syncIds.repository,
        providerNamespace: "ast",
      },
      syncPolicy: "whole-source-v1",
      profileExtension: { fromProfile: "events-service-v1", toProfile: "composed-source-v1" },
      manifest: syncManifest,
    }),
  );
  expect(onBegin.mock.calls[0]?.[0]).not.toHaveProperty("changeManifest");
  expect(retained.provider.profiles).toHaveLength(5);
  expect(screen.getByText(new RegExp(`${syncIds.later} · unrelated`))).toBeInTheDocument();
});

function comparisonPartitions() {
  return [
    {
      repositoryId: syncIds.repository,
      snapshotId: syncIds.snapshot,
      provider: syncManifest.provider,
      snapshot: {
        ...syncSnapshot(),
        role: "active_source" as const,
        files: Array.from({ length: 17 }, (_, index) => ({
          ...syncManifest.snapshot.files[0]!,
          path: `existing/file-${index}.go`,
        })),
      },
    },
    {
      repositoryId: syncIds.later,
      snapshotId: syncIds.incoming,
      provider: { ...syncManifest.provider, namespace: "other" },
      snapshot: {
        ...syncSnapshot(),
        role: "active_source" as const,
        id: syncIds.incoming,
        repositoryId: syncIds.later,
        provider: { ...syncManifest.provider, namespace: "other" },
        files: [],
      },
    },
  ];
}

it.each(["add_provider", "add_repository"] as const)(
  "%s does not compare a new provider with a retained partition",
  async (kind) => {
    const user = userEvent.setup();
    const manifest = structuredClone(syncManifest);
    manifest.provider.namespace = "new-provider";
    manifest.snapshot.files = [];
    renderWithProviders(
      <BackendSourceScopeForm
        baseRevisionId={syncIds.revision}
        projectVersion={3}
        baseSchema="6"
        emptyBase={false}
        partitions={comparisonPartitions()}
        onBegin={vi.fn()}
      />,
    );
    await upload(user, manifest);
    expect(
      screen.getByText("Изменений хешей относительно выбранного снимка: 17"),
    ).toBeInTheDocument();

    await user.selectOptions(screen.getByLabelText("Область синхронизации"), kind);
    expect(
      screen.getByText("Новая область: предыдущего снимка для сравнения нет."),
    ).toBeInTheDocument();
    expect(
      screen.queryByText(/Изменений хешей относительно выбранного снимка/),
    ).not.toBeInTheDocument();
    expect(screen.queryByText(/deleted · existing\/file-/)).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Сохранённый раздел")).not.toBeInTheDocument();
    if (kind === "add_provider") {
      await user.selectOptions(screen.getByLabelText("Репозиторий"), syncIds.later);
      expect(
        screen.queryByText(/Изменений хешей относительно выбранного снимка/),
      ).not.toBeInTheDocument();
    }

    await user.selectOptions(screen.getByLabelText("Область синхронизации"), "reconcile");
    expect(
      screen.getByText("Изменений хешей относительно выбранного снимка: 17"),
    ).toBeInTheDocument();
  },
);

it.each(["reconcile", "migrate_provider"] as const)(
  "%s compares file hashes only with its explicitly selected snapshot",
  async (kind) => {
    const user = userEvent.setup();
    const partitions = comparisonPartitions();
    partitions[1]!.snapshot.files = [
      { ...syncManifest.snapshot.files[0]!, contentHash: "b".repeat(64) },
    ];
    renderWithProviders(
      <BackendSourceScopeForm
        baseRevisionId={syncIds.revision}
        projectVersion={3}
        baseSchema="6"
        emptyBase={false}
        partitions={partitions}
        onBegin={vi.fn()}
      />,
    );
    await user.selectOptions(screen.getByLabelText("Область синхронизации"), kind);
    await user.selectOptions(screen.getByLabelText("Сохранённый раздел"), "1");
    await upload(user);
    expect(
      screen.getByText("Изменений хешей относительно выбранного снимка: 1"),
    ).toBeInTheDocument();
    expect(
      screen.getByText(`modified · src/main.go · ${"b".repeat(64)} → ${syncHash}`),
    ).toBeInTheDocument();
    expect(screen.getByText(new RegExp(`снимок ${syncIds.incoming}`))).toBeInTheDocument();
    expect(screen.queryByText(/existing\/file-/)).not.toBeInTheDocument();

    await user.selectOptions(screen.getByLabelText("Сохранённый раздел"), "0");
    expect(
      screen.getByText("Изменений хешей относительно выбранного снимка: 18"),
    ).toBeInTheDocument();
  },
);
