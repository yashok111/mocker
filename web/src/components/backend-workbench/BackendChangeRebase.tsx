import { LoadState } from "./BackendReadUI";
import { rebaseReplacementSchema } from "./backendRebaseSchema";
import { readBackendNode, readBackendGraph } from "./backendGraphReads";
import { readBackendCoverage } from "./backendGraphReads";
import { useEffect, useRef, useState } from "react";
import {
  Alert,
  Button,
  Group,
  NativeSelect,
  Paper,
  Stack,
  Text,
  Textarea,
  TextInput,
  Title,
} from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import {
  getBackendRevision,
  previewBackendChangeProposalRebase,
  useListBackendRevisions,
} from "@/api/generated/backend-projects/backend-projects";
import type {
  BackendChangeProposalDetail,
  BackendChangeProposalRebaseCandidate,
  BackendChangeProposalApplyResult,
  BackendChangeRebaseConflict,
  BackendChangeRebaseResolution,
  BackendChangeProposalCommand,
  BackendChangeRebaseIdentityResolution,
  PreviewBackendChangeProposalRebaseRequest,
  BackendEffectiveIdentity,
} from "@/api/generated/schemas";
import {
  makeAnalysisAttempt,
  useBackendAnalysisRecovery,
  type RebaseReconciliation,
} from "./backendAnalysisRecovery";
import { verifyRebaseAcceptance } from "./backendChangeReads";
import { BackendChangeSchemaField } from "./BackendChangeSchemaField";
import { BackendChangeCommandForms, changeCommandLabels } from "./BackendChangeCommandForms";
import { backendChangeSchemas } from "./backendChangeSchema";
import { changeSchemaDefault, changeSchemaIssues } from "./backendChangeFormModel";
import type { ChangeJSON } from "./backendChangeSchemaTypes";
import { AnalysisValue } from "./BackendImpactReport";
import { changeMessage } from "./backendChangeRecovery";
export function BackendChangeRebase({
  projectId,
  detail,
  disabled = false,
  identities = [],
  onSaved,
  onDirty,
}: {
  projectId: string;
  detail: BackendChangeProposalDetail;
  disabled?: boolean;
  identities?: BackendEffectiveIdentity[];
  onSaved: (detail: BackendChangeProposalDetail) => void;
  onDirty?: (value: boolean) => void;
}) {
  const recovery = useBackendAnalysisRecovery(projectId);
  const recovered =
    recovery.lastReconciled?.detail.proposal.id === detail.proposal.id &&
    recovery.lastReconciled.detail.proposal.version >= detail.proposal.version
      ? recovery.lastReconciled
      : null;
  const [reconciledBasis, setBasis] = useState<BackendChangeProposalDetail | null>(
    recovered?.detail ?? null,
  );
  const basis =
    reconciledBasis && reconciledBasis.proposal.version >= detail.proposal.version
      ? reconciledBasis
      : detail;
  const [previousChoices, setPreviousChoices] = useState<
    PreviewBackendChangeProposalRebaseRequest | undefined
  >(recovered?.attempt.input);
  const [newBase, setNewBase] = useState(recovered?.attempt.input.newBaseRevisionId ?? "");
  const [resolutions, setResolutions] = useState<BackendChangeRebaseResolution[]>([]),
    [correspondence, setCorrespondence] = useState<BackendChangeRebaseIdentityResolution[]>([]),
    [repairs, setRepairs] = useState<BackendChangeProposalCommand[]>([]);
  const [conflicts, setConflicts] = useState<BackendChangeRebaseConflict[]>([]);
  const [candidate, setCandidate] = useState<{
    value: BackendChangeProposalRebaseCandidate;
    body: string;
  }>();
  const [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [formDirty, setFormDirty] = useState(false),
    [formKey, setFormKey] = useState(0);
  const epoch = useRef(0),
    controller = useRef<AbortController | null>(null);
  useEffect(() => {
    const reconcile = (event: Event) => {
      const value = (event as CustomEvent<RebaseReconciliation>).detail;
      if (
        value.attempt.owner.projectId !== projectId ||
        value.attempt.owner.proposalId !== basis.proposal.id
      )
        return;
      epoch.current++;
      controller.current?.abort();
      setBusy(false);
      setCandidate(undefined);
      setBasis(value.detail);
      setPreviousChoices(value.attempt.input);
      setResolutions([]);
      setCorrespondence([]);
      setRepairs([]);
      setConflicts([]);
      setFormDirty(false);
      setFormKey((key) => key + 1);
      onSaved(value.detail);
    };
    window.addEventListener("backend-rebase-reconciled", reconcile);
    return () => window.removeEventListener("backend-rebase-reconciled", reconcile);
  }, [projectId, basis.proposal.id, onSaved]);
  const revisions = useListBackendRevisions(projectId, { limit: 100 });
  const source = useQuery({
    queryKey: ["backend-rebase-source", projectId, newBase],
    enabled: /^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$/.test(newBase),
    retry: false,
    staleTime: Infinity,
    queryFn: async ({ signal }) => {
      const response = await getBackendRevision(projectId, newBase, { signal });
      if (
        response.status !== 200 ||
        response.data.projectId !== projectId ||
        response.data.id !== newBase
      )
        throw new Error("Новая база не подтверждена");
      const coverage = await readBackendCoverage(projectId, { revisionId: newBase }, signal);
      const sourceVector =
        "source" in coverage && coverage.source?.sourceVector
          ? coverage.source.sourceVector
          : undefined;
      if (
        response.data.schemaVersion === "6" &&
        !("source" in coverage && coverage.source?.sourceVector)
      )
        throw new Error("Не получен точный вектор новой базы");
      return { ...response.data, sourceVector };
    },
  });
  const dirty =
    !!newBase ||
    resolutions.length > 0 ||
    correspondence.length > 0 ||
    repairs.length > 0 ||
    formDirty;
  useEffect(() => {
    onDirty?.(dirty);
  }, [dirty, onDirty]);
  useEffect(
    () => () => {
      epoch.current++;
      controller.current?.abort();
    },
    [],
  );
  const input: PreviewBackendChangeProposalRebaseRequest = {
    expectedVersion: basis.proposal.version,
    proposalRevisionId: basis.revision.id,
    newBaseRevisionId: newBase,
    identityResolutions: correspondence,
    resolutions,
    repairCommands: repairs,
  };
  const body = JSON.stringify(input);
  const blocked = disabled || recovery.blocked || busy;
  function invalidate() {
    epoch.current++;
    controller.current?.abort();
    setBusy(false);
    setCandidate(undefined);
    setError("");
  }
  async function preview() {
    if (blocked || !source.data || formDirty) return;
    const issues = changeSchemaIssues(
      backendChangeSchemas.PreviewBackendChangeProposalRebaseRequest!,
      input as unknown as ChangeJSON,
    );
    if (issues.length) {
      setError(issues.join("; "));
      return;
    }
    const token = ++epoch.current;
    controller.current?.abort();
    controller.current = new AbortController();
    setBusy(true);
    setCandidate(undefined);
    setError("");
    try {
      const response = await previewBackendChangeProposalRebase(
        projectId,
        basis.proposal.id,
        input,
        { signal: controller.current.signal },
      );
      if (token !== epoch.current) return;
      if (response.status !== 200) throw new Error("Предпросмотр переноса не подтверждён");
      const c = response.data;
      if (
        c.proposalId !== basis.proposal.id ||
        c.proposalRevisionId !== basis.revision.id ||
        c.expectedVersion !== basis.proposal.version ||
        c.oldBaseRevisionId !== basis.revision.baseRevisionId ||
        c.oldBaseSemanticHash !== basis.revision.baseSemanticHash ||
        c.newBaseRevisionId !== newBase ||
        c.newBaseSemanticHash !== source.data.semanticHash ||
        c.sourcePins.revisionId !== newBase ||
        c.sourcePins.semanticHash !== source.data.semanticHash ||
        JSON.stringify(c.sourcePins.sourceSnapshotIds) !==
          JSON.stringify(source.data.sourceSnapshotIds)
      )
        throw new Error("Предпросмотр относится к другой базе");
      setCandidate({ value: c, body });
      setConflicts((previous) => [
        ...new Map([...previous, ...c.conflicts].map((x) => [x.id, x])).values(),
      ]);
    } catch (failure) {
      if (token === epoch.current) setError(changeMessage(failure));
    } finally {
      if (token === epoch.current) setBusy(false);
    }
  }
  async function apply() {
    if (
      blocked ||
      formDirty ||
      !candidate?.value.candidateHash ||
      !candidate.value.semanticHash ||
      candidate.body !== body ||
      !source.data
    )
      return;
    const expected = {
      newBaseRevisionId: newBase,
      newBaseSemanticHash: source.data.semanticHash,
      sourceVectorHash: candidate.value.sourcePins.sourceVectorHash,
      sourceSnapshotIds: source.data.sourceSnapshotIds,
      candidateHash: candidate.value.candidateHash,
      semanticHash: candidate.value.semanticHash,
    };
    setCandidate(undefined);
    try {
      const result = await recovery.execute(
        makeAnalysisAttempt(
          "rebase",
          { projectId, proposalId: basis.proposal.id },
          { ...input, candidateHash: expected.candidateHash, idempotencyKey: crypto.randomUUID() },
          {
            newBaseSemanticHash: expected.newBaseSemanticHash,
            sourceVectorHash: expected.sourceVectorHash,
            sourceSnapshotIds: expected.sourceSnapshotIds,
            candidateSemanticHash: expected.semanticHash,
          },
        ),
      );
      if (result) {
        onSaved(
          await verifyRebaseAcceptance(result as BackendChangeProposalApplyResult, basis, expected),
        );
        reset();
      }
    } catch (failure) {
      setError(changeMessage(failure));
    }
  }
  function reset() {
    invalidate();
    setNewBase("");
    setPreviousChoices(undefined);
    setConflicts([]);
    setResolutions([]);
    setCorrespondence([]);
    setRepairs([]);
    setFormDirty(false);
    setFormKey((v) => v + 1);
  }
  return (
    <Paper
      component="section"
      aria-label="Перенос на новую базу"
      withBorder
      p="md"
      style={{ minWidth: 0 }}
    >
      <Stack>
        <Title order={4}>Перенос на новую базу</Title>
        {previousChoices && (
          <>
            <Text>
              Предложение сверено: версия {basis.proposal.version}. Выберите решения заново по
              свежему предпросмотру.
            </Text>
            <AnalysisValue label="Решения до конфликта (справочно)" value={previousChoices} />
          </>
        )}
        <Text size="sm" style={{ overflowWrap: "anywhere" }}>
          B — исходная база {basis.revision.baseRevisionId}; O — предложение {basis.revision.id}; N
          — выбранный источник.
        </Text>
        <Text size="sm">
          Исторические основания остаются историческими. Аннотации сохраняют исходную привязку;
          перенос не меняет их адрес.
        </Text>
        {error && <Alert color="red">{error}</Alert>}
        <LoadState query={revisions} label="истории исходных ревизий" />
        <NativeSelect
          label="Новая база (N)"
          value={newBase}
          disabled={blocked}
          data={[
            { value: "", label: "Выберите точную исходную ревизию" },
            ...(revisions.data?.status === 200 ? revisions.data.data.items : [])
              .filter((r) => ["5", "6"].includes(r.schemaVersion))
              .map((r) => ({ value: r.id, label: `${r.summary} · ${r.id}` })),
          ]}
          onChange={(e) => {
            invalidate();
            setNewBase(e.currentTarget.value);
            setConflicts([]);
            setResolutions([]);
            setCorrespondence([]);
          }}
        />
        <details>
          <summary>Выбрать точную базу вне текущей страницы истории</summary>
          <TextInput
            label="Точная новая база (UUID)"
            value={newBase}
            disabled={blocked}
            onChange={(e) => {
              invalidate();
              setNewBase(e.currentTarget.value);
              setConflicts([]);
              setResolutions([]);
              setCorrespondence([]);
            }}
          />
        </details>
        {source.error && <Alert color="red">{changeMessage(source.error)}</Alert>}
        {conflicts.map((conflict) => (
          <ConflictChoice
            projectId={projectId}
            detail={basis}
            key={conflict.id}
            conflict={conflict}
            disabled={blocked}
            value={resolutions.find((r) => r.conflictId === conflict.id)}
            onChange={(value) => {
              invalidate();
              setResolutions((old) => [
                ...old.filter((r) => r.conflictId !== conflict.id),
                ...(value ? [value] : []),
              ]);
            }}
          />
        ))}
        <fieldset disabled={blocked} style={{ minWidth: 0, border: 0, padding: 0 }}>
          <legend>Явное соответствие идентичностей</legend>
          <BackendChangeSchemaField
            schema={{
              type: "array",
              items: { $ref: "#/components/schemas/BackendChangeRebaseIdentityResolution" },
            }}
            value={correspondence as unknown as ChangeJSON}
            onChange={(value) => {
              invalidate();
              setCorrespondence(value as unknown as BackendChangeRebaseIdentityResolution[]);
            }}
            label="Соответствия старых и новых объектов"
          />
        </fieldset>
        {newBase && (
          <details>
            <summary>Исправляющие команды ({repairs.length})</summary>
            <fieldset disabled={blocked} style={{ minWidth: 0, border: 0, padding: 0 }}>
              <BackendChangeCommandForms
                key={formKey}
                baseSchemaVersion={source.data?.schemaVersion === "5" ? "5" : "6"}
                identities={identities}
                criteria={basis.revision.criteria}
                onDirtyChange={(v) => {
                  setFormDirty(v);
                  if (v) invalidate();
                }}
                onAdd={(command) => {
                  invalidate();
                  setRepairs((old) => [...old, command]);
                  setFormDirty(false);
                  setFormKey((v) => v + 1);
                }}
              />
              {repairs.map((command, i) => (
                <Group key={command.commandId}>
                  <Text>
                    {changeCommandLabels[command.type]} · {command.reason}
                  </Text>
                  <Button
                    variant="subtle"
                    onClick={() => {
                      invalidate();
                      setRepairs((old) => old.filter((_, j) => j !== i));
                    }}
                  >
                    Убрать исправление {i + 1}
                  </Button>
                </Group>
              ))}
            </fieldset>
          </details>
        )}
        {formDirty && (
          <Button
            variant="subtle"
            onClick={() => {
              invalidate();
              setFormDirty(false);
              setFormKey((v) => v + 1);
            }}
          >
            Отменить ввод исправления
          </Button>
        )}
        {candidate?.value.diagnostics.map((d, i) => (
          <Alert key={i} color="yellow">
            {d.message}
          </Alert>
        ))}
        {candidate && (
          <Text>
            {candidate.value.candidateHash
              ? "Перенос проверен. Можно создать новый черновик."
              : "Разрешите конфликты и исправьте диагностику, затем повторите предпросмотр."}
          </Text>
        )}
        <Group>
          <Button disabled={blocked || !source.data || formDirty} onClick={() => void preview()}>
            Проверить перенос
          </Button>
          <Button
            disabled={
              blocked || formDirty || !candidate?.value.candidateHash || candidate.body !== body
            }
            onClick={() => void apply()}
          >
            Применить перенос
          </Button>
          <Button variant="subtle" disabled={recovery.blocked || busy} onClick={reset}>
            Сбросить перенос
          </Button>
        </Group>
      </Stack>
    </Paper>
  );
}
function ConflictChoice({
  projectId,
  detail,
  conflict,
  value,
  disabled,
  onChange,
}: {
  projectId: string;
  detail: BackendChangeProposalDetail;
  conflict: BackendChangeRebaseConflict;
  value?: BackendChangeRebaseResolution;
  disabled: boolean;
  onChange: (value?: BackendChangeRebaseResolution) => void;
}) {
  const record = useQuery({
    queryKey: ["rebase-conflict-kind", projectId, detail.revision.id, conflict.object],
    enabled: !rebaseReplacementSchema(conflict) && conflict.selector.kind !== "record",
    retry: false,
    staleTime: Infinity,
    queryFn: async ({ signal }) => {
      const target = {
        changeProposal: { proposalId: detail.proposal.id, proposalRevisionId: detail.revision.id },
      };
      if (conflict.object.recordType === "node") {
        const node = await readBackendNode(projectId, target, conflict.object.id, signal);
        return "node" in node
          ? node.node.kind
          : "proposalProjection" in node
            ? node.proposalProjection.kind
            : node.kind;
      }
      const page = await readBackendGraph(
        projectId,
        target,
        { recordType: "edges", id: conflict.object.id, limit: 1 },
        signal,
      );
      return page.edges[0]?.kind;
    },
  });
  const schema = rebaseReplacementSchema(conflict, record.data);
  return (
    <fieldset
      disabled={disabled}
      style={{ minWidth: 0, border: "1px solid var(--mantine-color-default-border)", padding: 12 }}
    >
      <legend>
        Конфликт {conflict.object.recordType} {conflict.object.id}
      </legend>
      <Stack gap="xs">
        <AnalysisValue label="Типизированное свойство" value={conflict.selector} />
        <AnalysisValue label="B — исходное значение и присутствие" value={conflict.base} />
        <AnalysisValue label="O — значение предложения и присутствие" value={conflict.ours} />
        <AnalysisValue
          label="N — новое исходное значение и присутствие"
          value={conflict.newSource}
        />
        <NativeSelect
          label="Решение конфликта"
          value={value?.choice ?? ""}
          data={[
            { value: "", label: "Выберите явно" },
            { value: "take_source", label: "Взять источник N" },
            { value: "keep_proposal", label: "Сохранить предложение O" },
            ...(schema ? [{ value: "replace", label: "Заменить типизированное значение" }] : []),
          ]}
          onChange={(e) => {
            const choice = e.currentTarget.value;
            if (!choice) onChange();
            else if (choice === "replace")
              onChange({
                conflictId: conflict.id,
                choice,
                reason: value?.reason ?? "",
                value: changeSchemaDefault(schema!) as Extract<
                  BackendChangeRebaseResolution,
                  { choice: "replace" }
                >["value"],
              });
            else
              onChange({
                conflictId: conflict.id,
                choice: choice as "take_source" | "keep_proposal",
                reason: value?.reason ?? "",
              });
          }}
        />
        {value && (
          <Textarea
            label="Причина решения"
            required
            maxLength={4096}
            value={value.reason}
            onChange={(e) => onChange({ ...value, reason: e.currentTarget.value })}
          />
        )}
        {value?.choice === "replace" && schema && (
          <BackendChangeSchemaField
            schema={schema}
            value={value.value as ChangeJSON}
            onChange={(next) => onChange({ ...value, value: next as typeof value.value })}
            label="Новое значение группы"
          />
        )}
        {conflict.selector.kind === "record" && (
          <Text size="sm">
            Для изменения целой записи используйте типизированные исправляющие команды.
          </Text>
        )}
      </Stack>
    </fieldset>
  );
}
