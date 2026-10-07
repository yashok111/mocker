import { BackendObservations } from "./BackendObservations";
import { BackendObservationProvider } from "./BackendObservationContext";
import { BackendDiagramReplayProvider } from "./BackendDiagramReplayContext";
import { BackendReplay } from "./BackendReplay";
import { BackendPortable } from "./BackendPortable";
import { BackendMaterialization } from "./BackendMaterialization";
import { BackendDiagramChangeProvider } from "./BackendDiagramChangeContext";
import { BackendArchitecture } from "./BackendArchitecture";
import { BackendWorkspaceNavigation } from "./BackendWorkspaceNavigation";
import { parseBackendSourcePin } from "./backendFlowReads";
import type { BackendDiagramTarget } from "@/api/generated/schemas";
import type { BackendWorkspaceSearch } from "./backendWorkspaceSearch";
import { BackendLegacyRecoveryNotice } from "./BackendLegacyRecoveryNotice";
import { BackendAnalysisJobs } from "./BackendAnalysisJobs";
import {
  BackendAnalysisRecoveryProvider,
  useBackendAnalysisRecovery,
} from "./backendAnalysisRecovery";
import { BackendAnalysisRecoveryNotice } from "./BackendAnalysisRecoveryNotice";
import { BackendAnnotations, type BackendAnnotationsProps } from "./BackendAnnotations";
import { BackendArtifactProjections } from "./BackendArtifactProjections";
import { useCallback, useEffect, useRef, useState } from "react";
import {
  Alert,
  Badge,
  Button,
  Code,
  Group,
  Loader,
  Paper,
  Stack,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { IconArrowLeft } from "@tabler/icons-react";
import { useBlocker, useNavigate } from "@tanstack/react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  getGetBackendProjectQueryKey,
  getGetBackendRevisionQueryKey,
  getBackendRevision,
  getBackendChangeProposal,
  getListBackendProjectsQueryKey,
  useApplyBackendProjectCommands,
  useGetBackendProject,
  useGetBackendRevision,
  useListBackendRevisions,
  getBackendSavedView,
  getGetBackendSavedViewQueryKey,
} from "@/api/generated/backend-projects/backend-projects";
import type {
  ApplyBackendProjectCommandsRequest,
  BackendSavedViewResponse,
  BackendProject,
  BackendReadTarget,
} from "@/api/generated/schemas";
import { ApiFailure } from "@/api/client";
import { describeApiFailureDetailed } from "@/api/errors";
import { BackendGraphInventory } from "./BackendGraphInventory";
import { BackendEffectiveViews } from "./BackendEffectiveViews";
import { BackendRevisionCompare } from "./BackendRevisionCompare";
import { BackendImportReview } from "./BackendImportReview";
import { BackendImportCandidate } from "./BackendImportCandidate";
import { BackendChangeProposals } from "./BackendChangeProposals";
import type { ImportCandidateTarget } from "./backendImportAttempts";
import { BackendDatabase } from "./BackendDatabase";
import { BackendFlow } from "./BackendFlow";
import { BackendEvents } from "./BackendEvents";
import { usePinnedValue, type BackendSourcePin } from "./backendFlowReads";
import { BackendSavedViews } from "./BackendSavedViews";
import { BackendSavedViewContext, savedViewSourcePin } from "./backendSavedViewState";
import { checkBackendSavedView } from "./backendSavedViewReads";
import { backendReadTargetKey } from "./backendReadTargets";
import { verifyChangeDetail } from "./backendChangeReads";
import { readBackendCoverage } from "./backendGraphReads";
import { checkBackendReadPins, BackendReadError } from "./backendReadTargets";
import { LoadState } from "./BackendReadUI";
import { useBackendSavedViewSession } from "./useBackendSavedViewSession";
import { BackendAPIArtifacts, BackendAPIArtifactsContext } from "./BackendAPIArtifacts";

type ProjectPageProps = {
  projectId: string;
  sourcePin?: BackendWorkspaceSearch;
  onSourceNavigate?: (pin: BackendWorkspaceSearch, replace?: boolean) => void;
  initialSaved?: BackendSavedViewResponse;
  initialFullTarget?: BackendReadTarget;
  onImportCandidateChange?: (target: ImportCandidateTarget | null) => void;
};

export function BackendProjectPage(props: ProjectPageProps) {
  const [diagramDirty, setDiagramDirty] = useState(false);
  const [materializationDirty, setMaterializationDirty] = useState(false);
  const [portableDirty, setPortableDirty] = useState(false);
  // Whether the replay <details> is open: BackendReplay polls its runs list
  // only then. Controlled, so a project switch (the providers below are
  // keyed by projectId and remount the <details>) cannot leave this state
  // saying "open" over a freshly closed element. focusWorkspaceRegion opens
  // it by setting `.open`, which fires the same toggle event.
  const [replayOpen, setReplayOpen] = useState(false);
  const [backPins, setBackPins] = useState<BackendWorkspaceSearch[]>([]);
  const navigateDiagram = (pin: BackendWorkspaceSearch, replace = false) => {
    if (!replace && props.sourcePin && JSON.stringify(props.sourcePin) !== JSON.stringify(pin))
      setBackPins((previous) => [...previous, structuredClone(props.sourcePin!)]);
    props.onSourceNavigate?.(pin, replace);
  };
  const diagramDirtyRef = useRef(false);
  const onDiagramDirty = useCallback((value: boolean) => {
    diagramDirtyRef.current = value;
    setDiagramDirty(value);
  }, []);
  const selector = JSON.stringify([
    props.sourcePin?.diagramId,
    props.sourcePin?.diagramVersion,
    props.sourcePin?.diagramHash,
    props.sourcePin?.diagramViewId,
    props.sourcePin?.diagramViewVersion,
  ]);
  const [resolved, setResolved] = useState<{ selector: string; target: BackendDiagramTarget }>();
  const onTargetResolved = useCallback(
    (target: BackendDiagramTarget) =>
      setResolved((previous) =>
        previous?.selector === selector &&
        JSON.stringify(previous.target) === JSON.stringify(target)
          ? previous
          : { selector, target },
      ),
    [selector],
  );
  const diagramSelected = !!(props.sourcePin?.diagramId || props.sourcePin?.diagramViewId);
  const target = resolved?.selector === selector ? resolved.target : undefined;
  const legacyPin =
    diagramSelected && target
      ? parseBackendSourcePin({
          recordId: props.sourcePin?.recordId,
          recordType: props.sourcePin?.recordType,
          ...("revisionId" in target
            ? { revisionId: target.revisionId }
            : {
                changeProposalId: target.changeProposal.proposalId,
                proposalRevisionId: target.changeProposal.proposalRevisionId,
              }),
        })
      : props.sourcePin;
  useBlocker({
    shouldBlockFn: () =>
      (diagramDirtyRef.current || materializationDirty || portableDirty) &&
      !window.confirm(
        "Есть несохранённый mapping или неизвестный результат запроса. Покинуть текущий выбор?",
      ),
    enableBeforeUnload: diagramDirty || materializationDirty || portableDirty,
    withResolver: false,
  });
  return (
    <BackendObservationProvider key={props.projectId}>
      <BackendDiagramReplayProvider key={props.projectId}>
        <BackendDiagramChangeProvider key={props.projectId}>
          <BackendAnalysisRecoveryProvider key={props.projectId} projectId={props.projectId}>
            <BackendAnalysisRecoveryNotice projectId={props.projectId} />
            <BackendLegacyRecoveryNotice projectId={props.projectId} />
            <BackendWorkspaceNavigation />
            <details>
              <summary>Наблюдения и correlation</summary>
              <BackendObservations
                key={props.projectId + "observations"}
                projectId={props.projectId}
              />
            </details>
            <details
              open={replayOpen}
              onToggle={(event) => setReplayOpen(event.currentTarget.open)}
            >
              <summary>Orders replay и тестовые профили</summary>
              <BackendReplay
                key={props.projectId + "replay"}
                projectId={props.projectId}
                open={replayOpen}
              />
            </details>
            <details>
              <summary>Portable: экспорт и импорт проекта</summary>
              <BackendPortable
                key={props.projectId + "portable"}
                projectId={props.projectId}
                onDirty={setPortableDirty}
              />
            </details>
            <BackendMaterialization
              key={props.projectId}
              projectId={props.projectId}
              onDirty={setMaterializationDirty}
            />
            {backPins.length > 0 && (
              <Button
                variant="subtle"
                disabled={diagramDirty}
                onClick={() => {
                  const pin = backPins.at(-1)!;
                  setBackPins((previous) => previous.slice(0, -1));
                  props.onSourceNavigate?.(pin);
                }}
              >
                Назад к точному виду
              </Button>
            )}
            <BackendArchitecture
              projectId={props.projectId}
              search={props.sourcePin ?? {}}
              onNavigate={navigateDiagram}
              onDetailedNavigate={navigateDiagram}
              onDirty={onDiagramDirty}
              onTargetResolved={onTargetResolved}
            />
            <div hidden={diagramSelected && !target}>
              <BackendProjectGate {...props} sourcePin={legacyPin} />
            </div>
          </BackendAnalysisRecoveryProvider>
        </BackendDiagramChangeProvider>
      </BackendDiagramReplayProvider>
    </BackendObservationProvider>
  );
}
function BackendProjectGate(props: ProjectPageProps) {
  const full =
    props.sourcePin?.changeProposalId !== undefined ||
    props.sourcePin?.proposalRevisionId !== undefined;
  if (full && props.sourcePin?.viewId !== undefined)
    return (
      <Alert color="red" role="alert">
        Выберите один точный источник: сохранённый вид или черновик предложения.
      </Alert>
    );
  if (full) return <BackendProjectFullGate key={props.projectId} {...props} />;
  return <BackendProjectSavedGate key={props.projectId} {...props} />;
}

function BackendProjectFullGate(props: ProjectPageProps) {
  const proposalId = props.sourcePin?.changeProposalId ?? "";
  const proposalRevisionId = props.sourcePin?.proposalRevisionId ?? "";
  const target: BackendReadTarget = { changeProposal: { proposalId, proposalRevisionId } };
  let valid = true;
  try {
    backendReadTargetKey(target);
  } catch {
    valid = false;
  }
  const query = useQuery({
    queryKey: ["backend-project-full-target", props.projectId, proposalId, proposalRevisionId],
    enabled: valid,
    retry: false,
    staleTime: Infinity,
    queryFn: async ({ signal }) => {
      const response = await getBackendChangeProposal(
        props.projectId,
        proposalId,
        { proposalRevisionId, limit: 1 },
        { signal },
      );
      signal.throwIfAborted();
      if (response.status !== 200)
        throw new Error("Не удалось прочитать точный черновик предложения");
      const detail = verifyChangeDetail(
        response.data,
        props.projectId,
        proposalId,
        proposalRevisionId,
      );
      backendReadTargetKey({ revisionId: detail.revision.baseRevisionId });
      return detail;
    },
  });
  if (!valid || query.isError)
    return (
      <Alert color="red" role="alert">
        {valid
          ? describeApiFailureDetailed(query.error)
          : "Укажите точные идентификаторы предложения и черновика."}
        <Group>
          <Button disabled={!valid} onClick={() => void query.refetch()}>
            Повторить загрузку предложения
          </Button>
        </Group>
      </Alert>
    );
  if (!query.data) return <Loader aria-label="Загружаем точный черновик" />;
  return (
    <BackendProjectDetail
      {...props}
      initialFullTarget={target}
      sourcePin={{ ...props.sourcePin, revisionId: query.data.revision.baseRevisionId }}
    />
  );
}

function BackendProjectSavedGate(props: ProjectPageProps) {
  const { sourcePin, projectId, onSourceNavigate } = props;
  const viewId = sourcePin?.viewId;
  const version = sourcePin?.viewVersion;
  const valid =
    !!viewId &&
    /^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$/.test(viewId) &&
    (version === undefined || (Number.isSafeInteger(version) && version > 0));
  const queryClient = useQueryClient();
  // Every unversioned open is a fresh intent. Immutable version keys remain reusable.
  const [latestMountId] = useState(() => crypto.randomUUID());
  const resolutionIdentity = JSON.stringify([viewId, version]);
  const [resolution, setResolution] = useState({ identity: resolutionIdentity, generation: 0 });
  const generation =
    resolution.identity === resolutionIdentity ? resolution.generation : resolution.generation + 1;
  if (resolution.identity !== resolutionIdentity)
    setResolution({ identity: resolutionIdentity, generation });
  const versionKey = getGetBackendSavedViewQueryKey(
    projectId,
    viewId ?? "",
    version === undefined ? undefined : { version },
  );
  const key =
    version === undefined
      ? [...versionKey, "latest-intent", latestMountId, String(generation)]
      : versionKey;
  const keyIdentity = JSON.stringify(key);
  useEffect(
    () => () => {
      void queryClient.cancelQueries({ queryKey: JSON.parse(keyIdentity) });
    },
    [queryClient, keyIdentity],
  );
  const query = useQuery({
    queryKey: key,
    enabled: valid,
    retry: false,
    staleTime: Infinity,
    queryFn: async ({ signal }) => {
      const response = await getBackendSavedView(
        projectId,
        viewId!,
        version === undefined ? undefined : { version },
        { signal },
      );
      signal.throwIfAborted();
      if (response.status !== 200) throw new Error("Не удалось прочитать сохранённый вид");
      const value = checkBackendSavedView(response.data, projectId, { id: viewId, version });
      return value;
    },
  });
  const saved = valid ? query.data : undefined;
  const expectedPin = saved ? savedViewSourcePin(saved) : undefined;
  const pinMatches =
    !!expectedPin &&
    Object.keys(sourcePin ?? {}).filter(
      (key) => (sourcePin as Record<string, unknown>)[key] !== undefined,
    ).length === Object.keys(expectedPin).length &&
    Object.entries(expectedPin).every(
      ([key, value]) => (sourcePin as Record<string, unknown> | undefined)?.[key] === value,
    );
  useEffect(() => {
    if (saved && !pinMatches) onSourceNavigate?.(savedViewSourcePin(saved), true);
  }, [saved, pinMatches, onSourceNavigate]);
  if (viewId !== undefined && !saved)
    return (
      <Stack aria-label="Загрузка сохранённого вида">
        {valid && query.isPending && <Loader aria-label="Загружаем сохранённый вид" />}
        {(!valid || query.isError) && (
          <Alert color="red" role="alert">
            {!valid
              ? "Неверный идентификатор или версия сохранённого вида"
              : query.error instanceof Error
                ? query.error.message
                : describeApiFailureDetailed(query.error)}
            <Group>
              <Button disabled={!valid} onClick={() => void query.refetch()}>
                Повторить загрузку вида
              </Button>
              <Button variant="default" onClick={() => onSourceNavigate?.({}, true)}>
                Вернуться к источнику
              </Button>
            </Group>
          </Alert>
        )}
      </Stack>
    );
  return (
    <BackendProjectDetail
      key={projectId}
      {...props}
      initialSaved={saved}
      sourcePin={saved ? savedViewSourcePin(saved) : sourcePin}
    />
  );
}

function BackendProjectDetail({
  projectId,
  sourcePin,
  onSourceNavigate,
  initialSaved,
  initialFullTarget,
  onImportCandidateChange,
}: ProjectPageProps) {
  const analysisRecovery = useBackendAnalysisRecovery(projectId);
  const fullTarget =
    initialFullTarget ??
    (initialSaved?.documentVersion === "saved-view-v2" && initialSaved.target.changeProposal
      ? initialSaved.target
      : undefined);
  const fullPins =
    initialSaved?.documentVersion === "saved-view-v2" && initialSaved.target.changeProposal
      ? initialSaved.pins.effective
      : undefined;
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const query = useGetBackendProject(projectId);
  const project = query.data?.status === 200 ? query.data.data : undefined;
  const [selectedRevisionId, setSelectedRevisionId] = usePinnedValue<string | null>(
    sourcePin?.revisionId,
    sourcePin?.revisionId ?? null,
  );
  const pinIdentity = JSON.stringify(sourcePin ?? {});
  const [pin, setPin] = usePinnedValue<BackendSourcePin>(pinIdentity, sourcePin ?? {});
  const [databaseDirty, setDatabaseDirty] = useState(false);
  const [annotationDirty, setAnnotationDirty] = useState(false);
  const [syncDirty, setSyncDirty] = useState(false);
  const [changeDirty, setChangeDirty] = useState(false);
  const [importCandidate, setImportCandidate] = useState<ImportCandidateTarget | null>(null);
  const onCandidateChange = useCallback(
    (target: ImportCandidateTarget | null) => {
      setImportCandidate(target);
      onImportCandidateChange?.(target);
    },
    [onImportCandidateChange],
  );
  const [annotationSelection, setAnnotationSelection] =
    useState<BackendAnnotationsProps["selection"]>();
  const [annotationSelectionPin, setAnnotationSelectionPin] = useState("");
  const [apiDirty, setAPIDirty] = useState<Record<string, boolean>>({});
  const apiEditorOwner = useRef<string | null>(null);
  const onAPIDirty = useCallback((value: boolean, identity = "panel") => {
    if (value) apiEditorOwner.current = identity;
    else if (apiEditorOwner.current === identity) apiEditorOwner.current = null;
    setAPIDirty((previous) =>
      previous[identity] === value ? previous : { ...previous, [identity]: value },
    );
  }, []);
  const acknowledgedNavigation = useRef(false);
  const session = useBackendSavedViewSession(projectId, initialSaved, (value) => {
    acknowledgedNavigation.current = true;
    setPin(savedViewSourcePin(value));
    setSelectedRevisionId(value.pins.revisionId);
    onSourceNavigate?.(savedViewSourcePin(value), true);
  });
  const dirty =
    analysisRecovery.blocked ||
    changeDirty ||
    syncDirty ||
    annotationDirty ||
    databaseDirty ||
    Object.values(apiDirty).some(Boolean) ||
    session.dirty ||
    !!session.pending;
  useBlocker({
    shouldBlockFn: ({ current, next }) => {
      if (acknowledgedNavigation.current) {
        acknowledgedNavigation.current = false;
        return false;
      }
      const nextPin = next.search as BackendWorkspaceSearch,
        currentPin = current.search as BackendWorkspaceSearch;
      const sameWorkspace =
        current.pathname === next.pathname &&
        nextPin.diagramId === currentPin.diagramId &&
        nextPin.diagramVersion === currentPin.diagramVersion &&
        nextPin.diagramViewId === currentPin.diagramViewId &&
        nextPin.diagramViewVersion === currentPin.diagramViewVersion &&
        nextPin.viewId === currentPin.viewId &&
        nextPin.viewVersion === currentPin.viewVersion &&
        nextPin.revisionId === currentPin.revisionId &&
        nextPin.changeProposalId === currentPin.changeProposalId &&
        nextPin.proposalRevisionId === currentPin.proposalRevisionId &&
        nextPin.recordId === currentPin.recordId &&
        nextPin.recordType === currentPin.recordType &&
        nextPin.entrypointId === currentPin.entrypointId &&
        nextPin.flowId === currentPin.flowId &&
        nextPin.dataNodeId === currentPin.dataNodeId;
      return (
        dirty &&
        !sameWorkspace &&
        !window.confirm(
          "Есть несохранённые изменения вида или предложения либо неизвестный результат сохранения. Покинуть рабочую область?",
        )
      );
    },
    enableBeforeUnload: dirty,
    withResolver: false,
  });
  const [historyOpen, setHistoryOpen] = useState(false);
  const [compareOpen, setCompareOpen] = useState(false);
  const [importsOpen, setImportsOpen] = useState(false);
  const [importsMounted, setImportsMounted] = useState(false);
  const [analysisOpen, setAnalysisOpen] = useState(false);
  const [changesOpen, setChangesOpen] = useState(false);
  const [changesMounted, setChangesMounted] = useState(false);
  const [historyCursors, setHistoryCursors] = useState([""]);
  const historyCursor = historyCursors.at(-1) ?? "";
  const historyQuery = useListBackendRevisions(
    projectId,
    historyCursor ? { cursor: historyCursor } : undefined,
    { query: { enabled: historyOpen } },
  );
  const history = historyQuery.data?.status === 200 ? historyQuery.data.data : undefined;
  const revisionQuery = useGetBackendRevision(
    projectId,
    selectedRevisionId ?? project?.currentRevisionId ?? "",
    {
      query: { enabled: !!project },
    },
  );
  const revision = revisionQuery.data?.status === 200 ? revisionQuery.data.data : undefined;
  const expectedSourcePins =
    initialSaved?.documentVersion === "saved-view-v2" && initialSaved.target.revisionId
      ? initialSaved.pins.effective
      : undefined;
  const sourceContext = useQuery({
    queryKey: [
      "backend-project-source-context",
      projectId,
      revision?.id ?? null,
      expectedSourcePins?.targetHash ?? null,
    ],
    enabled: revision?.schemaVersion === "6" && !fullTarget,
    retry: false,
    staleTime: Infinity,
    queryFn: async ({ signal }) => {
      const target = { revisionId: revision!.id };
      const coverage = await readBackendCoverage(projectId, target, signal, expectedSourcePins);
      const pins = checkBackendReadPins(coverage, target, expectedSourcePins);
      if (!pins || pins.viewSchemaVersion !== "6")
        throw new BackendReadError("Источник source6 вернул несовместимый контекст графа.");
      return pins;
    },
  });
  const sourcePins = sourceContext.data;
  const sourceReady = revision?.schemaVersion !== "6" || !!sourcePins;
  const displayedRevisionID = revision?.id;
  const annotationFocusIdentity = JSON.stringify([
    displayedRevisionID,
    pin.recordId,
    pin.recordType,
  ]);
  const onAnnotationDirty = useCallback(
    (value: boolean) => {
      setAnnotationDirty(value);
      if (value && displayedRevisionID) setSelectedRevisionId(displayedRevisionID);
    },
    [displayedRevisionID, setSelectedRevisionId],
  );
  const onChangeDirty = useCallback(
    (value: boolean) => {
      setChangeDirty(value);
      if (value && displayedRevisionID) setSelectedRevisionId(displayedRevisionID);
    },
    [displayedRevisionID, setSelectedRevisionId],
  );
  if (revision && ["3", "4", "5"].includes(revision.schemaVersion) && selectedRevisionId === null) {
    setSelectedRevisionId(revision.id);
    setPin({ ...pin, revisionId: revision.id });
  }
  useEffect(() => {
    if (
      revision &&
      ["3", "4", "5"].includes(revision.schemaVersion) &&
      !sourcePin?.revisionId &&
      pin.revisionId === revision.id
    )
      onSourceNavigate?.(pin);
  }, [revision, sourcePin?.revisionId, pin, onSourceNavigate]);
  function navigateSource(value: BackendSourcePin) {
    setPin(value);
    if (value.revisionId) setSelectedRevisionId(value.revisionId);
    onSourceNavigate?.(value);
  }
  const [editing, setEditing] = useState(false);
  const [name, setName] = useState("");
  const [baseVersion, setBaseVersion] = useState(0);
  const [attempt, setAttempt] = useState<ApplyBackendProjectCommandsRequest | null>(null);
  const mutation = useApplyBackendProjectCommands({
    mutation: {
      retry: false,
      onSuccess: (response) => {
        if (response.status !== 200) return;
        queryClient.setQueryData(getGetBackendProjectQueryKey(projectId), response);
        void queryClient.invalidateQueries({ queryKey: getListBackendProjectsQueryKey() });
        setEditing(false);
        setAttempt(null);
      },
      onError: (error) => {
        if (
          error instanceof ApiFailure &&
          error.status >= 400 &&
          error.status < 500 &&
          error.status !== 408 &&
          error.status !== 429
        )
          setAttempt(null);
      },
    },
  });
  const conflict = mutation.error instanceof ApiFailure && mutation.error.status === 409;

  function submit() {
    if (mutation.isPending || conflict || !name.trim() || !Number.isSafeInteger(baseVersion))
      return;
    const input: ApplyBackendProjectCommandsRequest = attempt ?? {
      expectedVersion: baseVersion,
      idempotencyKey: crypto.randomUUID(),
      commands: [{ type: "rename_project", name: name.trim() }],
    };
    setAttempt(input);
    mutation.mutate({ id: projectId, data: input });
  }

  async function reloadForEdit() {
    const response = await query.refetch();
    if (response.isError || response.data?.status !== 200) return;
    setBaseVersion(response.data.data.version);
    setAttempt(null);
    mutation.reset();
  }

  return (
    <BackendAPIArtifactsContext
      value={
        revision && project
          ? {
              revision,
              projectVersion: project.version,
              canEdit: revision.id === project.currentRevisionId && !initialSaved,
              onDirty: onAPIDirty,
              claim: (identity) => {
                if (apiEditorOwner.current && apiEditorOwner.current !== identity) return false;
                apiEditorOwner.current = identity;
                return true;
              },
              guard: () =>
                !Object.values(apiDirty).some(Boolean) ||
                window.confirm(
                  "Есть несохранённые связи API или неизвестный результат применения. Изменить выбор источника?",
                ),
              reread: async () => {
                const fresh = await query.refetch();
                if (fresh.isError) throw fresh.error;
                if (fresh.data?.status !== 200)
                  throw new Error("Не удалось перечитать текущий проект");
                const head = await getBackendRevision(projectId, fresh.data.data.currentRevisionId);
                if (
                  head.status !== 200 ||
                  head.data.projectId !== projectId ||
                  head.data.id !== fresh.data.data.currentRevisionId
                )
                  throw new Error("Не удалось перечитать текущую ревизию");
                return { revision: head.data, projectVersion: fresh.data.data.version };
              },
              onCurrentHead: (context, identity) => {
                const current = queryClient.getQueryData<{ status: number; data: BackendProject }>(
                  getGetBackendProjectQueryKey(projectId),
                );
                if (
                  apiEditorOwner.current !== identity ||
                  current?.status !== 200 ||
                  current.data.version !== context.projectVersion ||
                  current.data.currentRevisionId !== context.revision.id ||
                  context.revision.projectId !== projectId
                )
                  return false;
                acknowledgedNavigation.current = true;
                navigateSource({ ...pin, revisionId: context.revision.id });
                return true;
              },
              onApplied: (result) => {
                const current = queryClient.getQueryData<{ status: number; data: BackendProject }>(
                  getGetBackendProjectQueryKey(projectId),
                );
                if (
                  !current ||
                  current.status !== 200 ||
                  current.data.version <= result.project.version
                )
                  queryClient.setQueryData(getGetBackendProjectQueryKey(projectId), {
                    status: 200,
                    data: result.project,
                    headers: new Headers(),
                  });
                queryClient.setQueryData(
                  getGetBackendRevisionQueryKey(projectId, result.revision.id),
                  { status: 200, data: result.revision, headers: new Headers() },
                );
                acknowledgedNavigation.current = true;
                setAPIDirty({});
                apiEditorOwner.current = null;
                setSelectedRevisionId(result.revision.id);
                const next = { ...pin, revisionId: result.revision.id };
                setPin(next);
                onSourceNavigate?.(next);
                void queryClient.invalidateQueries({ queryKey: getListBackendProjectsQueryKey() });
              },
            }
          : null
      }
    >
      <BackendSavedViewContext value={session}>
        <Stack gap="xl" data-testid="backend-project-page" tabIndex={-1}>
          <Button
            variant="subtle"
            w="fit-content"
            leftSection={<IconArrowLeft size={16} />}
            onClick={() => void navigate({ to: "/backend-projects" })}
          >
            Бэкенд-проекты
          </Button>
          {query.isPending && <Loader aria-label="Загружаем проект" />}
          {query.isError && (
            <Alert color="red" role="alert">
              {describeApiFailureDetailed(query.error)}
              <Button mt="sm" variant="light" onClick={() => void query.refetch()}>
                Повторить загрузку
              </Button>
            </Alert>
          )}
          {project && (
            <>
              <Group justify="space-between" align="flex-start">
                <div style={{ minWidth: 0 }}>
                  <Title order={1} style={{ overflowWrap: "anywhere" }}>
                    {project.name}
                  </Title>
                  <Text c="dimmed" size="sm" mt={4}>
                    Версия проекта {project.version}
                  </Text>
                  <Button
                    variant="subtle"
                    size="sm"
                    data-testid="backend-project-refresh"
                    onClick={() => void query.refetch()}
                  >
                    Обновить состояние проекта
                  </Button>
                </div>
                {!editing && (
                  <Button
                    variant="default"
                    onClick={() => {
                      setName(project.name);
                      setBaseVersion(project.version);
                      setEditing(true);
                      mutation.reset();
                    }}
                  >
                    Переименовать
                  </Button>
                )}
              </Group>
              {editing && (
                <Paper withBorder p="md" maw={640}>
                  <form
                    onSubmit={(event) => {
                      event.preventDefault();
                      submit();
                    }}
                  >
                    <Stack>
                      <TextInput
                        label="Название проекта"
                        value={name}
                        onChange={(event) => setName(event.currentTarget.value)}
                        disabled={attempt !== null}
                        required
                        data-autofocus
                      />
                      {mutation.isError && (
                        <Alert color="red" role="alert">
                          {describeApiFailureDetailed(mutation.error)}
                        </Alert>
                      )}
                      {conflict && (
                        <Text size="sm">
                          Загрузите текущую версию, сравните название и повторите сохранение. Ваш
                          текст сохранён в форме.
                        </Text>
                      )}
                      {attempt && mutation.isError && (
                        <Text size="sm">
                          Результат запроса неизвестен. Повтор сохранит исходный запрос и защитит от
                          дублирования.
                        </Text>
                      )}
                      <Group>
                        <Button
                          type="submit"
                          loading={mutation.isPending}
                          disabled={conflict || !name.trim() || !Number.isSafeInteger(baseVersion)}
                        >
                          {attempt && mutation.isError ? "Повторить запрос" : "Сохранить название"}
                        </Button>
                        {conflict && (
                          <Button
                            variant="default"
                            loading={query.isFetching}
                            onClick={() => void reloadForEdit()}
                          >
                            Загрузить текущую версию
                          </Button>
                        )}
                        <Button
                          variant="subtle"
                          disabled={mutation.isPending || attempt !== null}
                          onClick={() => {
                            setEditing(false);
                            mutation.reset();
                          }}
                        >
                          Отмена
                        </Button>
                      </Group>
                    </Stack>
                  </form>
                </Paper>
              )}
              <BackendSavedViews
                projectId={projectId}
                session={session}
                onOpen={(value) => onSourceNavigate?.(value)}
              />
              <Group>
                <Button
                  variant="default"
                  aria-expanded={compareOpen}
                  onClick={() => setCompareOpen((value) => !value)}
                >
                  Сравнение ревизий
                </Button>
                <Button
                  variant="default"
                  aria-expanded={importsOpen}
                  onClick={() => {
                    setImportsMounted(true);
                    setImportsOpen((value) => !value);
                  }}
                >
                  Проверить импорты
                </Button>
                <Button
                  variant="default"
                  aria-expanded={changesOpen}
                  onClick={() => {
                    setChangesMounted(true);
                    setChangesOpen((value) => !value);
                  }}
                >
                  Предложения изменений
                </Button>
              </Group>
              <Button
                variant="default"
                aria-expanded={analysisOpen}
                onClick={() => setAnalysisOpen((value) => !value)}
              >
                Задания анализа
              </Button>
              {analysisOpen && revision && (
                <BackendAnalysisJobs projectId={projectId} sourceRevisionId={revision.id} />
              )}
              {compareOpen && (
                <BackendRevisionCompare
                  projectId={projectId}
                  initialRevisionId={selectedRevisionId ?? project.currentRevisionId}
                />
              )}
              {importsMounted && (
                <div hidden={!importsOpen}>
                  <BackendImportReview
                    projectId={projectId}
                    currentRevisionId={project.currentRevisionId}
                    onDirty={setSyncDirty}
                    onCandidateChange={onCandidateChange}
                    onCommitted={(revisionId) => navigateSource({ revisionId })}
                  />
                  <BackendImportCandidate projectId={projectId} target={importCandidate} />
                </div>
              )}
              {changesMounted && revision && (
                <div hidden={!changesOpen}>
                  <BackendChangeProposals
                    projectId={projectId}
                    baseRevisionId={revision.id}
                    baseSchemaVersion={String(revision.schemaVersion)}
                    readOnly={!!initialSaved}
                    onDirty={onChangeDirty}
                  />
                </div>
              )}
              {revisionQuery.isPending && <Loader aria-label="Загружаем ревизию модели" />}
              {revisionQuery.isError && (
                <Alert color="red" role="alert">
                  {describeApiFailureDetailed(revisionQuery.error)}
                  <Button mt="sm" variant="light" onClick={() => void revisionQuery.refetch()}>
                    Повторить загрузку ревизии
                  </Button>
                </Alert>
              )}
              {revision && (
                <>
                  {fullTarget ? (
                    <BackendEffectiveViews
                      projectId={projectId}
                      target={fullTarget}
                      pins={fullPins}
                      initialPin={pin}
                      savedKind={initialSaved?.state.kind}
                      onBaseNavigate={(revisionId) => {
                        if (
                          dirty &&
                          !window.confirm("Есть несохранённые изменения. Открыть базовый источник?")
                        )
                          return;
                        acknowledgedNavigation.current = true;
                        navigateSource({ revisionId });
                      }}
                    />
                  ) : (
                    <>
                      {revision.schemaVersion === "6" && (
                        <LoadState query={sourceContext} label="точного контекста источника" />
                      )}
                      {sourceReady && ["4", "5", "6"].includes(revision.schemaVersion) && (
                        <>
                          <BackendAPIArtifacts
                            projectId={projectId}
                            revisionId={revision.id}
                            pins={sourcePins}
                          />
                          <BackendArtifactProjections
                            projectId={projectId}
                            revisionId={revision.id}
                            pins={sourcePins}
                          />
                        </>
                      )}
                      {sourceReady &&
                        ["2", "3", "4", "5", "6"].includes(revision.schemaVersion) && (
                          <BackendDatabase
                            projectId={projectId}
                            revisionId={revision.id}
                            pins={sourcePins}
                            repositoryId={
                              revision.schemaVersion === "6"
                                ? undefined
                                : project.repositories[0]?.id
                            }
                            pin={pin}
                            onFlowNavigate={
                              ["3", "4", "5", "6"].includes(revision.schemaVersion)
                                ? (value) => {
                                    navigateSource(value);
                                    requestAnimationFrame(() =>
                                      document
                                        .querySelector<HTMLElement>(
                                          '[aria-label="Flow исходников"] h2',
                                        )
                                        ?.focus(),
                                    );
                                  }
                                : undefined
                            }
                            onDirty={(dirty) => {
                              setDatabaseDirty(dirty);
                              if (dirty) setSelectedRevisionId((current) => current ?? revision.id);
                            }}
                          />
                        )}
                      {sourceReady && ["5", "6"].includes(revision.schemaVersion) && (
                        <BackendEvents
                          projectId={projectId}
                          revisionId={revision.id}
                          pins={sourcePins}
                          semanticHash={revision.semanticHash}
                          onFlowNavigate={(value) => {
                            navigateSource(value);
                            requestAnimationFrame(() =>
                              document
                                .querySelector<HTMLElement>('[aria-label="Flow исходников"] h2')
                                ?.focus(),
                            );
                          }}
                        />
                      )}
                      {sourceReady && ["3", "4", "5", "6"].includes(revision.schemaVersion) && (
                        <BackendFlow
                          projectId={projectId}
                          revisionId={revision.id}
                          pins={sourcePins}
                          pin={pin}
                          onPinChange={navigateSource}
                          onDatabaseNavigate={() => {
                            requestAnimationFrame(() =>
                              document
                                .querySelector<HTMLElement>('[aria-label="База данных"]')
                                ?.scrollIntoView({ block: "start" }),
                            );
                          }}
                        />
                      )}
                      {revision.sourceSnapshotIds.length > 0 ? (
                        sourceReady && (
                          <BackendGraphInventory
                            key={`${projectId}:${revision.id}`}
                            projectId={projectId}
                            revisionId={revision.id}
                            schemaVersion={revision.schemaVersion}
                            pins={sourcePins}
                            focusTarget={
                              pin.recordId && pin.recordType
                                ? { recordType: pin.recordType, id: pin.recordId }
                                : undefined
                            }
                            onSelectionChange={(value) => {
                              setAnnotationSelection(value);
                              setAnnotationSelectionPin(annotationFocusIdentity);
                            }}
                          />
                        )
                      ) : (
                        <Paper withBorder p="lg">
                          <Stack gap="sm">
                            <Group justify="space-between">
                              <Title order={2}>Модель бэкенда</Title>
                              <Badge color="yellow">Покрытие неизвестно</Badge>
                            </Group>
                            <Text>
                              Исходный код ещё не импортирован. Список таблиц, связей и endpoint’ов
                              пока не исследован.
                            </Text>
                            <Text c="dimmed" size="sm">
                              Импортируйте первый граф исходников через агента, выбрав совместимые
                              инструкции импорта. Покрытие и основания появятся в новой ревизии.
                            </Text>
                            <Text size="sm">
                              Объектов в модели: {revision.coverage.knownObjects}. Общее количество
                              неизвестно.
                            </Text>
                          </Stack>
                        </Paper>
                      )}
                      <BackendAnnotations
                        projectId={projectId}
                        selection={
                          annotationSelectionPin === annotationFocusIdentity
                            ? annotationSelection
                            : pin.recordId && pin.recordType
                              ? {
                                  recordType: pin.recordType,
                                  id: pin.recordId,
                                  revisionId: revision.id,
                                }
                              : undefined
                        }
                        onDirtyChange={onAnnotationDirty}
                        onNavigate={(target) => {
                          if (
                            dirty &&
                            !window.confirm("Есть несохранённые изменения. Открыть цель заметки?")
                          )
                            return;
                          navigateSource({
                            revisionId: target.revisionId ?? project.currentRevisionId,
                            recordType: target.recordType,
                            recordId: target.id,
                          });
                          requestAnimationFrame(() =>
                            document
                              .querySelector<HTMLElement>('[aria-label="Инспектор объекта"]')
                              ?.scrollIntoView({ block: "start" }),
                          );
                        }}
                      />
                      <Stack gap="sm">
                        <Group justify="space-between">
                          <Title order={2}>
                            {revision.id === project.currentRevisionId
                              ? "Текущая ревизия"
                              : "Ревизия модели"}
                          </Title>
                          <Button
                            variant="default"
                            aria-expanded={historyOpen}
                            onClick={() => setHistoryOpen((open) => !open)}
                          >
                            История ревизий
                          </Button>
                        </Group>
                        {historyOpen && (
                          <Stack gap="xs" aria-label="История ревизий">
                            {historyQuery.isPending && <Loader aria-label="Загружаем историю" />}
                            {historyQuery.isError && (
                              <Alert color="red" role="alert">
                                {describeApiFailureDetailed(historyQuery.error)}
                                <Button variant="light" onClick={() => void historyQuery.refetch()}>
                                  Повторить загрузку истории
                                </Button>
                              </Alert>
                            )}
                            {history?.items.map((item) => (
                              <Button
                                key={item.id}
                                variant={item.id === revision.id ? "light" : "subtle"}
                                w="fit-content"
                                maw="100%"
                                onClick={() => {
                                  if (
                                    !dirty ||
                                    window.confirm(
                                      "В предложении есть несохранённые изменения. Открыть другую ревизию источника?",
                                    )
                                  ) {
                                    setDatabaseDirty(false);
                                    setSelectedRevisionId(item.id);
                                    navigateSource({ revisionId: item.id });
                                  }
                                }}
                                aria-label={`Открыть ревизию ${item.id}`}
                              >
                                {new Date(item.createdAt).toLocaleString("ru-RU")}
                                {item.id === project.currentRevisionId ? " · текущая" : ""}
                              </Button>
                            ))}
                            {(historyCursors.length > 1 || history?.nextCursor) && (
                              <Group>
                                <Button
                                  variant="default"
                                  disabled={historyCursors.length === 1 || historyQuery.isFetching}
                                  onClick={() =>
                                    setHistoryCursors((previous) => previous.slice(0, -1))
                                  }
                                >
                                  Предыдущие ревизии
                                </Button>
                                <Button
                                  variant="default"
                                  disabled={!history?.nextCursor || historyQuery.isFetching}
                                  onClick={() => {
                                    if (history?.nextCursor)
                                      setHistoryCursors((previous) => [
                                        ...previous,
                                        history.nextCursor,
                                      ]);
                                  }}
                                >
                                  Следующие ревизии
                                </Button>
                              </Group>
                            )}
                          </Stack>
                        )}
                        <Text size="sm" c="dimmed">
                          Создана {new Date(revision.createdAt).toLocaleString("ru-RU")}. Название
                          проекта хранится отдельно от модели.
                        </Text>
                        <Text size="sm">ID ревизии</Text>
                        <Code block style={{ overflowWrap: "anywhere", whiteSpace: "pre-wrap" }}>
                          {revision.id}
                        </Code>
                        <Text size="sm">Хеш содержимого</Text>
                        <Code block style={{ overflowWrap: "anywhere", whiteSpace: "pre-wrap" }}>
                          {revision.semanticHash}
                        </Code>
                      </Stack>
                    </>
                  )}
                </>
              )}
            </>
          )}
        </Stack>
      </BackendSavedViewContext>
    </BackendAPIArtifactsContext>
  );
}
