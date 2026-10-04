import { useState } from "react";
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
  TextInput,
  Title,
} from "@mantine/core";
import type {
  BackendComposedImportSession,
  BackendImportCommand,
  BackendProviderAssertion,
  BackendSourceAssertionConflict,
} from "@/api/generated/schemas";
import { validateSessionProof } from "./backendSourceInputs";

const wrap = { overflowWrap: "anywhere" as const, whiteSpace: "pre-wrap" as const };
export function sourceAssertionAddress(a: BackendProviderAssertion) {
  return {
    repositoryId: a.owner.repositoryId,
    providerNamespace: a.owner.providerNamespace,
    recordType: a.recordType,
    externalKey: a.externalKey,
    expectedId: a.recordId,
    assertionHash: a.assertionHash,
  };
}
export function BackendAssertionDecisionForm({
  conflict,
  previewVersion,
  disabled,
  changedPin,
  onResolve,
}: {
  conflict: BackendSourceAssertionConflict;
  previewVersion: number;
  disabled?: boolean;
  changedPin?: boolean;
  onResolve: (command: BackendImportCommand) => void;
}) {
  const [choice, setChoice] = useState("");
  const [reason, setReason] = useState("");
  const selected = conflict.contenders[Number(choice)];
  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        if (disabled || choice === "" || !selected || !reason.trim()) return;
        onResolve({
          op: "resolve_assertion",
          resolution: {
            decisionId: crypto.randomUUID(),
            recordType: conflict.recordType,
            id: conflict.id,
            property: conflict.property,
            conflictHash: conflict.conflictHash,
            select: {
              repositoryId: selected.owner.repositoryId,
              providerNamespace: selected.owner.providerNamespace,
              assertionHash: selected.assertionHash,
            },
            reason: reason.trim(),
          },
        });
      }}
    >
      <Stack gap="sm" component="section" aria-label={`Конфликт ${conflict.id}`}>
        <Title order={4}>Выбор утверждения · Preview {previewVersion}</Title>
        {changedPin && (
          <Alert color="yellow">
            После пересчёта конфликт изменился. Просмотрите новые основания и подтвердите выбор
            заново.
          </Alert>
        )}
        <Text size="sm" style={wrap}>
          {conflict.recordType} · {conflict.id} · {conflict.conflictHash}
        </Text>
        <Code block style={wrap}>
          {JSON.stringify(conflict.property, null, 2)}
        </Code>
        {conflict.contenders.map((contender, i) => (
          <Stack
            key={`${contender.owner.repositoryId}/${contender.owner.providerNamespace}/${contender.assertionHash}`}
            gap="xs"
          >
            <Group>
              <Badge variant="light">
                {i + 1}. {contender.owner.providerNamespace}
              </Badge>
              <Text size="xs">
                Собственное: {contender.currentness.own.status}; зависимости:{" "}
                {contender.currentness.dependency.status}
              </Text>
            </Group>
            <Text size="xs" style={wrap}>
              {contender.owner.repositoryId} · {contender.assertionHash}
            </Text>
            <Code block style={wrap}>
              {contender.value.present
                ? JSON.stringify(contender.value.value, null, 2)
                : "Отсутствует"}
            </Code>
            <Text size="xs" style={wrap}>
              Основания: {contender.evidenceIds.join(", ") || "не предоставлены"}
            </Text>
            <details>
              <summary>Текущесть свойств и причины</summary>
              <Code block style={wrap}>
                {JSON.stringify(contender.currentness, null, 2)}
              </Code>
            </details>
          </Stack>
        ))}
        <NativeSelect
          label={`Утверждение для ${conflict.id}`}
          value={choice}
          disabled={disabled}
          data={[
            { value: "", label: "Выберите предложенное утверждение" },
            ...conflict.contenders.map((contender, i) => ({
              value: String(i),
              label: `${contender.owner.providerNamespace} · ${contender.assertionHash}`,
            })),
          ]}
          onChange={(event) => setChoice(event.currentTarget.value)}
        />
        <TextInput
          label="Причина выбора утверждения"
          value={reason}
          maxLength={4096}
          disabled={disabled}
          required
          onChange={(event) => setReason(event.currentTarget.value)}
        />
        <Button type="submit" disabled={disabled || choice === "" || !reason.trim()}>
          Отправить выбор утверждения
        </Button>
      </Stack>
    </form>
  );
}

export function BackendClaimIdentityForm({
  session,
  commands,
  offers,
  loading,
  nextCursor,
  prerequisite,
  disabled,
  onLoad,
  onClaim,
}: {
  session: BackendComposedImportSession;
  commands: readonly BackendImportCommand[];
  offers: readonly BackendProviderAssertion[];
  loading: boolean;
  nextCursor: string;
  prerequisite?: string;
  disabled?: boolean;
  onLoad: (recordType: "node" | "edge", more?: boolean) => void;
  onClaim: (command: BackendImportCommand) => void;
}) {
  const incoming = commands.flatMap<{
    recordType: "node" | "edge";
    externalKey: string;
    kind: string;
  }>((command) =>
    command.op === "upsert_node"
      ? [{ recordType: "node", externalKey: command.node.externalKey, kind: command.node.kind }]
      : command.op === "upsert_edge"
        ? [{ recordType: "edge", externalKey: command.edge.externalKey, kind: command.edge.kind }]
        : [],
  );
  const [subjectIndex, setSubjectIndex] = useState("");
  const [targetIndex, setTargetIndex] = useState("");
  const [reason, setReason] = useState("");
  const [evidenceKeys, setEvidenceKeys] = useState<string[]>([]);
  const subject = incoming[Number(subjectIndex)];
  const eligible = offers.filter(
    (a) =>
      subject &&
      a.recordType === subject.recordType &&
      a.owner.repositoryId === session.repositoryId &&
      a.payload.kind === subject.kind,
  );
  const target = eligible.find((a) => JSON.stringify(sourceAssertionAddress(a)) === targetIndex);
  const proofs = commands.flatMap((command) => {
    if (
      command.op !== "upsert_evidence" ||
      subjectIndex === "" ||
      !subject ||
      command.evidence.subjectType !== subject.recordType ||
      command.evidence.subjectKey !== subject.externalKey
    )
      return [];
    try {
      validateSessionProof([command], session);
      return [command.evidence.externalKey];
    } catch {
      return [];
    }
  });
  const selectedProof = evidenceKeys.filter((key) => proofs.includes(key));
  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        if (
          disabled ||
          subjectIndex === "" ||
          targetIndex === "" ||
          !subject ||
          !target ||
          !selectedProof.length ||
          !reason.trim()
        )
          return;
        onClaim({
          op: "claim_identity",
          claimIdentity: {
            decisionId: crypto.randomUUID(),
            recordType: subject.recordType,
            externalKey: subject.externalKey,
            target: sourceAssertionAddress(target),
            reason: reason.trim(),
            evidenceKeys: selectedProof,
          },
        });
      }}
    >
      <Stack component="section" aria-label="Общая идентичность" gap="sm">
        <Title order={4}>Связать входящий объект с общим UUID</Title>
        <Text size="sm">Решение добавится перед записями и основаниями загруженного пакета.</Text>
        <NativeSelect
          label="Входящий объект"
          disabled={disabled}
          value={subjectIndex}
          data={[
            { value: "", label: "Выберите объект из пакета" },
            ...incoming.map((item, i) => ({
              value: String(i),
              label: `${item.recordType} · ${item.externalKey}`,
            })),
          ]}
          onChange={(event) => {
            setSubjectIndex(event.currentTarget.value);
            setTargetIndex("");
            setEvidenceKeys([]);
          }}
        />
        <Button
          variant="default"
          disabled={disabled || loading || subjectIndex === "" || !subject}
          onClick={() => {
            if (subject) {
              setTargetIndex("");
              onLoad(subject.recordType);
            }
          }}
        >
          Загрузить утверждения базы
        </Button>
        {prerequisite && <Alert color="yellow">{prerequisite}</Alert>}
        <NativeSelect
          label="Точное утверждение базы"
          disabled={disabled || loading}
          value={targetIndex}
          data={[
            { value: "", label: "Выберите утверждение того же репозитория и типа" },
            ...eligible.map((a) => ({
              value: JSON.stringify(sourceAssertionAddress(a)),
              label: `${a.owner.providerNamespace} · ${a.externalKey} · ${a.recordId}`,
            })),
          ]}
          onChange={(event) => setTargetIndex(event.currentTarget.value)}
        />
        {targetIndex !== "" && target && (
          <Code block style={wrap}>
            {JSON.stringify(sourceAssertionAddress(target), null, 2)}
          </Code>
        )}
        {nextCursor && (
          <Button
            variant="subtle"
            disabled={disabled || loading || !subject}
            onClick={() => subject && onLoad(subject.recordType, true)}
          >
            Ещё утверждения базы
          </Button>
        )}
        <Checkbox.Group
          label="Собственные основания входящего объекта"
          value={selectedProof}
          onChange={setEvidenceKeys}
        >
          <Stack gap="xs">
            {proofs.map((key) => (
              <Checkbox key={key} value={key} label={key} disabled={disabled} />
            ))}
          </Stack>
        </Checkbox.Group>
        {subjectIndex !== "" && !proofs.length && (
          <Text size="sm">
            В пакете нет основания этого объекта с репозиторием, снимком и хешем файла текущей
            сессии.
          </Text>
        )}
        <TextInput
          label="Причина общей идентичности"
          required
          maxLength={4096}
          value={reason}
          disabled={disabled}
          onChange={(event) => setReason(event.currentTarget.value)}
        />
        <Button
          type="submit"
          disabled={
            disabled ||
            loading ||
            subjectIndex === "" ||
            !target ||
            !selectedProof.length ||
            !reason.trim()
          }
        >
          Добавить claim_identity в пакет
        </Button>
      </Stack>
    </form>
  );
}
