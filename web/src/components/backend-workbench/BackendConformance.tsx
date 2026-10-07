import { useState } from "react";
import { Button, Checkbox, Paper, Stack, Text, TextInput, Title } from "@mantine/core";
import type {
  BackendAnalysisIdentityMapEntry,
  BackendChangeProposalDetail,
} from "@/api/generated/schemas";
import type { B43Start } from "./BackendChangePackage";
import { AnalysisValue } from "./BackendImpactReport";
import classes from "./BackendAnalysisControls.module.css";
export function BackendConformance({
  proposal,
  disabled,
  onStart,
}: {
  proposal: BackendChangeProposalDetail;
  disabled: boolean;
  onStart: B43Start;
}) {
  const [result, setResult] = useState("");
  const [rows, setRows] = useState<(BackendAnalysisIdentityMapEntry & { key: string })[]>([]);
  const [attachments, setAttachments] = useState<string[]>([]);
  const uuid = /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/;
  const valid =
    uuid.test(result) &&
    rows.every(
      (r) => uuid.test(r.proposalNodeId) && uuid.test(r.sourceNodeId) && r.reason.trim(),
    ) &&
    new Set(rows.map((r) => r.proposalNodeId)).size === rows.length &&
    new Set(rows.map((r) => r.sourceNodeId)).size === rows.length;
  function update(key: string, field: keyof BackendAnalysisIdentityMapEntry, value: string) {
    setRows((old) => old.map((r) => (r.key === key ? { ...r, [field]: value } : r)));
  }
  return (
    <section aria-label="Соответствие реализации">
      <Stack gap="sm">
        <Title order={4}>Соответствие реализации</Title>
        <Text size="sm">
          Точная исходная ревизия сопоставляется с сохранённым намерением. Runtime остаётся
          непроверенным; вложение подтверждает адрес теста, а не его выполнение.
        </Text>
        <fieldset disabled={disabled} style={{ border: 0, padding: 0, minWidth: 0 }}>
          <Stack gap="sm">
            <TextInput
              label="Ревизия реализованного источника"
              value={result}
              onChange={(e) => setResult(e.currentTarget.value)}
              description="UUID сохранённой source-ревизии, без автоматического выбора текущей."
            />
            <Text size="sm">
              Новые узлы требуют явного соответствия UUID. Совпадение имени не доказывает
              идентичность.
            </Text>
            {rows.map((r, i) => (
              <Paper withBorder p="sm" key={r.key}>
                <Stack gap="xs">
                  <div className={classes.grid}>
                    <TextInput
                      label={`UUID узла предложения ${i + 1}`}
                      value={r.proposalNodeId}
                      onChange={(e) => update(r.key, "proposalNodeId", e.currentTarget.value)}
                    />
                    <TextInput
                      label={`UUID узла источника ${i + 1}`}
                      value={r.sourceNodeId}
                      onChange={(e) => update(r.key, "sourceNodeId", e.currentTarget.value)}
                    />
                    <TextInput
                      label={`Основание соответствия ${i + 1}`}
                      value={r.reason}
                      maxLength={4096}
                      onChange={(e) => update(r.key, "reason", e.currentTarget.value)}
                    />
                  </div>
                  <Button
                    variant="subtle"
                    onClick={() => setRows((old) => old.filter((x) => x.key !== r.key))}
                  >
                    Удалить соответствие {i + 1}
                  </Button>
                </Stack>
              </Paper>
            ))}
            <Button
              variant="default"
              disabled={rows.length >= 10000}
              onClick={() =>
                setRows((old) => [
                  ...old,
                  { key: crypto.randomUUID(), proposalNodeId: "", sourceNodeId: "", reason: "" },
                ])
              }
            >
              Добавить соответствие узлов
            </Button>
            {proposal.revision.criteria
              .filter((c) => c.kind === "test_attachment")
              .map((c) => (
                <Paper key={c.key} withBorder p="sm">
                  <Stack gap="xs">
                    <Checkbox
                      label={`Приложить ${c.key}`}
                      checked={attachments.includes(c.key)}
                      onChange={(e) => {
                        const checked = e.currentTarget.checked;
                        setAttachments((old) =>
                          checked ? [...old, c.key] : old.filter((k) => k !== c.key),
                        );
                      }}
                    />
                    <Text size="sm">{c.description}</Text>
                    <Text size="sm">
                      {c.attachment.kind === "source"
                        ? `Источник: ${c.attachment.file}, строки ${c.attachment.startLine}–${c.attachment.endLine} · ${c.attachment.revisionId}`
                        : c.attachment.kind === "artifact_v3"
                          ? `Артефакт ${c.attachment.namespacedArtifact.namespace.scope} · ${c.attachment.namespacedArtifact.namespace.installationId} · ${c.attachment.namespacedArtifact.pin.kind}:${c.attachment.namespacedArtifact.pin.id}`
                          : `Артефакт: ${c.attachment.artifact.kind} · ${c.attachment.artifact.id} · ревизия ${c.attachment.artifact.revisionId}`}
                    </Text>
                    <AnalysisValue label={`Точное вложение ${c.key}`} value={c.attachment} />
                  </Stack>
                </Paper>
              ))}
            <Text size="xs">
              Вложение отправляется с точным сохранённым locator и хешем. Для изменения вложения
              сначала измените критерий и сохраните новый черновик.
            </Text>
            {!valid && (
              <Text size="sm">
                Укажите корректную ревизию и уникальные UUID обеих сторон с основанием каждой связи.
              </Text>
            )}
            <Button
              disabled={!valid || disabled}
              onClick={() =>
                void onStart({
                  kind: "conformance",
                  changeProposal: {
                    proposalId: proposal.proposal.id,
                    proposalRevisionId: proposal.revision.id,
                  },
                  resultRevisionId: result,
                  identityMap: rows.map(({ proposalNodeId, sourceNodeId, reason }) => ({
                    proposalNodeId,
                    sourceNodeId,
                    reason,
                  })),
                  testAttachments: proposal.revision.criteria
                    .filter((c) => c.kind === "test_attachment")
                    .filter((c) => attachments.includes(c.key))
                    .map((c) => ({ criterionKey: c.key, attachment: c.attachment })),
                  limits: {},
                  observationMode: "none",
                  idempotencyKey: crypto.randomUUID(),
                })
              }
            >
              Проверить соответствие
            </Button>
          </Stack>
        </fieldset>
      </Stack>
    </section>
  );
}
