import { useEffect, useState } from "react";
import {
  Alert,
  Badge,
  Button,
  Checkbox,
  Code,
  Group,
  NativeSelect,
  Stack,
  Text,
  Textarea,
  Title,
} from "@mantine/core";
import type {
  BackendChangeManifest,
  BackendInventoryItem,
  BackendSourceManifest,
  BackendSourceScope,
  BeginBackendImportRequest,
} from "@/api/generated/schemas";
import { BackendSourceFileInput } from "./BackendSourceFileInput";
import {
  buildSourceBegin,
  extensionRequired,
  parseChangeManifest,
  parseSourceInventory,
  parseSourceManifest,
  sourceFileChanges,
  type SyncPartition,
} from "./backendSourceInputs";

export type BackendSourceScopeFormProps = {
  baseRevisionId: string;
  projectVersion: number;
  baseSchema: string;
  emptyBase: boolean;
  partitions: readonly SyncPartition[];
  disabled?: boolean;
  onBegin: (request: BeginBackendImportRequest) => void;
  onDirty?: (value: boolean) => void;
};
const wrap = { overflowWrap: "anywhere" as const };

export function BackendSourceScopeForm(props: BackendSourceScopeFormProps) {
  const { onDirty } = props;
  const [kind, setKind] = useState<BackendSourceScope["kind"]>(
    props.emptyBase ? "add_repository" : "reconcile",
  );
  const [partitionKey, setPartitionKey] = useState("0");
  const [repositoryId, setRepositoryId] = useState(props.partitions[0]?.repositoryId ?? "");
  const [reason, setReason] = useState("");
  const [policy, setPolicy] = useState<"whole-source-v1" | "incremental-source-v1">(
    "whole-source-v1",
  );
  const [extension, setExtension] = useState(false);
  const [scopeStatus, setScopeStatus] = useState<"complete" | "partial">("partial");
  const [gaps, setGaps] = useState("");
  const [reviewed, setReviewed] = useState(false);
  const [manifest, setManifest] = useState<BackendSourceManifest>();
  const [inventory, setInventory] = useState<BackendInventoryItem[]>();
  const [changeManifest, setChangeManifest] = useState<BackendChangeManifest>();
  const [loading, setLoading] = useState<Record<string, boolean>>({});
  const [failure, setFailure] = useState("");
  const partition = props.partitions[Number(partitionKey)];
  const sourceScope: BackendSourceScope =
    kind === "add_repository"
      ? { kind }
      : kind === "add_provider"
        ? { kind, repositoryId }
        : kind === "reconcile"
          ? {
              kind,
              repositoryId: partition?.repositoryId ?? "",
              providerNamespace: partition?.provider.namespace ?? "",
            }
          : {
              kind,
              repositoryId: partition?.repositoryId ?? "",
              fromProviderNamespace: partition?.provider.namespace ?? "",
              fromSnapshotId: partition?.snapshotId ?? "",
              reason: reason.trim(),
            };
  const needsExtension = extensionRequired(props.baseSchema, sourceScope, props.partitions);
  const canIncremental =
    kind === "reconcile" &&
    props.baseSchema === "6" &&
    !needsExtension &&
    partition?.provider.profiles.length === 6;
  const supported =
    ["5", "6"].includes(props.baseSchema) || (props.baseSchema === "1" && props.emptyBase);
  const fileBusy = Object.values(loading).some(Boolean);
  const dirty = !!manifest || !!inventory || !!changeManifest || fileBusy;
  useEffect(() => {
    onDirty?.(dirty);
  }, [dirty, onDirty]);
  function resetSelection() {
    setExtension(false);
    setPolicy("whole-source-v1");
    setReviewed(false);
    setFailure("");
  }
  const makeRequest = () => {
    if (!manifest || !inventory) throw new Error("Загрузите манифест и inventory.");
    return buildSourceBegin({
      ...props,
      sourceScope,
      scopeStatus:
        scopeStatus === "complete"
          ? { status: "complete", gaps: [] }
          : {
              status: "partial",
              gaps: gaps
                .split("\n")
                .map((line) => line.trim())
                .filter(Boolean),
            },
      policy,
      extension,
      manifest,
      inventory,
      changeManifest,
      idempotencyKey: crypto.randomUUID(),
    });
  };
  const comparesPartition = kind === "reconcile" || kind === "migrate_provider";
  const difference =
    comparesPartition && manifest && partition?.snapshot
      ? sourceFileChanges(partition.snapshot.files, manifest.snapshot.files)
      : undefined;
  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        if (props.disabled || fileBusy || !reviewed) return;
        try {
          setFailure("");
          props.onBegin(makeRequest());
        } catch (error) {
          setFailure(error instanceof Error ? error.message : "Проверьте входные данные.");
        }
      }}
    >
      <Stack gap="md" component="section" aria-label="Настройка синхронизации">
        <Title order={3}>Настройка источника</Title>
        <Text size="sm" style={wrap}>
          База {props.baseRevisionId} · версия проекта {props.projectVersion} · schema{" "}
          {props.baseSchema}
        </Text>
        {!supported && (
          <Alert color="yellow">
            Для этой базы сначала нужен существующий соседний переход профиля до events-service-v1.
            Подготовленную сессию можно открыть в списке импортов ниже.
          </Alert>
        )}
        <NativeSelect
          label="Область синхронизации"
          value={kind}
          disabled={props.disabled || !supported}
          data={[
            { value: "add_repository", label: "Добавить репозиторий" },
            ...(!props.emptyBase
              ? [
                  { value: "add_provider", label: "Добавить провайдера" },
                  { value: "reconcile", label: "Обновить раздел" },
                  { value: "migrate_provider", label: "Миграция провайдера" },
                ]
              : []),
          ]}
          onChange={(event) => {
            setKind(event.currentTarget.value as BackendSourceScope["kind"]);
            resetSelection();
          }}
        />
        {kind === "add_provider" && (
          <NativeSelect
            label="Репозиторий"
            value={repositoryId}
            disabled={props.disabled}
            data={[...new Set(props.partitions.map((part) => part.repositoryId))].map((id) => ({
              value: id,
              label: id,
            }))}
            onChange={(event) => {
              setRepositoryId(event.currentTarget.value);
              resetSelection();
            }}
          />
        )}
        {(kind === "reconcile" || kind === "migrate_provider") && (
          <>
            <NativeSelect
              label="Сохранённый раздел"
              value={partitionKey}
              disabled={props.disabled}
              data={props.partitions.map((part, index) => ({
                value: String(index),
                label: `${part.provider.namespace} · ${part.repositoryId} · ${part.provider.profiles.length} профилей`,
              }))}
              onChange={(event) => {
                setPartitionKey(event.currentTarget.value);
                resetSelection();
              }}
            />
            {partition && (
              <Text size="sm" style={wrap}>
                Провайдер {partition.provider.name} {partition.provider.version} ·{" "}
                {partition.provider.method} · снимок {partition.snapshotId}
              </Text>
            )}
          </>
        )}
        {kind === "migrate_provider" && (
          <>
            <Textarea
              label="Причина миграции"
              required
              minRows={2}
              maxLength={4096}
              disabled={props.disabled}
              value={reason}
              onChange={(event) => {
                setReason(event.currentTarget.value);
                setReviewed(false);
              }}
            />
            <Text size="sm">
              Прежний namespace, его утверждения и основания останутся в истории и составе
              источника. Входящий манифест должен объявлять новый namespace.
            </Text>
          </>
        )}
        {needsExtension && (
          <Checkbox
            label="Явно расширить events-service-v1 до composed-source-v1"
            checked={extension}
            disabled={props.disabled}
            onChange={(event) => {
              setExtension(event.currentTarget.checked);
              setPolicy("whole-source-v1");
              setReviewed(false);
            }}
          />
        )}
        <NativeSelect
          label="Политика синхронизации"
          value={policy}
          disabled={props.disabled}
          data={[
            { value: "whole-source-v1", label: "Весь выбранный раздел" },
            {
              value: "incremental-source-v1",
              label: "Incremental: затронутый подграф",
              disabled: !canIncremental,
            },
          ]}
          onChange={(event) => {
            setPolicy(event.currentTarget.value as typeof policy);
            setReviewed(false);
          }}
        />
        <NativeSelect
          label="Полнота выбранной области"
          value={scopeStatus}
          disabled={props.disabled}
          data={[
            { value: "partial", label: "Частичная — есть пробелы" },
            { value: "complete", label: "Полная" },
          ]}
          onChange={(event) => {
            setScopeStatus(event.currentTarget.value as typeof scopeStatus);
            setReviewed(false);
          }}
        />
        {scopeStatus === "partial" && (
          <Textarea
            label="Пробелы области, по одному на строку"
            value={gaps}
            required
            minRows={2}
            maxLength={4096}
            disabled={props.disabled}
            onChange={(event) => {
              setGaps(event.currentTarget.value);
              setReviewed(false);
            }}
          />
        )}
        <BackendSourceFileInput
          label="Манифест JSON"
          disabled={props.disabled || !supported}
          parse={parseSourceManifest}
          onValue={(value) => {
            setManifest(value);
            setReviewed(false);
          }}
          onBusy={(busy) => setLoading((state) => ({ ...state, manifest: busy }))}
        />
        <BackendSourceFileInput
          label="Inventory JSON — девять категорий"
          disabled={props.disabled || !supported}
          parse={parseSourceInventory}
          onValue={(value) => {
            setInventory(value);
            setReviewed(false);
          }}
          onBusy={(busy) => setLoading((state) => ({ ...state, inventory: busy }))}
        />
        {policy === "incremental-source-v1" && (
          <BackendSourceFileInput
            label="ChangeManifest JSON"
            disabled={props.disabled}
            parse={parseChangeManifest}
            onValue={(value) => {
              setChangeManifest(value);
              setReviewed(false);
            }}
            onBusy={(busy) => setLoading((state) => ({ ...state, changes: busy }))}
          />
        )}
        {manifest && (
          <Stack gap="xs">
            <Group>
              <Badge>{manifest.snapshot.consistency}</Badge>
              <Text size="sm">
                {manifest.repositoryName} · {manifest.provider.namespace} ·{" "}
                {manifest.snapshot.files.length} файлов
              </Text>
            </Group>
            <Text size="sm">
              {manifest.provider.name} {manifest.provider.version} · {manifest.provider.method} ·{" "}
              {manifest.provider.profiles.join(", ")}
            </Text>
            {manifest.provider.limitations.map((gap) => (
              <Text size="xs" key={gap}>
                {gap}
              </Text>
            ))}
            {difference ? (
              <>
                <Text size="sm">
                  Изменений хешей относительно выбранного снимка: {difference.length}
                </Text>
                {difference.slice(0, 100).map((file) => (
                  <Text size="xs" key={file.path} style={wrap}>
                    {file.kind} · {file.path} · {"beforeHash" in file ? file.beforeHash : "—"} →{" "}
                    {"afterHash" in file ? file.afterHash : "—"}
                  </Text>
                ))}
                {difference.length > 100 && (
                  <Text size="xs">Показаны первые 100; полный diff будет в Preview.</Text>
                )}
              </>
            ) : (
              <Text size="sm">
                {comparesPartition
                  ? "Снимок выбранного раздела недоступен для сравнения."
                  : "Новая область: предыдущего снимка для сравнения нет."}
              </Text>
            )}
          </Stack>
        )}
        {inventory && (
          <Text size="sm">
            Inventory:{" "}
            {inventory
              .map((row) => `${row.category}: ${row.knownCount}, ${row.status}`)
              .join(" · ")}
          </Text>
        )}
        <details>
          <summary>Сохраняемые разделы</summary>
          {props.partitions.map((part) => (
            <Text key={`${part.repositoryId}/${part.provider.namespace}`} size="xs" style={wrap}>
              {part.repositoryId} · {part.provider.namespace} · {part.snapshotId}
            </Text>
          ))}
        </details>
        <Code block style={wrap}>
          {JSON.stringify(sourceScope, null, 2)}
        </Code>
        <Checkbox
          label="Я проверил область, манифест и inventory"
          checked={reviewed}
          disabled={props.disabled || !manifest || !inventory || fileBusy}
          onChange={(event) => setReviewed(event.currentTarget.checked)}
        />
        {failure && (
          <Alert color="red" role="alert">
            {failure}
          </Alert>
        )}
        <Button
          type="submit"
          disabled={
            !supported || props.disabled || fileBusy || !manifest || !inventory || !reviewed
          }
        >
          Начать синхронизацию
        </Button>
      </Stack>
    </form>
  );
}
