import { useState } from "react";
import { Alert, Badge, Code, Group, NativeSelect, Stack, Text, Title } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { getBackendImportChanges } from "@/api/generated/backend-projects/backend-projects";
import type {
  BackendComposedImportChangeItem,
  BackendImportCommand,
  BackendImportPreviewResponse,
  GetBackendImportChangesRecordType,
} from "@/api/generated/schemas";
import { BackendAssertionDecisionForm } from "./BackendAssertionDecisionForm";
import { LoadState, Pages } from "./BackendGraphInventory";
import { ComparisonSummary } from "./BackendRevisionCompare";

const wrap = { overflowWrap: "anywhere" as const, whiteSpace: "pre-wrap" as const };
export function conflictAddress(item: { recordType: string; id: string; property: unknown }) {
  return `${item.recordType}/${item.id}/${JSON.stringify(item.property)}`;
}

export function BackendSyncPreview({
  projectId,
  preview,
  disabled,
  lastChoices,
  onResolve,
}: {
  projectId: string;
  preview: BackendImportPreviewResponse;
  disabled?: boolean;
  lastChoices: Readonly<Record<string, string>>;
  onResolve: (command: BackendImportCommand) => void;
}) {
  const [recordType, setRecordType] = useState<GetBackendImportChangesRecordType>(
    preview.state === "needs_resolution" ? "assertion_conflict" : "source",
  );
  const [cursors, setCursors] = useState([""]);
  const cursor = cursors.at(-1) ?? "";
  const query = useQuery({
    queryKey: [
      "backend-sync-changes",
      projectId,
      preview.sessionId,
      preview.version,
      preview.candidateHash,
      recordType,
      cursor,
    ],
    queryFn: ({ signal }) =>
      getBackendImportChanges(
        projectId,
        preview.sessionId,
        { previewVersion: preview.version, recordType, limit: 100, cursor },
        { signal },
      ),
    retry: false,
  });
  const response = query.data?.status === 200 ? query.data.data : undefined;
  const page =
    response?.sessionId === preview.sessionId &&
    response.previewVersion === preview.version &&
    response.candidateHash === preview.candidateHash
      ? response
      : undefined;
  const affected = "affectedScope" in preview ? preview.affectedScope : undefined;
  return (
    <Stack component="section" aria-label="Обзор синхронизации">
      <Group>
        <Title order={3}>Сохранённый Preview</Title>
        <Badge>
          {preview.state} · версия {preview.version}
        </Badge>
      </Group>
      <Code block style={wrap}>
        {preview.candidateHash ??
          "Готового кандидата пока нет — изучите решения этой версии Preview."}
      </Code>
      <Text size="sm">
        Объекты {preview.summary.nodes} · связи {preview.summary.edges} · основания{" "}
        {preview.summary.evidence} · не разрешено {preview.summary.unresolved}
      </Text>
      {preview.comparisonSummary && <ComparisonSummary summary={preview.comparisonSummary} />}
      {preview.diagnostics.map((d, index) => (
        <Alert key={index} color="yellow" style={wrap}>
          {d.code} · {d.path}: {d.message}
        </Alert>
      ))}
      {affected && (
        <Stack gap="xs">
          <Title order={4}>Граница incremental</Title>
          <Text size="sm">
            Запись: {affected.affected.length} · проверки: {affected.validationDependencies.length}{" "}
            · внешние зависимости: {affected.foreignDependencies.length} · нетронутые:{" "}
            {affected.untouchedCount}
          </Text>
          {(
            [
              ["Разрешённая область записи", affected.affected],
              ["Зависимости только для проверки", affected.validationDependencies],
              ["Внешние зависимости только для чтения", affected.foreignDependencies],
            ] as const
          ).map(([label, records]) => (
            <details key={label}>
              <summary>{label}</summary>
              {records.map((record) => (
                <Text size="xs" key={`${record.recordType}/${record.id}`} style={wrap}>
                  {record.recordType} · {record.id}
                </Text>
              ))}
            </details>
          ))}
          {affected.gaps.map((gap) => (
            <Text key={gap} size="sm" style={wrap}>
              {gap}
            </Text>
          ))}
          {affected.availabilityChanges.map((gap) => (
            <Text key={gap} size="sm" style={wrap}>
              {gap}
            </Text>
          ))}
        </Stack>
      )}
      <NativeSelect
        label="Детали синхронизации"
        value={recordType}
        data={[
          { value: "source", label: "Изменения исходников" },
          { value: "assertion_conflict", label: "Расхождения утверждений" },
          { value: "claim_identity", label: "Общие идентичности" },
          { value: "migration", label: "Миграция провайдера" },
          { value: "identity", label: "Переименования" },
          { value: "deletion", label: "Решения об удалении" },
        ]}
        onChange={(event) => {
          setRecordType(event.currentTarget.value as GetBackendImportChangesRecordType);
          setCursors([""]);
        }}
      />
      <LoadState query={query} label="решений Preview" />
      {response && !page && (
        <Alert color="yellow">
          Получена другая версия деталей. Перечитайте статус и сохранённый Preview.
        </Alert>
      )}
      {page?.items.length === 0 && <Text c="dimmed">Изменений этого типа нет</Text>}
      {page?.items.map((item, index) => (
        <SyncChange
          key={`${cursor}/${index}`}
          item={item}
          previewVersion={preview.version}
          disabled={disabled || query.isFetching}
          lastChoices={lastChoices}
          onResolve={onResolve}
        />
      ))}
      <Pages
        label="решения синхронизации"
        cursors={cursors}
        next={page?.nextCursor}
        busy={query.isFetching || !!disabled}
        setCursors={setCursors}
      />
    </Stack>
  );
}

function SyncChange({
  item,
  previewVersion,
  disabled,
  lastChoices,
  onResolve,
}: {
  item: BackendComposedImportChangeItem;
  previewVersion: number;
  disabled?: boolean;
  lastChoices: Readonly<Record<string, string>>;
  onResolve: (command: BackendImportCommand) => void;
}) {
  if (item.recordType === "assertion_conflict") {
    const conflict = item.assertionConflict,
      previous = lastChoices[conflictAddress(conflict)];
    return (
      <BackendAssertionDecisionForm
        key={`${previewVersion}/${conflict.conflictHash}`}
        conflict={conflict}
        previewVersion={previewVersion}
        disabled={disabled}
        changedPin={!!previous && previous !== conflict.conflictHash}
        onResolve={onResolve}
      />
    );
  }
  const value =
    item.recordType === "source"
      ? item.source
      : item.recordType === "identity"
        ? item.identity
        : item.recordType === "deletion"
          ? item.deletion
          : item.recordType === "claim_identity"
            ? item.claimIdentity
            : item.migration;
  return (
    <Stack gap="xs">
      <Badge variant="light" w="fit-content">
        {item.recordType}
      </Badge>
      <Code block style={wrap}>
        {JSON.stringify(value, null, 2)}
      </Code>
    </Stack>
  );
}
