import { readBackendCoverage } from "./backendGraphReads";
import {
  projectionTarget,
  projectionKey,
  projectionReturnTarget,
  projectionURLTarget,
  type ProjectionReadProps,
} from "./backendEffectiveProjectionReads";
import { useContext, useEffect, useId, useRef, useState } from "react";
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
  getBackendRevision,
  previewBackendArtifactPins,
} from "@/api/generated/backend-projects/backend-projects";
import type {
  ApplyBackendArtifactPinsRequest,
  ArtifactPinCommand,
  ArtifactPinsPreview,
  ArtifactProjectionPage,
  EditorSelector,
  PreviewBackendArtifactPinsRequest,
  QueryBackendArtifactsRequest,
} from "@/api/generated/schemas";
import { ApiFailure } from "@/api/client";
import { BackendAPIArtifactsContext } from "./BackendAPIArtifacts";
import { completeArtifactSet, readArtifactPage, selectorIdentity } from "./backendArtifactReads";
import { exactArtifactId, safeLegacyId, type APIArtifactScope } from "./backendAPIArtifactReads";
import {
  databaseButtonStyles,
  databaseWrap,
  useDatabaseCancellation,
} from "./backendDatabaseReads";
import { LoadState } from "./BackendGraphInventory";
import { ArtifactDeltaSide, ArtifactTypedContent } from "./BackendArtifactContent";
import { PinnedScenarioArtifact } from "../design-canvas/PinnedScenarioArtifact";
import { PinnedAPIArtifact } from "../api-designer/PinnedAPIArtifact";
const button = { h: "auto", py: "xs", maw: "100%", styles: databaseButtonStyles };
type Props = ProjectionReadProps & {
  projectId: string;
  sourceNodeId?: string;
  readOnly?: boolean;
  initialArtifact?: string;
  initialView?: string;
  initialEmbedded?: string;
};
export function BackendArtifactProjections(props: Props) {
  return (
    <ProjectionPanel
      key={`${props.projectId}:${projectionKey(projectionTarget(props), props.pins)}:${props.sourceNodeId ?? "all"}`}
      {...props}
    />
  );
}
function fault(error: unknown) {
  if (error instanceof ApiFailure && error.status === 413)
    return `${error.message}. Сузьте набор объектов или связей: лимит полного v2-контекста 4 MiB отдельно от лимита построения event_model и тела команды. Размеры: ${JSON.stringify(error.details ?? {})}`;
  return error instanceof Error
    ? error.message
    : "Не удалось прочитать или изменить закреплённый контекст.";
}
const hostInitialView = "";
function ProjectionPanel({
  projectId,
  revisionId: selectedRevision,
  target: explicitTarget,
  pins: providedPins,
  sourceNodeId,
  readOnly = false,
  initialArtifact = "",
  initialView = hostInitialView,
  initialEmbedded = "",
}: Props) {
  const target = projectionTarget({
    revisionId: selectedRevision,
    target: explicitTarget,
    pins: providedPins,
  });
  const contextKey = [
    "artifact-projection-context",
    projectId,
    projectionKey(target, providedPins),
  ];
  useDatabaseCancellation(contextKey);
  const contextQuery = useQuery({
    queryKey: contextKey,
    enabled: !providedPins && !!explicitTarget,
    queryFn: ({ signal }) => readBackendCoverage(projectId, target, signal),
    staleTime: Infinity,
    retry: false,
  });
  const effectivePins =
    providedPins ??
    (contextQuery.data && "pins" in contextQuery.data ? contextQuery.data.pins : undefined);
  const revisionId = effectivePins?.baseRevisionId ?? target.revisionId ?? selectedRevision ?? "";
  readOnly = readOnly || !!target.changeProposal || !!target.proposal;

  const host = useContext(BackendAPIArtifactsContext);
  const instanceId = useId();
  const revisionQuery = useQuery({
    queryKey: ["artifact-projection-revision", projectId, projectionKey(target, effectivePins)],
    enabled:
      !effectivePins &&
      !!target.revisionId &&
      (!explicitTarget || contextQuery.isSuccess) &&
      host?.revision.id !== revisionId,
    retry: false,
    queryFn: async ({ signal }) => {
      const response = await getBackendRevision(projectId, revisionId, { signal });
      signal.throwIfAborted();
      if (
        response.status !== 200 ||
        response.data.id !== revisionId ||
        response.data.projectId !== projectId
      )
        throw new Error("Неверная ревизия проекции");
      return response.data;
    },
  });
  const revision =
    !target.changeProposal && (!explicitTarget || providedPins || contextQuery.isSuccess)
      ? host?.revision.id === revisionId
        ? host.revision
        : revisionQuery.data
      : undefined;
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
  const [selection, setSelection] = useState(initialArtifact);
  const [view, setView] = useState(
    initialView ||
      (host?.revision.artifactPins.find((p) => ["api_design", "design_scenario"].includes(p.kind))
        ?.kind === "api_design"
        ? "states"
        : "sequence"),
  );
  const [embedded, setEmbedded] = useState(initialEmbedded);
  const [filter, setFilter] = useState("");
  const [cursors, setCursors] = useState([""]);
  const [raw, setRaw] = useState(false);
  const [editing, setEditing] = useState(false);
  const [ownerKind, setOwnerKind] = useState("design_scenario");
  const [ownerId, setOwnerId] = useState("");
  const [ownerRevision, setOwnerRevision] = useState("");
  const [reason, setReason] = useState("");
  const [bindings, setBindings] = useState<{ selector: EditorSelector; sourceNodeIds: string[] }[]>(
    [],
  );
  const [apiBindings, setAPIBindings] = useState<
    { sourceNodeId: string; selector: { objectKey: string } | { jsonPointer: string } }[]
  >([]);
  const [sourceIds, setSourceIds] = useState(sourceNodeId ?? "");
  const [capture, setCapture] = useState<{
    scope: APIArtifactScope;
    version: number;
    page?: ArtifactProjectionPage;
  } | null>(null);
  const [preview, setPreview] = useState<ArtifactPinsPreview | null>(null);
  const [candidate, setCandidate] = useState<PreviewBackendArtifactPinsRequest | null>(null);
  const [attempt, setAttempt] = useState<ApplyBackendArtifactPinsRequest | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [conflict, setConflict] = useState(false);
  const [unknown, setUnknown] = useState(false);
  const request = useRef({
    alive: true,
    generation: 0,
    controller: null as AbortController | null,
  });
  const latest = useRef(host);
  useEffect(() => {
    latest.current = host;
  }, [host]);
  const first = useRef<ArtifactProjectionPage | undefined>(undefined);
  const trigger = useRef<HTMLButtonElement>(null);
  const rawTrigger = useRef<HTMLButtonElement>(null);
  const identity = `${projectId}:${revisionId}:projections:${sourceNodeId ?? "all"}:${instanceId}`;
  const dirty = editing || !!attempt;
  const onDirty = host?.onDirty;
  useEffect(() => {
    onDirty?.(dirty, identity);
    return () => onDirty?.(false, identity);
  }, [dirty, identity, onDirty]);
  useEffect(() => {
    const r = request.current;
    r.alive = true;
    return () => {
      r.alive = false;
      r.generation++;
      r.controller?.abort();
    };
  }, []);
  useEffect(() => {
    const listener = (e: BeforeUnloadEvent) => {
      if (dirty) {
        e.preventDefault();
        e.returnValue = "";
      }
    };
    window.addEventListener("beforeunload", listener);
    return () => window.removeEventListener("beforeunload", listener);
  }, [dirty]);
  const pins = (effectivePins?.artifactPins ?? revision?.artifactPins ?? []).filter(
    (p) => ["api_design", "design_scenario"].includes(p.kind) && "contentHash" in p,
  );
  const pin = selection ? pins.find((p) => `${p.kind}:${p.id}` === selection) : pins[0];
  let input: QueryBackendArtifactsRequest | undefined;
  if (pin?.kind === "api_design" && (view === "states" || view === "response_rules"))
    input = {
      ...target,
      artifact: { kind: "api_design", id: pin.id },
      view,
      limit: 50,
      cursor: cursors.at(-1),
    };
  if (pin?.kind === "design_scenario") {
    if (view === "sequence" || view === "event_model")
      input = {
        ...target,
        artifact: { kind: "design_scenario", id: pin.id },
        view,
        limit: 50,
        cursor: cursors.at(-1),
      };
    else if ((view === "states" || view === "response_rules") && embedded)
      input = {
        ...target,
        artifact: { kind: "design_scenario", id: pin.id },
        view,
        embeddedContractId: embedded,
        limit: 50,
        cursor: cursors.at(-1),
      };
  }
  const key = ["artifact-projections", JSON.stringify(scope), JSON.stringify(input)];
  useDatabaseCancellation(key);
  const query = useQuery({
    queryKey: key,
    enabled: !!scope && !!input,
    retry: false,
    queryFn: async ({ signal }) => {
      const page = await readArtifactPage(
        scope!,
        input!,
        signal,
        cursors.length > 1 ? first.current : undefined,
      );
      if (cursors.length === 1) first.current = page;
      return page;
    },
  });
  const page = query.data;
  function reset() {
    first.current = undefined;
    setCursors([""]);
    setRaw(false);
  }
  function change(action: () => void) {
    if (dirty && host?.guard && !host.guard()) return;
    request.current.generation++;
    request.current.controller?.abort();
    setBusy(false);
    setEditing(false);
    setAttempt(null);
    setPreview(null);
    setCapture(null);
    action();
    reset();
  }
  function invalidate() {
    request.current.generation++;
    request.current.controller?.abort();
    setPreview(null);
    setCandidate(null);
    setError(null);
    setConflict(false);
    setBusy(false);
  }
  function begin(fresh = false) {
    if (!scope || !host || busy || attempt || (!fresh && !page)) return;
    if (host.claim && !host.claim(identity)) {
      setError("Завершите изменение в другом инспекторе, включая неизвестную попытку.");
      return;
    }
    invalidate();
    setCapture({
      scope: structuredClone(scope),
      version: host.projectVersion,
      ...(!fresh ? { page: structuredClone(page!) } : {}),
    });
    setEditing(true);
    setOwnerKind(fresh ? "design_scenario" : pin!.kind);
    setOwnerId(fresh ? "" : pin!.id);
    setOwnerRevision(fresh ? "" : pin!.revisionId);
    setReason("");
    setBindings(
      fresh
        ? []
        : page!.editorBindings.map(({ selector, sourceNodeIds }) => ({ selector, sourceNodeIds })),
    );
    setAPIBindings(
      fresh
        ? []
        : page!.apiBindings.map((b) => ({
            sourceNodeId: b.sourceNodeId,
            selector: b.ref.selector,
          })),
    );
  }
  function close() {
    if (busy || attempt) return;
    invalidate();
    setEditing(false);
    setCapture(null);
    requestAnimationFrame(() => trigger.current?.focus());
  }
  async function recoverCurrentHead() {
    if (busy || attempt || !capture) return;
    const currentHost = latest.current;
    if (!currentHost?.reread || !currentHost.onCurrentHead) {
      setError("Невозможно перечитать и открыть текущий источник.");
      return;
    }
    const selectedOwner = capture.page?.selectedPin ?? { kind: ownerKind, id: ownerId };
    const generation = ++request.current.generation;
    request.current.controller?.abort();
    const controller = new AbortController();
    request.current.controller = controller;
    setBusy(true);
    setError(null);
    try {
      const fresh = await currentHost.reread();
      controller.signal.throwIfAborted();
      if (!request.current.alive || request.current.generation !== generation) return;
      if (
        fresh.revision.projectId !== projectId ||
        !fresh.revision.id ||
        !Number.isSafeInteger(fresh.projectVersion) ||
        fresh.projectVersion < 1
      )
        throw new Error("Получен другой текущий источник.");
      const freshScope: APIArtifactScope = {
        projectId,
        revisionId: fresh.revision.id,
        semanticHash: fresh.revision.semanticHash,
        sourceSnapshotIds: fresh.revision.sourceSnapshotIds,
        artifactPins: fresh.revision.artifactPins,
      };
      const freshPin = freshScope.artifactPins.find(
        (p) => p.kind === selectedOwner.kind && p.id === selectedOwner.id,
      );
      if (capture.page && !freshPin)
        throw new Error(
          `Выбранная группа ${selectedOwner.kind} ${selectedOwner.id} больше не закреплена в текущей ревизии ${fresh.revision.id}. Откройте текущую ревизию из истории и выберите группу явно.`,
        );
      if (freshPin) {
        const capturedView =
          capture.page?.view ?? (freshPin.kind === "api_design" ? "states" : "sequence");
        let next: QueryBackendArtifactsRequest;
        if (
          freshPin.kind === "api_design" &&
          (capturedView === "states" || capturedView === "response_rules")
        )
          next = {
            revisionId: freshScope.revisionId,
            artifact: { kind: "api_design", id: freshPin.id },
            view: capturedView,
            limit: 1,
          };
        else if (
          freshPin.kind === "design_scenario" &&
          (capturedView === "sequence" || capturedView === "event_model")
        )
          next = {
            revisionId: freshScope.revisionId,
            artifact: { kind: "design_scenario", id: freshPin.id },
            view: capturedView,
            limit: 1,
          };
        else if (
          freshPin.kind === "design_scenario" &&
          (capturedView === "states" || capturedView === "response_rules") &&
          capture.page?.embeddedContractId
        )
          next = {
            revisionId: freshScope.revisionId,
            artifact: { kind: "design_scenario", id: freshPin.id },
            view: capturedView,
            embeddedContractId: capture.page.embeddedContractId,
            limit: 1,
          };
        else throw new Error("Текущее представление группы требует явного выбора.");
        await readArtifactPage(freshScope, next, controller.signal);
      }
      controller.signal.throwIfAborted();
      if (!request.current.alive || request.current.generation !== generation) return;
      const owner = latest.current;
      if (
        owner?.revision.id !== revisionId ||
        owner.projectVersion > fresh.projectVersion ||
        !owner.onCurrentHead?.(fresh, identity)
      )
        throw new Error(
          "Текущий выбор или версия проекта изменились во время чтения. Перечитайте источник явно.",
        );
      invalidate();
      setEditing(false);
      setCapture(null);
    } catch (failure) {
      if (
        request.current.alive &&
        request.current.generation === generation &&
        !controller.signal.aborted
      )
        setError(fault(failure));
    } finally {
      if (request.current.alive && request.current.generation === generation) setBusy(false);
    }
  }
  async function makePreview(remove = false) {
    if (
      !capture ||
      busy ||
      attempt ||
      !reason.trim() ||
      new TextEncoder().encode(reason).length > 4096 ||
      !exactArtifactId(ownerId) ||
      (!remove && !exactArtifactId(ownerRevision))
    )
      return;
    if (
      !capture.page &&
      capture.scope.artifactPins.some((p) => p.kind === ownerKind && p.id === ownerId)
    ) {
      setError("Эта группа уже закреплена. Выберите её и измените полный перечень связей явно.");
      return;
    }
    let command: ArtifactPinCommand;
    if (remove)
      command = {
        type: "remove_artifact_pin",
        artifact: { kind: ownerKind as "api_design" | "design_scenario", id: ownerId },
        reason: reason.trim(),
      };
    else if (capture.page)
      command = completeArtifactSet(
        capture.page,
        ownerRevision,
        reason.trim(),
        bindings,
        apiBindings,
      );
    else if (ownerKind === "api_design")
      command = {
        type: "set_artifact_pin",
        artifact: { kind: "api_design", id: ownerId },
        revisionId: ownerRevision,
        apiBindings: [],
        editorBindings: [],
        reason: reason.trim(),
      };
    else
      command = {
        type: "set_artifact_pin",
        artifact: { kind: "design_scenario", id: ownerId },
        revisionId: ownerRevision,
        editorBindings: [],
        reason: reason.trim(),
      };
    const body: PreviewBackendArtifactPinsRequest = {
      baseRevisionId: capture.scope.revisionId,
      expectedVersion: capture.version,
      commands: [command],
    };
    invalidate();
    const generation = ++request.current.generation;
    const controller = new AbortController();
    request.current.controller = controller;
    setBusy(true);
    try {
      const response = await previewBackendArtifactPins(projectId, body, {
        signal: controller.signal,
      });
      controller.signal.throwIfAborted();
      if (response.status !== 200) throw new Error("Неизвестный ответ предпросмотра");
      const p = response.data;
      if (
        p.baseRevisionId !== body.baseRevisionId ||
        p.expectedVersion !== body.expectedVersion ||
        JSON.stringify([...p.sourceSnapshotIds].sort()) !==
          JSON.stringify([...capture.scope.sourceSnapshotIds].sort())
      )
        throw new Error("Другой контекст предпросмотра");
      if (request.current.alive && request.current.generation === generation) {
        setCandidate(structuredClone(body));
        setPreview(p);
      }
    } catch (e) {
      if (
        request.current.alive &&
        request.current.generation === generation &&
        !controller.signal.aborted
      ) {
        setError(fault(e));
        setConflict(e instanceof ApiFailure && e.status === 409);
      }
    } finally {
      if (request.current.alive && request.current.generation === generation) setBusy(false);
    }
  }
  async function apply() {
    if (
      busy ||
      conflict ||
      (!attempt && (!candidate || !preview?.canApply || preview.diffTruncated))
    )
      return;
    const body = attempt ?? {
      ...candidate!,
      candidateHash: preview!.candidateHash,
      idempotencyKey: crypto.randomUUID(),
    };
    if (
      !/^[\x20-\x7e]{1,128}$/.test(body.idempotencyKey) ||
      new TextEncoder().encode(JSON.stringify(body)).length > 128 * 1024
    ) {
      setError("Тело применения превышает 128 KiB. Сузьте запрос.");
      return;
    }
    const generation = request.current.generation;
    setAttempt(body);
    setBusy(true);
    setError(null);
    try {
      const response = await applyBackendArtifactPins(projectId, body);
      if (
        response.status !== 200 ||
        response.data.project.id !== projectId ||
        response.data.revision.projectId !== projectId ||
        response.data.revision.parentRevisionId !== body.baseRevisionId
      )
        throw new Error("Неизвестный контекст результата применения");
      if (request.current.alive && request.current.generation === generation) {
        setAttempt(null);
        setUnknown(false);
        setEditing(false);
        setPreview(null);
        setCandidate(null);
        if (
          latest.current?.revision.id === body.baseRevisionId &&
          latest.current.projectVersion === body.expectedVersion
        )
          latest.current.onApplied(response.data);
        else
          setError(
            "Применение подтверждено. Откройте результат из истории: текущий контекст уже изменился.",
          );
        requestAnimationFrame(() => trigger.current?.focus());
      }
    } catch (e) {
      if (!request.current.alive || request.current.generation !== generation) return;
      const known =
        e instanceof ApiFailure &&
        e.status >= 400 &&
        e.status < 500 &&
        ![408, 429].includes(e.status);
      setUnknown(!known);
      if (known) setAttempt(null);
      if (known && e instanceof ApiFailure && [413, 422].includes(e.status)) {
        setPreview((value) => (value ? { ...value, canApply: false } : value));
      }
      setConflict(e instanceof ApiFailure && e.status === 409);
      setError(fault(e));
    } finally {
      if (request.current.alive && request.current.generation === generation) setBusy(false);
    }
  }
  const visible = (page?.items ?? []).filter(
    (item) =>
      (!sourceNodeId || item.sourceNodeIds.includes(sourceNodeId)) &&
      (!filter || item.label.toLowerCase().includes(filter.toLowerCase())),
  );
  const pinnedContext =
    pin && "contentHash" in pin && typeof pin.contentHash === "string"
      ? {
          pinnedRevisionId: pin.revisionId,
          pinnedHash: pin.contentHash,
          returnProjectId: projectId,
          ...projectionReturnTarget(target, revisionId),
        }
      : undefined;
  const href =
    pin &&
    pinnedContext &&
    safeLegacyId(pin.id) !== undefined &&
    safeLegacyId(pin.revisionId) !== undefined
      ? `${pin.kind === "api_design" ? "/designs" : "/design-scenarios"}/${pin.id}${defaultStringifySearch({ ...pinnedContext, projectionView: view, ...(embedded ? { embeddedContractId: embedded } : {}) })}`
      : undefined;
  return (
    <Paper
      component="section"
      withBorder
      p="md"
      aria-label="Сценарии и модели"
      data-testid="backend-artifact-projections"
      style={{ minWidth: 0, maxWidth: "100%" }}
    >
      <Stack>
        <Title order={3}>Сценарии и модели</Title>
        <Text size="sm">
          Авторские модели описывают сохранённое намерение. Связи с исходниками задаются вручную;
          полнота модели не доказывает поведение во время исполнения.
        </Text>
        <Text size="xs" style={databaseWrap}>
          Бэкенд {revisionId} · хеш {scope?.semanticHash} · исходные снимки{" "}
          {scope?.sourceSnapshotIds.join(", ")}
        </Text>
        {!providedPins && explicitTarget && (
          <LoadState query={contextQuery} label="контекста модели" />
        )}
        {!revision && !effectivePins && (!explicitTarget || contextQuery.isSuccess) && (
          <LoadState query={revisionQuery} label="ревизии моделей" />
        )}
        <NativeSelect
          label="Закреплённый артефакт"
          value={pin ? `${pin.kind}:${pin.id}` : ""}
          data={pins.map((p) => ({
            value: `${p.kind}:${p.id}`,
            label: `${p.kind} ${p.id} · ${p.revisionId}`,
          }))}
          disabled={busy}
          onChange={(e) =>
            change(() => {
              const p = pins.find((p) => `${p.kind}:${p.id}` === e.currentTarget.value);
              setSelection(e.currentTarget.value);
              setView(p?.kind === "api_design" ? "states" : "sequence");
              setEmbedded("");
            })
          }
        />
        <NativeSelect
          label="Представление модели"
          value={view}
          disabled={busy}
          data={(pin?.kind === "api_design"
            ? ["states", "response_rules"]
            : ["sequence", "states", "response_rules", "event_model"]
          ).map((value) => ({
            value,
            label: {
              sequence: "Последовательность",
              states: "Состояния",
              response_rules: "Правила ответа",
              event_model: "Событийная модель",
            }[value]!,
          }))}
          onChange={(e) => change(() => setView(e.currentTarget.value))}
        />
        {pin?.kind === "design_scenario" && (view === "states" || view === "response_rules") && (
          <TextInput
            label="Точный ID вложенного контракта"
            value={embedded}
            disabled={busy}
            onChange={(e) => change(() => setEmbedded(e.currentTarget.value))}
          />
        )}
        <TextInput
          label="Фильтр авторских строк"
          value={filter}
          disabled={busy}
          onChange={(e) => change(() => setFilter(e.currentTarget.value))}
        />
        {pin && (
          <Text style={databaseWrap}>
            {pin.kind} {pin.id} · ревизия {pin.revisionId} ·{" "}
            {"contentHash" in pin ? String(pin.contentHash) : "хеш недоступен"}
          </Text>
        )}
        {query.isError ? (
          <Alert color="red" role="alert">
            {fault(query.error)}
            <Button {...button} onClick={() => void query.refetch()}>
              Повторить чтение
            </Button>
          </Alert>
        ) : (
          input && <LoadState query={query} label="проекции модели" />
        )}
        {!pin && <Text>Закреплённых моделей нет.</Text>}
        {page && (
          <>
            <Badge>{page.resolution.status}</Badge>
            <Text style={databaseWrap}>
              Текущий черновик: {page.resolution.currentDraftRevisionId ?? "неизвестен"}
              {page.resolution.updateAvailable ? " · доступно обновление" : ""}
            </Text>
            <Text>
              Полный перечень связей: {page.apiBindings.length} API + {page.editorBindings.length}{" "}
              объектов. Авторское покрытие: {page.complete ? "полное" : "частичное"}; строк{" "}
              {page.coverage.itemsReturned} из {page.coverage.totalItems}.
            </Text>
            {page.coverage.truncatedReasons.map((r) => (
              <Text key={r} c="orange" style={databaseWrap}>
                Неполное построение: {r}. Сузьте модель; точный снимок и связи доступны.
              </Text>
            ))}
            {[...page.diagnostics, ...page.resolution.diagnostics].map((d, i) => (
              <Text key={i} style={databaseWrap}>
                {d.code}: {d.message}
              </Text>
            ))}
            <Title order={4}>Полный закреплённый перечень</Title>
            {page.editorBindings.map((b) => (
              <Stack key={selectorIdentity(b.selector)} gap="xs">
                <Text fw={600} style={databaseWrap}>
                  {b.lastKnownLabel}
                </Text>
                <Text style={databaseWrap}>
                  {selectorIdentity(b.selector)} · {b.sourceNodeIds.join(", ")} ·{" "}
                  {b.sourceLabels.join(", ")} · {b.reason}
                </Text>
              </Stack>
            ))}
            {page.apiBindings.map((b) => (
              <Text key={b.sourceNodeId} style={databaseWrap}>
                API {b.sourceLastKnownLabel} · {b.sourceNodeId} · {JSON.stringify(b.ref.selector)}
              </Text>
            ))}
            <Title order={4}>Авторское содержимое</Title>
            {visible.length === 0 && (
              <Text>
                На этой странице нет подходящих авторских строк. Полный перечень связей сохранён
                выше.
              </Text>
            )}
            {visible.map((item) => (
              <Stack key={`${item.id}:${item.locator.owner.pointer}`} gap="xs">
                <Text fw={600} style={databaseWrap}>
                  {item.label} · {item.kind}
                </Text>
                <Text size="xs" style={databaseWrap}>
                  {item.locator.owner.pointer} · хеш объекта {item.objectHash}
                </Text>
                {item.locator.embedded && (
                  <>
                    <Badge>
                      {item.locator.embedded.mode} · {item.locator.embedded.originStatus}
                    </Badge>
                    <Text size="sm" style={databaseWrap}>
                      Вложенный контракт {item.locator.embedded.contractId} · хеш{" "}
                      {item.locator.embedded.documentHash}
                      {item.locator.embedded.origin
                        ? ` · происхождение ${item.locator.embedded.origin.designId}/${item.locator.embedded.origin.revisionId}, версия ${item.locator.embedded.origin.version}`
                        : " · происхождение не задано"}
                    </Text>
                  </>
                )}
                <ArtifactTypedContent data={item.data} />
                {item.diagnostics.map((d, i) => (
                  <Text key={i} style={databaseWrap}>
                    {d.code}: {d.message}
                  </Text>
                ))}
                {item.sourceNodeIds.map((id) => (
                  <Button
                    key={id}
                    {...button}
                    component="a"
                    variant="subtle"
                    href={`/backend-projects/${projectId}${defaultStringifySearch({ ...projectionURLTarget(target, revisionId), recordType: "node", recordId: id })}`}
                  >
                    Исходный узел {id}
                  </Button>
                ))}
                {editing && item.bindingSelector && (
                  <Button
                    {...button}
                    disabled={busy || !!attempt}
                    onClick={() => {
                      const ids = [...new Set(sourceIds.split(/[\s,]+/).filter(Boolean))].sort();
                      if (!ids.length || ids.length > 100) {
                        setError("Укажите от 1 до 100 точных исходных узлов.");
                        return;
                      }
                      invalidate();
                      setBindings((old) => [
                        ...old.filter(
                          (b) =>
                            selectorIdentity(b.selector) !==
                            selectorIdentity(item.bindingSelector!),
                        ),
                        { selector: item.bindingSelector!, sourceNodeIds: ids },
                      ]);
                    }}
                  >
                    Связать выбранный объект
                  </Button>
                )}
              </Stack>
            ))}
            <Group>
              <Button
                {...button}
                disabled={cursors.length === 1 || busy || !!attempt}
                onClick={() => setCursors((old) => old.slice(0, -1))}
              >
                Предыдущие строки
              </Button>
              <Button
                {...button}
                disabled={!page.nextCursor || busy || !!attempt}
                onClick={() => {
                  if (cursors.includes(page.nextCursor)) {
                    setError("Повтор курсора; перечитайте ревизию.");
                    return;
                  }
                  setCursors((old) => [...old, page.nextCursor]);
                }}
              >
                Следующие строки
              </Button>
            </Group>
          </>
        )}
        {pinnedContext && (
          <Group>
            <Button
              {...button}
              ref={rawTrigger}
              aria-expanded={raw}
              onClick={() => setRaw((old) => !old)}
            >
              Точный сырой снимок
            </Button>
            {href ? (
              <Button
                {...button}
                component="a"
                href={href}
                target={dirty ? "_blank" : undefined}
                rel={dirty ? "noopener noreferrer" : undefined}
              >
                Открыть закреплённую модель в редакторе
              </Button>
            ) : (
              <Text size="sm">
                Точные ID доступны в снимке. Числовой редактор не поддерживает безопасную навигацию.
              </Text>
            )}
          </Group>
        )}
        {raw &&
          pinnedContext &&
          pin &&
          (pin.kind === "design_scenario" ? (
            <PinnedScenarioArtifact
              artifactId={pin.id}
              pin={pinnedContext}
              onClose={() => {
                setRaw(false);
                requestAnimationFrame(() => rawTrigger.current?.focus());
              }}
            />
          ) : (
            <PinnedAPIArtifact
              artifactId={pin.id}
              pin={pinnedContext}
              currentRevisionId="неизвестен"
              dirty={false}
              onOpenCurrent={() => setRaw(false)}
            />
          ))}
        {host?.canEdit && host.revision.id === revisionId && !readOnly && !editing && (
          <Group>
            <Button {...button} ref={trigger} onClick={() => begin()} disabled={!page || busy}>
              Изменить закреплённую группу
            </Button>
            <Button {...button} onClick={() => begin(true)} disabled={!scope || busy}>
              Закрепить сценарий или API
            </Button>
          </Group>
        )}
        {editing && (
          <Stack aria-label="Редактор связей модели">
            <Title order={4}>Явное изменение полной группы</Title>
            <NativeSelect
              label="Тип владельца"
              value={ownerKind}
              data={[
                { value: "design_scenario", label: "Сценарий" },
                { value: "api_design", label: "API" },
              ]}
              disabled={!!capture?.page || busy || !!attempt}
              onChange={(e) => {
                invalidate();
                setOwnerKind(e.currentTarget.value);
              }}
            />
            <TextInput
              label="Точный ID владельца"
              value={ownerId}
              disabled={!!capture?.page || busy || !!attempt}
              onChange={(e) => {
                invalidate();
                setOwnerId(e.currentTarget.value);
              }}
            />
            <TextInput
              label="Точная ревизия владельца"
              value={ownerRevision}
              disabled={busy || !!attempt}
              onChange={(e) => {
                invalidate();
                setOwnerRevision(e.currentTarget.value);
              }}
            />
            <TextInput
              label="Причина изменения"
              value={reason}
              disabled={busy || !!attempt}
              onChange={(e) => {
                invalidate();
                setReason(e.currentTarget.value);
              }}
            />
            <TextInput
              label="Точные исходные узлы для выбранного объекта"
              value={sourceIds}
              disabled={busy || !!attempt}
              onChange={(e) => setSourceIds(e.currentTarget.value)}
            />
            <Text>
              Сохраняются все связи выбранной группы, включая другие представления и недоступные
              объекты. Для привязки используйте кнопку рядом с поддерживаемой авторской строкой.
            </Text>
            {bindings.map((b) => (
              <Group key={selectorIdentity(b.selector)}>
                <Text style={databaseWrap}>
                  {selectorIdentity(b.selector)} → {b.sourceNodeIds.join(", ")}
                </Text>
                <Button
                  {...button}
                  disabled={busy || !!attempt}
                  onClick={() => {
                    invalidate();
                    setBindings((old) =>
                      old.filter(
                        (x) => selectorIdentity(x.selector) !== selectorIdentity(b.selector),
                      ),
                    );
                  }}
                >
                  Удалить связь объекта
                </Button>
              </Group>
            ))}
            {apiBindings.map((b) => (
              <Group key={b.sourceNodeId}>
                <Text style={databaseWrap}>API → {b.sourceNodeId}</Text>
                <Button
                  {...button}
                  disabled={busy || !!attempt}
                  onClick={() => {
                    invalidate();
                    setAPIBindings((old) => old.filter((x) => x.sourceNodeId !== b.sourceNodeId));
                  }}
                >
                  Удалить связь API
                </Button>
              </Group>
            ))}
            <Group>
              <Button {...button} disabled={busy || !!attempt} onClick={() => void makePreview()}>
                Предпросмотр полной группы
              </Button>
              {capture?.page && (
                <Button
                  {...button}
                  color="red"
                  disabled={busy || !!attempt}
                  onClick={() => void makePreview(true)}
                >
                  Предпросмотр удаления всей группы
                </Button>
              )}
              <Button {...button} disabled={busy || !!attempt} onClick={close}>
                Отмена изменения модели
              </Button>
            </Group>
            {preview && (
              <Stack aria-label="Предпросмотр модели">
                <Text style={databaseWrap}>
                  База {preview.baseRevisionId} · версия {preview.expectedVersion} · кандидат{" "}
                  {preview.candidateHash}
                </Text>
                {preview.diff.map((d) => (
                  <Stack key={d.identity}>
                    <Text style={databaseWrap}>
                      {d.kind} · {d.status} · {d.identity}
                      {d.contextChanged ? " · контекст изменён" : ""}
                    </Text>
                    <Title order={5}>До</Title>
                    <ArtifactDeltaSide
                      side={d.before}
                      projectId={projectId}
                      revisionId={revisionId}
                      target={target}
                      pins={effectivePins}
                    />
                    <Title order={5}>После</Title>
                    <ArtifactDeltaSide
                      side={d.after}
                      projectId={projectId}
                      revisionId={revisionId}
                      target={target}
                      pins={effectivePins}
                    />
                    {d.changes.map((c, i) => (
                      <Code key={i} block style={databaseWrap}>
                        {JSON.stringify(c)}
                      </Code>
                    ))}
                  </Stack>
                ))}
                {preview.diagnostics.map((d, i) => (
                  <Text key={i} style={databaseWrap}>
                    {d.code}: {d.message}
                  </Text>
                ))}
                {preview.diffTruncated && (
                  <Alert color="yellow">
                    Предпросмотр усечён; применение запрещено. Сузьте запрос.
                  </Alert>
                )}
              </Stack>
            )}
            {unknown && (
              <Alert color="yellow">
                Результат неизвестен. Повтор отправит тот же сохранённый body и ключ; текущие данные
                владельца не заменяют исходную попытку.
              </Alert>
            )}
            <Button
              {...button}
              disabled={
                busy || conflict || (!attempt && (!preview?.canApply || preview.diffTruncated))
              }
              onClick={() => void apply()}
            >
              {attempt ? "Повторить точное применение" : "Применить показанную группу"}
            </Button>
            {conflict && (
              <Button {...button} disabled={busy} onClick={() => void recoverCurrentHead()}>
                Закрыть конфликт и перечитать контекст
              </Button>
            )}
          </Stack>
        )}
        {error && (
          <Alert color="red" role="alert">
            {error}
          </Alert>
        )}
      </Stack>
    </Paper>
  );
}
