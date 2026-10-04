import {
  projectionTarget,
  projectionKey,
  projectionReturnTarget,
  type ProjectionReadProps,
} from "./backendEffectiveProjectionReads";
import { completeArtifactSet, readArtifactPage } from "./backendArtifactReads";
import { ArtifactDeltaSide } from "./BackendArtifactContent";
import type { ArtifactPinsPreview, ApplyBackendArtifactPinsRequest } from "@/api/generated/schemas";
import { BackendArtifactProjections } from "./BackendArtifactProjections";
import { createContext, useContext, useEffect, useId, useRef, useState } from "react";
import {
  Alert,
  Badge,
  Button,
  Code,
  Group,
  NativeSelect,
  Paper,
  Stack,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { defaultStringifySearch } from "@tanstack/react-router";
import {
  applyBackendArtifactPins,
  previewBackendArtifactPins,
  applyBackendAPIPins,
  getBackendRevision,
  previewBackendAPIPins,
} from "@/api/generated/backend-projects/backend-projects";
import { getApiDesign, listApiDesigns } from "@/api/generated/api-designs/api-designs";
import type {
  ApplyBackendAPIPinsRequest,
  BackendAPIArtifactItem,
  BackendAPIArtifactSelector,
  BackendAPIPinCommand,
  BackendAPIPinsPreview,
  BackendAPIPinsResultResponse,
  BackendArtifactRef,
  BackendReadTarget,
  BackendRevisionResponse,
  PreviewBackendAPIPinsRequest,
  PreviewBackendArtifactPinsRequest,
} from "@/api/generated/schemas";
import { ApiFailure } from "@/api/client";
import { LoadState } from "./BackendGraphInventory";
import {
  databaseButtonStyles,
  databaseWrap,
  useDatabaseCancellation,
} from "./backendDatabaseReads";
import {
  exactArtifactId,
  readAPIArtifacts,
  replaceArtifactBindings,
  safeLegacyId,
  strictAuthoredPointer,
  type APIArtifactScope,
} from "./backendAPIArtifactReads";

const pinButtonProps = { h: "auto", py: "xs", maw: "100%", styles: databaseButtonStyles };

type Host = {
  revision: BackendRevisionResponse;
  projectVersion: number;
  canEdit: boolean;
  onDirty: (dirty: boolean, identity?: string) => void;
  onApplied: (result: BackendAPIPinsResultResponse) => void;
  reread?: () => Promise<{ revision: BackendRevisionResponse; projectVersion: number }>;
  onCurrentHead?: (
    context: { revision: BackendRevisionResponse; projectVersion: number },
    identity: string,
  ) => boolean;
  guard?: () => boolean;
  claim?: (identity: string) => boolean;
};
export const BackendAPIArtifactsContext = createContext<Host | null>(null);
export function artifactNavigationHref(
  ref: Pick<BackendArtifactRef, "artifactId" | "revisionId" | "contentHash" | "selector"> &
    Partial<Pick<BackendArtifactRef, "resolvedPointer">>,
  projectId: string,
  backendRevisionId: string,
  sourceNodeId?: string,
  target?: BackendReadTarget,
): string | undefined {
  if (safeLegacyId(ref.artifactId) === undefined || safeLegacyId(ref.revisionId) === undefined)
    return undefined;
  const search: Record<string, string> = {
    pinnedRevisionId: ref.revisionId,
    pinnedHash: ref.contentHash,
    returnProjectId: projectId,
    ...projectionReturnTarget(target, backendRevisionId),
  };
  if (ref.resolvedPointer) search.pinnedPointer = ref.resolvedPointer;
  if ("objectKey" in ref.selector) search.pinnedObjectKey = ref.selector.objectKey;
  else search.pinnedSelectorPointer = ref.selector.jsonPointer;
  if (sourceNodeId) search.returnSourceNodeId = sourceNodeId;
  return `/designs/${ref.artifactId}${defaultStringifySearch(search)}`;
}
export function BackendArtifactReference({
  reference,
  projectId,
  revisionId,
  target,
  sourceNodeId,
  separate = false,
}: ProjectionReadProps & {
  reference: BackendArtifactRef;
  projectId: string;
  sourceNodeId?: string;
  separate?: boolean;
}) {
  const href = artifactNavigationHref(reference, projectId, revisionId ?? "", sourceNodeId, target);
  return (
    <Stack gap="xs" style={{ minWidth: 0 }}>
      <Text fw={600} style={databaseWrap}>
        {reference.lastKnownLabel || "Закреплённый API"}
      </Text>
      <Text size="sm" style={databaseWrap}>
        Дизайн API {reference.artifactId} · ревизия {reference.revisionId}
      </Text>
      <Text size="xs" style={databaseWrap}>
        Хеш артефакта: {reference.contentHash}
      </Text>
      <Text size="xs" style={databaseWrap}>
        Хеш выбранного объекта: {reference.objectHash}
      </Text>
      <Text size="sm" style={databaseWrap}>
        Селектор: {JSON.stringify(reference.selector)}
      </Text>
      <Text size="sm" style={databaseWrap}>
        Авторский путь: {reference.resolvedPointer}
      </Text>
      {href ? (
        <Button
          {...pinButtonProps}
          component="a"
          href={href}
          target={separate ? "_blank" : undefined}
          rel={separate ? "noopener noreferrer" : undefined}
          variant="light"
          w="fit-content"
          maw="100%"
        >
          Открыть закреплённый API{separate ? " в новой вкладке" : ""}
        </Button>
      ) : (
        <Text c="orange" size="sm">
          Точный ID доступен для чтения. Числовой редактор API не поддерживает безопасную навигацию
          к этому ID.
        </Text>
      )}
    </Stack>
  );
}
type Props = ProjectionReadProps & {
  projectId: string;
  sourceNodeId?: string;
  sourceKind?: "http_operation" | "api_field";
  readOnly?: boolean;
};
export function BackendAPIArtifacts(props: Props) {
  const host = useContext(BackendAPIArtifactsContext);
  return (
    <>
      <ArtifactPanel
        key={`${props.projectId}:${projectionKey(projectionTarget(props), props.pins)}:${props.sourceNodeId ?? "all"}`}
        {...props}
      />
      {props.sourceNodeId &&
        (props.pins || (host && ["4", "5"].includes(host.revision.schemaVersion))) && (
          <BackendArtifactProjections {...props} />
        )}
    </>
  );
}
function ArtifactPanel({
  projectId,
  revisionId: selectedRevision,
  target: explicitTarget,
  pins: effectivePins,
  sourceNodeId,
  sourceKind,
  readOnly = false,
}: Props) {
  const target = projectionTarget({
    revisionId: selectedRevision,
    target: explicitTarget,
    pins: effectivePins,
  });
  const revisionId = effectivePins?.baseRevisionId ?? selectedRevision ?? "";
  readOnly = readOnly || !!target.changeProposal || !!target.proposal;

  const instanceId = useId();
  const host = useContext(BackendAPIArtifactsContext);
  const latestHost = useRef(host);
  useEffect(() => {
    latestHost.current = host;
  }, [host]);
  const key = [
    "backend-api-artifacts",
    projectId,
    projectionKey(target, effectivePins),
    sourceNodeId ?? "all",
  ];
  useDatabaseCancellation(key);
  const revisionQuery = useQuery({
    queryKey: [...key, "revision"],
    enabled: !effectivePins && !target.changeProposal && host?.revision.id !== revisionId,
    retry: false,
    queryFn: async ({ signal }) => {
      const result = await getBackendRevision(projectId, revisionId, { signal });
      signal.throwIfAborted();
      if (
        result.status !== 200 ||
        result.data.id !== revisionId ||
        result.data.projectId !== projectId
      )
        throw new Error("Неверная ревизия связей API");
      return result.data;
    },
  });
  const revision = host?.revision.id === revisionId ? host.revision : revisionQuery.data;
  const scope: APIArtifactScope | undefined = effectivePins
    ? {
        projectId,
        revisionId,
        target,
        pins: effectivePins,
        semanticHash: effectivePins.effectiveSemanticHash,
        sourceSnapshotIds: effectivePins.sourceSnapshotIds,
        artifactPins: effectivePins.artifactPins,
      }
    : revision
      ? {
          projectId,
          revisionId,
          semanticHash: revision.semanticHash,
          sourceSnapshotIds: revision.sourceSnapshotIds,
          artifactPins: revision.artifactPins,
        }
      : undefined;
  const scopeIdentity = JSON.stringify(scope);
  const query = useQuery({
    queryKey: [...key, scopeIdentity],
    enabled: !!scope,
    retry: false,
    staleTime: 0,
    refetchOnWindowFocus: true,
    queryFn: ({ signal }) => readAPIArtifacts(scope!, undefined, signal),
  });
  const all = query.data?.items ?? [];
  const visible = sourceNodeId
    ? all.filter((item) => item.binding.sourceNodeId === sourceNodeId)
    : all;
  const [editing, setEditing] = useState(false);
  const [artifactId, setArtifactId] = useState("");
  const [apiRevisionId, setAPIRevisionId] = useState("");
  const [selectorText, setSelectorText] = useState("");
  const [reason, setReason] = useState("");
  const [targetSource, setTargetSource] = useState(sourceNodeId ?? "");
  const [originalSource, setOriginalSource] = useState(sourceNodeId ?? "");
  const [selectedKind, setSelectedKind] = useState(sourceKind ?? "http_operation");
  const [preview, setPreview] = useState<BackendAPIPinsPreview | null>(null);
  const [candidate, setCandidate] = useState<
    PreviewBackendAPIPinsRequest | PreviewBackendArtifactPinsRequest | null
  >(null);
  const [attempt, setAttempt] = useState<
    ApplyBackendAPIPinsRequest | ApplyBackendArtifactPinsRequest | null
  >(null);
  const [genericPreview, setGenericPreview] = useState<ArtifactPinsPreview | null>(null);
  const generic = useRef(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [conflict, setConflict] = useState(false);
  const [unknown, setUnknown] = useState(false);
  const request = useRef({
    generation: 0,
    controller: null as AbortController | null,
    alive: true,
  });
  const [frozenItems, setFrozenItems] = useState<BackendAPIArtifactItem[]>([]);
  const [editContext, setEditContext] = useState<{
    scope: APIArtifactScope;
    expectedVersion: number;
  } | null>(null);
  const dirty = editing || !!attempt;
  const dirtyIdentity = `${projectId}:${revisionId}:${sourceNodeId ?? "all"}:${instanceId}`;
  const onDirty = host?.onDirty;
  useEffect(() => {
    onDirty?.(dirty, dirtyIdentity);
    return () => onDirty?.(false, dirtyIdentity);
  }, [dirty, dirtyIdentity, onDirty]);
  useEffect(() => {
    const current = request.current;
    current.alive = true;
    return () => {
      current.alive = false;
      current.generation++;
      current.controller?.abort();
    };
  }, []);
  useEffect(() => {
    const handler = (event: BeforeUnloadEvent) => {
      if (dirty) {
        event.preventDefault();
        event.returnValue = "";
      }
    };
    window.addEventListener("beforeunload", handler);
    return () => window.removeEventListener("beforeunload", handler);
  }, [dirty]);
  const designs = useQuery({
    queryKey: [...key, "designs"],
    enabled: editing,
    retry: false,
    queryFn: async ({ signal }) => {
      const result = await listApiDesigns({ signal });
      signal.throwIfAborted();
      if (result.status !== 200) throw new Error("Не удалось загрузить дизайны API");
      return result.data.designs;
    },
  });
  const safeDesignId = safeLegacyId(artifactId);
  const design = useQuery({
    queryKey: [...key, "design", artifactId],
    enabled: editing && safeDesignId !== undefined,
    retry: false,
    staleTime: 0,
    queryFn: async ({ signal }) => {
      const result = await getApiDesign(safeDesignId!, { signal });
      signal.throwIfAborted();
      if (result.status !== 200 || safeLegacyId(result.data.design.id) !== safeDesignId)
        throw new Error("Не удалось загрузить точный дизайн API");
      return result.data;
    },
  });
  const editable = !!host?.canEdit && host.revision.id === revisionId && !readOnly;
  function invalidatePreview() {
    request.current.generation++;
    request.current.controller?.abort();
    setPreview(null);
    setGenericPreview(null);
    setCandidate(null);
    setError(null);
    setConflict(false);
  }
  function begin(item?: BackendAPIArtifactItem) {
    if (!query.data || !scope || !host || busy || attempt) return;
    if (host.claim && !host.claim(dirtyIdentity)) {
      setError(
        "Завершите изменение связи API в другом инспекторе, включая попытку с неизвестным результатом.",
      );
      return;
    }
    setFrozenItems(structuredClone(query.data.items));
    setEditContext({ scope: structuredClone(scope), expectedVersion: host.projectVersion });
    invalidatePreview();
    setEditing(true);
    setArtifactId(item?.binding.ref.artifactId ?? "");
    setAPIRevisionId("");
    setSelectorText("");
    setReason("");
    setTargetSource(item?.binding.sourceNodeId ?? sourceNodeId ?? "");
    setOriginalSource(item?.binding.sourceNodeId ?? sourceNodeId ?? "");
    setSelectedKind(item?.binding.sourceKind ?? sourceKind ?? "http_operation");
  }
  async function makePreview(mode: "set" | "remove_node" | "remove_group") {
    if (
      !host ||
      !scope ||
      busy ||
      attempt ||
      !exactArtifactId(artifactId) ||
      !reason.trim() ||
      new TextEncoder().encode(reason).length > 4096
    )
      return;
    if (mode !== "remove_group" && !targetSource.trim()) return;
    if (
      mode === "set" &&
      (!exactArtifactId(apiRevisionId) ||
        (selectedKind === "api_field"
          ? !strictAuthoredPointer(selectorText)
          : !selectorText || new TextEncoder().encode(selectorText).length > 200))
    ) {
      setError("Укажите точную ревизию и авторский селектор API");
      return;
    }
    const selector: BackendAPIArtifactSelector =
      selectedKind === "api_field" ? { jsonPointer: selectorText } : { objectKey: selectorText };
    const retained =
      mode === "set" && originalSource !== targetSource.trim()
        ? frozenItems.filter(
            (item) =>
              item.binding.ref.artifactId !== artifactId ||
              item.binding.sourceNodeId !== originalSource,
          )
        : frozenItems;
    const command: BackendAPIPinCommand =
      mode === "remove_group"
        ? { type: "remove_api_pin", artifactId, reason: reason.trim() }
        : replaceArtifactBindings(
            retained,
            artifactId,
            mode === "set"
              ? apiRevisionId
              : (frozenItems.find((item) => item.binding.ref.artifactId === artifactId)?.binding.ref
                  .revisionId ?? apiRevisionId),
            mode === "set" ? targetSource.trim() : originalSource,
            mode === "set" ? selector : null,
            reason.trim(),
          );
    if (!editContext) return;
    const captured = editContext;
    const input = {
      baseRevisionId: captured.scope.revisionId,
      expectedVersion: captured.expectedVersion,
      commands: [command],
    };
    if (new TextEncoder().encode(JSON.stringify(input)).length > 128 * 1024) {
      setError("Команда превышает 128 KiB; требуется более узкий запрос");
      return;
    }
    invalidatePreview();
    const current = ++request.current.generation;
    request.current.controller = new AbortController();
    const signal = request.current.controller.signal;
    setBusy(true);
    try {
      // Legacy removal cannot delete a group that retains invisible editor links.
      generic.current = false;
      if (mode === "remove_node" || mode === "remove_group") {
        const group = await readArtifactPage(
          captured.scope,
          {
            revisionId: captured.scope.revisionId,
            artifact: { kind: "api_design", id: artifactId },
            view: "states",
            limit: 1,
          },
          signal,
        );
        if (group.editorBindings.length) {
          const command =
            mode === "remove_group"
              ? {
                  type: "remove_artifact_pin" as const,
                  artifact: { kind: "api_design" as const, id: artifactId },
                  reason: reason.trim(),
                }
              : completeArtifactSet(
                  group,
                  group.selectedPin.revisionId,
                  reason.trim(),
                  undefined,
                  group.apiBindings
                    .filter((b) => b.sourceNodeId !== originalSource)
                    .map((b) => ({ sourceNodeId: b.sourceNodeId, selector: b.ref.selector })),
                );
          const body = { ...input, commands: [command] };
          const response = await previewBackendArtifactPins(projectId, body, { signal });
          signal.throwIfAborted();
          if (
            response.status !== 200 ||
            response.data.baseRevisionId !== input.baseRevisionId ||
            response.data.expectedVersion !== input.expectedVersion ||
            JSON.stringify([...response.data.sourceSnapshotIds].sort()) !==
              JSON.stringify([...captured.scope.sourceSnapshotIds].sort())
          )
            throw new Error("Другой контекст generic preview");
          if (request.current.alive && current === request.current.generation) {
            generic.current = true;
            setGenericPreview(response.data);
            setCandidate(structuredClone(body));
          }
          return;
        }
      }
      const response = await previewBackendAPIPins(projectId, input, { signal });
      signal.throwIfAborted();
      if (response.status !== 200) throw new Error("Не удалось получить предпросмотр");
      const value = response.data;
      if (
        value.baseRevisionId !== input.baseRevisionId ||
        value.expectedVersion !== input.expectedVersion ||
        JSON.stringify([...value.sourceSnapshotIds].sort()) !==
          JSON.stringify([...captured.scope.sourceSnapshotIds].sort())
      )
        throw new Error("Получен другой контекст предпросмотра API");
      if (request.current.alive && current === request.current.generation) {
        setPreview(value);
        setCandidate(structuredClone(input));
      }
    } catch (failure) {
      if (request.current.alive && current === request.current.generation && !signal.aborted) {
        setError(
          failure instanceof ApiFailure && failure.status === 413
            ? `${failure.message}. Требуется более узкий запрос: превышен предел работы со снимком.`
            : failure instanceof Error
              ? failure.message
              : "Ошибка предпросмотра",
        );
        setConflict(failure instanceof ApiFailure && failure.status === 409);
      }
    } finally {
      if (request.current.alive && current === request.current.generation) setBusy(false);
    }
  }
  async function apply() {
    if (
      busy ||
      conflict ||
      (!attempt &&
        (!(genericPreview ?? preview)?.canApply ||
          (genericPreview ?? preview)?.diffTruncated ||
          !candidate))
    )
      return;
    const input = attempt ?? {
      ...candidate!,
      candidateHash: (genericPreview ?? preview)!.candidateHash,
      idempotencyKey: crypto.randomUUID(),
    };
    if (new TextEncoder().encode(JSON.stringify(input)).length > 128 * 1024) {
      setError("Команда применения превышает 128 KiB; требуется более узкий запрос");
      return;
    }
    const generation = request.current.generation;
    const usesGeneric = generic.current;
    setAttempt(input);
    setBusy(true);
    setError(null);
    try {
      const response = usesGeneric
        ? await applyBackendArtifactPins(projectId, input as ApplyBackendArtifactPinsRequest)
        : await applyBackendAPIPins(projectId, input as ApplyBackendAPIPinsRequest);
      if (response.status !== 200) throw new Error("Неизвестный ответ применения API");
      if (
        response.data.project.id !== projectId ||
        response.data.revision.projectId !== projectId ||
        response.data.revision.parentRevisionId !== input.baseRevisionId
      )
        throw new Error("Ответ применения содержит другой контекст API");
      if (request.current.alive && request.current.generation === generation) {
        setAttempt(null);
        setUnknown(false);
        setEditing(false);
        setPreview(null);
        setCandidate(null);
        setGenericPreview(null);
        const currentHost = latestHost.current;
        if (
          !usesGeneric ||
          (currentHost?.revision.id === input.baseRevisionId &&
            currentHost.projectVersion === input.expectedVersion)
        )
          currentHost?.onApplied(response.data);
        else
          setError(
            `Применение API подтверждено: ревизия ${response.data.revision.id}. Результат доступен в истории; текущий выбор сохранён.`,
          );
      }
    } catch (failure) {
      if (!request.current.alive || request.current.generation !== generation) return;
      const known =
        failure instanceof ApiFailure &&
        failure.status >= 400 &&
        failure.status < 500 &&
        ![408, 429].includes(failure.status);
      setUnknown(!known);
      if (known) setAttempt(null);
      if (known && failure instanceof ApiFailure && [413, 422].includes(failure.status)) {
        setPreview((value) => (value ? { ...value, canApply: false } : value));
        setGenericPreview((value) => (value ? { ...value, canApply: false } : value));
      }
      setConflict(failure instanceof ApiFailure && failure.status === 409);
      setError(failure instanceof Error ? failure.message : "Ошибка применения API");
    } finally {
      if (request.current.alive && request.current.generation === generation) setBusy(false);
    }
  }
  return (
    <Paper
      component="section"
      aria-label={`Связи API${sourceNodeId ? ` ${sourceNodeId}` : ""}`}
      data-testid="backend-api-artifacts"
      withBorder
      p="sm"
      style={{ minWidth: 0 }}
    >
      <Stack>
        <Title order={4}>Ручные связи API</Title>
        <Text size="sm">
          Основания описывают исходный код. Ручная связь с API задаёт выбранный объект и не
          доказывает соответствие схеме или полноту lineage.
        </Text>
        <Text size="xs" style={databaseWrap}>
          Снимки исходников: {scope?.sourceSnapshotIds.join(", ") || "не загружены"} · ревизия{" "}
          {revisionId}
        </Text>
        {!revision && !effectivePins && (
          <LoadState query={revisionQuery} label="ревизии связей API" />
        )}
        <LoadState query={query} label="связей API" />
        <Button
          {...pinButtonProps}
          variant="default"
          disabled={busy}
          onClick={() => void query.refetch()}
        >
          Обновить состояния API
        </Button>
        {query.data && visible.length === 0 && <Text>Ручных связей API нет.</Text>}
        {visible.map((item, index) => (
          <Stack
            key={`${item.binding.ref.artifactId}:${item.binding.sourceNodeId}:${index}`}
            gap="xs"
          >
            <Badge>{item.resolution.status}</Badge>
            {item.resolution.status === "orphaned" && (
              <Text>Узел исходника удалён; доступны явное удаление и переназначение связи.</Text>
            )}
            {item.resolution.status === "broken" && (
              <Text>Закреплённый API недоступен; удалите или исправьте связь явно.</Text>
            )}
            <Text style={databaseWrap}>
              Исходный узел: {item.binding.sourceNodeId} · {item.binding.sourceLastKnownLabel}
            </Text>
            <BackendArtifactReference
              reference={item.binding.ref}
              projectId={projectId}
              revisionId={revisionId}
              target={target}
              pins={effectivePins}
              sourceNodeId={item.binding.sourceNodeId}
              separate={dirty}
            />
            <Text size="sm" style={databaseWrap}>
              Причина: {item.binding.reason}
            </Text>
            {item.resolution.currentDraftRevisionId ? (
              <Text size="sm">
                Текущий черновик API: {item.resolution.currentDraftRevisionId}
                {item.resolution.updateAvailable ? " · доступно обновление" : ""}
              </Text>
            ) : (
              <Text size="sm">
                Состояние текущего черновика API неизвестно. Неизменяемая связь сохраняется.
              </Text>
            )}
            {item.resolution.diagnostics.map((diagnostic, i) => (
              <Text key={i} size="sm" style={databaseWrap}>
                {diagnostic.code}: {diagnostic.message}
              </Text>
            ))}
            {editable && !editing && (
              <Button {...pinButtonProps} variant="light" onClick={() => begin(item)}>
                Изменить связь API
              </Button>
            )}
          </Stack>
        ))}
        {editable && !!sourceNodeId && !editing && visible.length === 0 && (
          <Button {...pinButtonProps} disabled={!query.data} onClick={() => begin()}>
            Добавить связь API
          </Button>
        )}
        {!editable && (
          <Text size="sm" c="dimmed">
            Связи этой ревизии доступны для чтения. Для изменения откройте текущий источник проекта.
          </Text>
        )}
        {editing && (
          <fieldset
            disabled={busy || !!attempt}
            style={{ minWidth: 0, border: 0, margin: 0, padding: 0 }}
          >
            <Stack>
              <Text fw={600}>Заменяется весь набор связей выбранного дизайна API.</Text>
              {editContext && (
                <Text size="sm" style={databaseWrap}>
                  База редактирования: {editContext.scope.revisionId} · хеш{" "}
                  {editContext.scope.semanticHash} · снимки{" "}
                  {editContext.scope.sourceSnapshotIds.join(", ")} · версия проекта{" "}
                  {editContext.expectedVersion}
                </Text>
              )}
              <Text size="sm">
                Все остальные исходные узлы выбранной группы сохраняются; прочие группы артефактов
                сохраняются сервером. Введите селектор явно.
              </Text>
              <LoadState query={designs} label="дизайнов API" />
              {designs.data?.some((item) => safeLegacyId(item.id) === undefined) && (
                <Text c="orange">
                  Некоторые дизайны имеют небезопасный числовой ID и недоступны для выбора.
                </Text>
              )}
              <NativeSelect
                label="Дизайн API"
                value={artifactId}
                data={[
                  { value: "", label: "Выберите дизайн" },
                  ...(designs.data ?? [])
                    .filter((item) => safeLegacyId(item.id) !== undefined)
                    .map((item) => ({
                      value: String(item.id),
                      label: `${item.name} · ${item.id}`,
                    })),
                  ...(artifactId && !designs.data?.some((item) => String(item.id) === artifactId)
                    ? [{ value: artifactId, label: `Закреплённый дизайн ${artifactId}` }]
                    : []),
                ]}
                onChange={(event) => {
                  invalidatePreview();
                  setArtifactId(event.currentTarget.value);
                  setAPIRevisionId("");
                }}
              />
              <LoadState query={design} label="ревизий API" />
              <NativeSelect
                label="Ревизия API"
                value={apiRevisionId}
                data={[
                  { value: "", label: "Выберите точную ревизию" },
                  ...(design.data?.revisions ?? [])
                    .filter((item) => safeLegacyId(item.id) !== undefined)
                    .map((item) => ({
                      value: String(item.id),
                      label: `${item.id} · ${item.summary}`,
                    })),
                ]}
                onChange={(event) => {
                  invalidatePreview();
                  setAPIRevisionId(event.currentTarget.value);
                }}
              />
              <TextInput
                label={
                  selectedKind === "api_field"
                    ? "Авторский JSON Pointer поля"
                    : "Object key операции"
                }
                value={selectorText}
                onChange={(event) => {
                  invalidatePreview();
                  setSelectorText(event.currentTarget.value);
                }}
              />
              <TextInput
                label="Исходный узел для явного переназначения"
                value={targetSource}
                onChange={(event) => {
                  invalidatePreview();
                  setTargetSource(event.currentTarget.value);
                }}
              />
              <TextInput
                required
                label="Причина связи API"
                value={reason}
                onChange={(event) => {
                  invalidatePreview();
                  setReason(event.currentTarget.value);
                }}
              />
              <details>
                <summary>Текущий полный набор связей ({frozenItems.length})</summary>
                {frozenItems.map((item, i) => (
                  <Text key={i} size="sm" style={databaseWrap}>
                    {item.binding.ref.artifactId} · {item.binding.sourceNodeId} ·{" "}
                    {JSON.stringify(item.binding.ref.selector)}
                  </Text>
                ))}
              </details>
              <Group>
                <Button
                  {...pinButtonProps}
                  disabled={!reason.trim() || !artifactId || !apiRevisionId || !selectorText}
                  onClick={() => void makePreview("set")}
                >
                  Предпросмотр связей API
                </Button>
                <Button
                  {...pinButtonProps}
                  variant="default"
                  disabled={!reason.trim() || !artifactId}
                  onClick={() => void makePreview("remove_node")}
                >
                  Удалить связь этого узла
                </Button>
                <Button
                  {...pinButtonProps}
                  variant="default"
                  disabled={!reason.trim() || !artifactId}
                  onClick={() => void makePreview("remove_group")}
                >
                  Удалить группу API
                </Button>
              </Group>
            </Stack>
          </fieldset>
        )}
        {error && (
          <Alert role="alert" color="red">
            {error}
            {error.includes("limit") || error.includes("413")
              ? " Требуется более узкий запрос: превышен предел работы со снимком."
              : ""}
          </Alert>
        )}
        {unknown && (
          <Alert data-testid="api-pin-unknown-outcome" color="yellow">
            Результат применения неизвестен. Сохранена точная команда, кандидат и ключ; повторите
            эту попытку.
          </Alert>
        )}
        {conflict && (
          <Button
            {...pinButtonProps}
            disabled={busy || !!attempt}
            onClick={async () => {
              setBusy(true);
              try {
                const fresh = await host?.reread?.();
                if (!fresh) throw new Error("Невозможно перечитать текущий источник");
                const nextScope = {
                  projectId,
                  revisionId: fresh.revision.id,
                  semanticHash: fresh.revision.semanticHash,
                  sourceSnapshotIds: fresh.revision.sourceSnapshotIds,
                  artifactPins: fresh.revision.artifactPins,
                };
                const signal = new AbortController().signal;
                const vector = await readAPIArtifacts(nextScope, undefined, signal);
                if (!request.current.alive) return;
                setFrozenItems(structuredClone(vector.items));
                setEditContext({ scope: nextScope, expectedVersion: fresh.projectVersion });
                invalidatePreview();
              } catch (failure) {
                if (request.current.alive)
                  setError(
                    failure instanceof Error ? failure.message : "Не удалось перечитать источник",
                  );
              } finally {
                if (request.current.alive) setBusy(false);
              }
            }}
          >
            Перечитать источник и повторить предпросмотр
          </Button>
        )}
        {genericPreview && (
          <>
            <Button
              {...pinButtonProps}
              disabled={
                busy ||
                conflict ||
                (!attempt && (!genericPreview.canApply || genericPreview.diffTruncated))
              }
              onClick={() => void apply()}
            >
              {attempt
                ? "Повторить точное применение API"
                : "Применить полную группу API и моделей"}
            </Button>
            <Stack aria-label="Полный предпросмотр группы API и моделей">
              <Text>
                Полный перечень после изменения: {genericPreview.apiBindings.length} API +{" "}
                {genericPreview.editorBindings.length} моделей
              </Text>
              {genericPreview.diff.map((d) => (
                <Stack key={d.identity}>
                  <Text style={databaseWrap}>
                    {d.kind} · {d.status} · {d.identity}
                    {d.contextChanged ? " · контекст изменён" : ""}
                  </Text>
                  <ArtifactDeltaSide
                    side={d.before}
                    projectId={projectId}
                    revisionId={revisionId}
                    target={target}
                    pins={effectivePins}
                  />
                  <ArtifactDeltaSide
                    side={d.after}
                    projectId={projectId}
                    revisionId={revisionId}
                    target={target}
                    pins={effectivePins}
                  />
                </Stack>
              ))}
              {genericPreview.diagnostics.map((d, i) => (
                <Text key={i} style={databaseWrap}>
                  {d.code}: {d.message}
                </Text>
              ))}
              {genericPreview.diffTruncated && (
                <Text c="orange">Предпросмотр усечён; применение запрещено.</Text>
              )}
            </Stack>
          </>
        )}
        {preview && (
          <Stack data-testid="api-pin-preview" aria-label="Предпросмотр связей API">
            <Text size="sm" style={databaseWrap}>
              База предпросмотра: {preview.baseRevisionId} · версия проекта{" "}
              {preview.expectedVersion}
            </Text>
            <Text fw={600}>Предлагаемый полный набор ({preview.bindings.length})</Text>
            {preview.bindings.map((binding, i) => (
              <Text key={i} size="sm" style={databaseWrap}>
                {binding.ref.artifactId} · {binding.sourceNodeId} ·{" "}
                {JSON.stringify(binding.ref.selector)}
              </Text>
            ))}
            <Text fw={600}>Удаляемые связи</Text>
            {frozenItems
              .filter(
                (item) =>
                  !preview.bindings.some(
                    (binding) =>
                      binding.sourceNodeId === item.binding.sourceNodeId &&
                      binding.ref.artifactId === item.binding.ref.artifactId,
                  ),
              )
              .map((item, i) => (
                <Text key={i} style={databaseWrap}>
                  {item.binding.sourceNodeId} · API {item.binding.ref.artifactId}
                </Text>
              ))}
            <Text size="sm">
              Хеш метода описывает авторский объект операции. Параметры родителя, глобальные servers
              и security могут измениться отдельно.
            </Text>
            {preview.diff.map((diff, index) => (
              <Paper
                key={`${diff.sourceNodeId}:${diff.status}:${index}`}
                data-testid="api-pin-diff-row"
                withBorder
                p="xs"
              >
                <Stack gap="xs">
                  <Text style={databaseWrap}>
                    {diff.sourceNodeId} · {diff.status}
                  </Text>
                  {diff.contextChanged && (
                    <Text>
                      Контекст всего артефакта изменился, даже если хеш объекта совпадает.
                    </Text>
                  )}
                  {diff.before && (
                    <Stack>
                      <Text size="xs">До</Text>
                      <BackendArtifactReference
                        reference={diff.before}
                        projectId={projectId}
                        revisionId={preview.baseRevisionId}
                        sourceNodeId={diff.sourceNodeId}
                        separate
                      />
                    </Stack>
                  )}
                  {diff.after && (
                    <Stack>
                      <Text size="xs">После</Text>
                      <BackendArtifactReference
                        reference={diff.after}
                        projectId={projectId}
                        revisionId={preview.baseRevisionId}
                        sourceNodeId={diff.sourceNodeId}
                        separate
                      />
                    </Stack>
                  )}
                  {diff.changes.map((change, i) => (
                    <Code key={i} block style={databaseWrap}>
                      {change.pointer === "" ? "(корень выбранного объекта)" : change.pointer} ·{" "}
                      {change.kind} · {change.beforeHash ?? "отсутствует"} →{" "}
                      {change.afterHash ?? "отсутствует"}
                    </Code>
                  ))}
                </Stack>
              </Paper>
            ))}
            {candidate?.commands.map((command, index) => {
              if (command.type !== "set_api_pin") return null;
              const pin = preview.pins.find(
                (pin) =>
                  pin.kind === "api_design" &&
                  pin.id === command.artifactId &&
                  pin.revisionId === command.revisionId &&
                  "contentHash" in pin,
              );
              const binding = command.bindings.find(
                (binding) => binding.sourceNodeId === targetSource,
              );
              if (!pin || !("contentHash" in pin) || !binding) return null;
              const href = artifactNavigationHref(
                {
                  artifactId: pin.id,
                  revisionId: pin.revisionId,
                  contentHash: pin.contentHash,
                  selector: binding.selector,
                },
                projectId,
                preview.baseRevisionId,
                binding.sourceNodeId,
              );
              return href ? (
                <Button
                  {...pinButtonProps}
                  key={index}
                  component="a"
                  href={href}
                  target="_blank"
                  rel="noopener noreferrer"
                  variant="default"
                >
                  Открыть целевой снимок API в новой вкладке
                </Button>
              ) : (
                <Text key={index}>
                  Точный целевой ID {pin.id} / {pin.revisionId} не поддерживается числовым
                  редактором API.
                </Text>
              );
            })}
            {preview.diagnostics.map((diagnostic, i) => (
              <Text key={i} style={databaseWrap}>
                {diagnostic.code}: {diagnostic.message}
              </Text>
            ))}
            {preview.diffTruncated && (
              <Alert color="yellow">
                Diff усечён. Применение запрещено; требуется более узкий запрос.
              </Alert>
            )}
            <Button
              {...pinButtonProps}
              data-testid="api-pin-apply"
              disabled={busy || conflict || !preview.canApply || preview.diffTruncated}
              onClick={() => void apply()}
            >
              {attempt ? "Повторить точную попытку API" : "Применить связи API"}
            </Button>
          </Stack>
        )}
        {editing && !attempt && (
          <Button
            {...pinButtonProps}
            variant="default"
            disabled={busy}
            onClick={() => {
              invalidatePreview();
              setEditing(false);
            }}
          >
            Отменить изменение связи API
          </Button>
        )}
      </Stack>
    </Paper>
  );
}
