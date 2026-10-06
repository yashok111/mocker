import { BackendAnalysisJobs } from "./BackendAnalysisJobs";
import { useDiagramChangeHandoff } from "./BackendDiagramChangeContext";
import { businessMapArchitectureSearch } from "./backendBusinessMapNavigation";
import { lazy, Suspense, useCallback, useEffect, useLayoutEffect, useState, useRef } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Alert,
  Badge,
  Button,
  Code,
  Group,
  Loader,
  NativeSelect,
  Paper,
  SimpleGrid,
  Stack,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import {
  buildBackendInteractions,
  createBackendDiagram,
  saveBackendDiagram,
  forkBackendDiagram,
  getBackendDiagramView,
  createBackendDiagramView,
  saveBackendDiagramView,
  listBackendDiagrams,
  listBackendDiagramViews,
  listBackendSavedViews,
  compareBackendDiagrams,
  useGetBackendProject,
} from "@/api/generated/backend-projects/backend-projects";
import type {
  BackendDiagramDocument,
  BackendDiagramPin,
  BackendDiagramVersion,
  BackendDiagramView,
  BackendDiagramViewState,
  BackendDiagramRef,
  BackendDiagramTarget,
  BackendDiagramComparison,
} from "@/api/generated/schemas";
import { ApiFailure } from "@/api/client";
import { describeApiFailureDetailed } from "@/api/errors";
import { usePinnedValue, type BackendSourcePin } from "./backendFlowReads";
import {
  diagramSearch,
  reconcileDiagramPresentation,
  explicitDiagramPin,
  workspacePinError,
  type BackendWorkspaceSearch,
} from "./backendWorkspaceSearch";
import { readDiagram, diagramReadKey, sameDiagramPin, useDiagramPage } from "./backendDiagramReads";
import { useDatabaseCancellation } from "./backendDatabaseReads";
const BackendArchitectureGraph = lazy(() =>
  import("./BackendArchitectureGraph").then((module) => ({
    default: module.BackendArchitectureGraph,
  })),
);
import { BackendArchitectureInspector } from "./BackendArchitectureInspector";
import { BackendArtifactProjections } from "./BackendArtifactProjections";
import { BackendInteractions, BackendInteractionsEditor } from "./BackendInteractions";
import { BackendLifecycle, BackendLifecycleEditor } from "./BackendLifecycle";
import { BackendBusinessMap, BackendBusinessMapEditor } from "./BackendBusinessMap";
import { BackendLifecycleBuilder } from "./BackendLifecycleBuilder";
import { BackendArchitectureEditor } from "./BackendArchitectureEditor";
import { focusWorkspaceRegion } from "./BackendWorkspaceNavigation";

type Props = {
  projectId: string;
  search: BackendWorkspaceSearch;
  onNavigate: (pin: BackendWorkspaceSearch, replace?: boolean) => void;
  onDetailedNavigate: (pin: BackendSourcePin) => void;
  onDirty?: (dirty: boolean) => void;
  onTargetResolved?: (target: BackendDiagramTarget) => void;
};
type Pending = {
  operation: "create" | "save" | "fork" | "view-create" | "view-save";
  id?: string;
  body: unknown;
};
function initialState(
  d: BackendDiagramVersion,
  search: BackendWorkspaceSearch,
): BackendDiagramViewState {
  if (!search.diagramId && !search.diagramViewId) {
    try {
      const remembered = JSON.parse(
        sessionStorage.getItem(`mocker:c4:${d.projectId}:${d.targetHash}`) ?? "null",
      ) as BackendDiagramViewState | null;
      if (
        remembered &&
        sameDiagramPin(remembered.diagram, d.pin) &&
        Array.isArray(remembered.positions) &&
        Array.isArray(remembered.collapsedIds)
      )
        return remembered;
    } catch {
      /* Ignore unavailable or invalid optional remembered state. */
    }
  }
  const [type, id] = (search.diagramSelection ?? "").split(":");
  return {
    diagram: d.pin,
    ...(d.document.kind === "architecture"
      ? {
          level: search.diagramLevel ?? "context",
          rootId: search.diagramRoot ?? d.document.payload.primarySystemId,
        }
      : {}),
    search: "",
    origin: "all",
    selection: (type === "element" || type === "link") && id ? { type, id } : null,
    positions: [],
    collapsedIds: [],
  };
}
export function BackendArchitecture(props: Props) {
  const [opened, setOpened] = useState(false);
  const legacy = !!(
    props.search.viewId ||
    props.search.revisionId ||
    props.search.changeProposalId ||
    props.search.proposalRevisionId
  );
  const diagram = !!(props.search.diagramId || props.search.diagramViewId);
  if (legacy && !diagram && !opened)
    return (
      <Paper withBorder p="md" data-testid="backend-architecture">
        <Group justify="space-between">
          <Title order={2} tabIndex={-1}>
            Архитектура C4
          </Title>
          <Button variant="default" onClick={() => setOpened(true)}>
            Открыть архитектурные mappings
          </Button>
        </Group>
      </Paper>
    );
  return <ArchitectureContent {...props} />;
}
function ArchitectureContent(props: Props) {
  const queryClient = useQueryClient();
  const { projectId, search, onNavigate, onTargetResolved, onDirty: parentOnDirty } = props;
  const projectQuery = useGetBackendProject(projectId);
  const project = projectQuery.data?.status === 200 ? projectQuery.data.data : undefined;
  const [catalogCursors, setCatalogCursors] = useState([""]);
  const cursor = catalogCursors.at(-1) ?? "";
  const catalog = useQuery({
    queryKey: ["backend-diagram-catalog", projectId, cursor],
    retry: false,
    queryFn: async ({ signal }) => {
      const r = await listBackendDiagrams(
        projectId,
        { limit: 100, ...(cursor ? { cursor } : {}) },
        { signal },
      );
      signal.throwIfAborted();
      if (r.status !== 200) throw new Error("Список mappings недоступен");
      return r.data;
    },
  });
  const error = workspacePinError(search);
  const viewQuery = useQuery({
    queryKey: ["backend-diagram-view", projectId, search.diagramViewId, search.diagramViewVersion],
    enabled: !!search.diagramViewId && !error,
    retry: false,
    staleTime: Infinity,
    queryFn: async ({ signal }) => {
      const r = await getBackendDiagramView(
        projectId,
        search.diagramViewId!,
        search.diagramViewVersion!,
        { signal },
      );
      signal.throwIfAborted();
      if (
        r.status !== 200 ||
        r.data.id !== search.diagramViewId ||
        r.data.version !== search.diagramViewVersion
      )
        throw new Error("Точный сохранённый C4 вид недоступен");
      return r.data;
    },
  });
  const legacyExplicit = !!(search.viewId || search.revisionId || search.changeProposalId);
  const chosen =
    explicitDiagramPin(search) ??
    viewQuery.data?.state.diagram ??
    (!search.diagramViewId &&
    !legacyExplicit &&
    catalog.data?.items.length === 1 &&
    !catalog.data.nextCursor &&
    catalogCursors.length === 1
      ? catalog.data.items[0]?.pin
      : undefined);
  const key = diagramReadKey(projectId, chosen);
  useDatabaseCancellation(key);
  const diagramQuery = useQuery({
    queryKey: key,
    enabled: !!chosen && !error,
    retry: false,
    staleTime: Infinity,
    queryFn: ({ signal }) => readDiagram(projectId, chosen!, signal),
  });
  const [creating, setCreating] = useState(false);
  const [entrypoint, setEntrypoint] = useState("");
  const [building, setBuilding] = useState(false);
  const buildAbort = useRef<AbortController | null>(null);
  const [buildError, setBuildError] = useState("");
  const [contentDirty, setContentDirty] = useState(false);
  const notifyDirty = useCallback(
    (dirty: boolean) => {
      setContentDirty(dirty);
      parentOnDirty?.(dirty);
    },
    [parentOnDirty],
  );
  const [newDoc, setNewDoc] = useState<BackendDiagramDocument>();
  const sourceTarget: BackendDiagramTarget | undefined =
    search.changeProposalId && search.proposalRevisionId
      ? {
          changeProposal: {
            proposalId: search.changeProposalId,
            proposalRevisionId: search.proposalRevisionId,
          },
        }
      : search.revisionId || project?.currentRevisionId
        ? { revisionId: search.revisionId ?? project!.currentRevisionId }
        : undefined;
  const target = chosen || search.diagramViewId ? diagramQuery.data?.document.target : sourceTarget;
  const create = () => {
    if (!target) return;
    const id = crypto.randomUUID();
    setNewDoc({
      format: "backend-diagram-v1",
      kind: "architecture",
      target,
      payload: {
        primarySystemId: id,
        elements: [
          {
            id,
            label: "Новая система",
            role: "software_system",
            responsibility: "",
            technology: "",
            origin: { kind: "authored", reason: "Явно заданная пользователем граница системы" },
            refs: [],
          },
        ],
        links: [],
      },
    });
    setCreating(true);
  };
  const buildScope = JSON.stringify([projectId, target, chosen]);
  useEffect(
    () => () => {
      buildAbort.current?.abort();
    },
    [buildScope],
  );
  const buildInteractions = async () => {
    if (!target || building || contentDirty) return;
    setBuilding(true);
    setBuildError("");
    const controller = new AbortController();
    buildAbort.current = controller;
    try {
      const response = await buildBackendInteractions(
        projectId,
        {
          target,
          entrypointId: entrypoint,
          maxSteps: 200,
          ...(diagramQuery.data?.document.kind === "architecture"
            ? { architecture: diagramQuery.data.pin }
            : {}),
        },
        { signal: controller.signal },
      );
      controller.signal.throwIfAborted();
      if (response.status !== 200) throw new Error("Кандидат interactions недоступен");
      setNewDoc(response.data.document);
      setCreating(true);
    } catch (error) {
      if (!controller.signal.aborted) setBuildError(describeApiFailureDetailed(error));
    } finally {
      if (buildAbort.current === controller) setBuilding(false);
    }
  };
  const selected = diagramQuery.data;
  useEffect(() => {
    if (!selected) return;
    onTargetResolved?.(selected.document.target);
    if (!search.diagramId && !search.diagramViewId && !legacyExplicit)
      onNavigate(diagramSearch(selected.pin), true);
  }, [
    selected,
    onTargetResolved,
    search.diagramId,
    search.diagramViewId,
    legacyExplicit,
    onNavigate,
  ]);
  return (
    <Paper withBorder p="md" data-testid="backend-architecture" style={{ minWidth: 0 }}>
      <Stack>
        <Group justify="space-between">
          <Title order={2} tabIndex={-1}>
            Архитектура C4
          </Title>
          <Badge variant="light">Context → Containers → Components</Badge>
        </Group>
        <Text size="sm" c="dimmed">
          Явные границы, направленные зависимости и их основания. Исходные утверждения и авторский
          замысел показаны раздельно.
        </Text>
        <Group align="end" grow>
          <NativeSelect
            label="Архитектурный mapping"
            value={chosen ? JSON.stringify(chosen) : ""}
            disabled={contentDirty}
            onChange={(e) => {
              if (e.currentTarget.value) {
                setCreating(false);
                onNavigate(diagramSearch(JSON.parse(e.currentTarget.value)));
              }
            }}
            data={[
              { value: "", label: "Выберите mapping" },
              ...(catalog.data?.items ?? []).map((item) => ({
                value: JSON.stringify(item.pin),
                label: `${item.kind} · ${item.id} · v${item.pin.version}`,
              })),
            ]}
          />
          <Button
            variant="default"
            disabled={!!error || !target || creating || contentDirty}
            onClick={create}
          >
            Создать mapping
          </Button>
        </Group>
        <Group align="end">
          <TextInput
            label="Entrypoint ID для interactions"
            value={entrypoint}
            onChange={(e) => setEntrypoint(e.currentTarget.value)}
            disabled={contentDirty || building}
          />
          <Button
            variant="default"
            disabled={!target || !entrypoint || contentDirty || building || creating}
            loading={building}
            onClick={() => void buildInteractions()}
          >
            Предложить interactions
          </Button>
          <Button
            variant="default"
            disabled={!target || contentDirty || building || creating}
            onClick={() => {
              if (target) {
                setNewDoc({
                  format: "backend-diagram-v1",
                  kind: "interactions",
                  target,
                  payload: {
                    ...(diagramQuery.data?.document.kind === "architecture"
                      ? { architecture: diagramQuery.data.pin }
                      : {}),
                    scopeRefs: [],
                    participants: [],
                    steps: [],
                    branches: [],
                    order: [],
                  },
                });
                setCreating(true);
              }
            }}
          >
            Создать interactions
          </Button>
        </Group>
        <Button
          variant="default"
          disabled={!target || contentDirty || creating}
          onClick={() => {
            if (target) {
              setNewDoc({
                format: "backend-diagram-v1",
                kind: "business_map",
                target,
                payload: {
                  elements: [],
                  links: [],
                  ...(diagramQuery.data?.document.kind === "architecture"
                    ? { architecture: diagramQuery.data.pin }
                    : {}),
                },
              });
              setCreating(true);
            }
          }}
        >
          Создать business map
        </Button>
        <BackendLifecycleBuilder
          key={buildScope}
          projectId={projectId}
          target={target}
          disabled={contentDirty || building || creating}
          onCandidate={(d) => {
            setNewDoc(d);
            setCreating(true);
          }}
        />
        {buildError && (
          <Alert color="red" role="alert">
            {buildError}
          </Alert>
        )}
        {catalog.isError && (
          <Alert color="red" role="alert">
            Список mappings недоступен.{" "}
            <Button
              onClick={() => {
                setCatalogCursors([""]);
                void catalog.refetch();
              }}
            >
              Обновить список mappings
            </Button>
          </Alert>
        )}
        {(catalogCursors.length > 1 || catalog.data?.nextCursor) && (
          <Group>
            <Button
              disabled={catalogCursors.length === 1}
              onClick={() => setCatalogCursors((c) => c.slice(0, -1))}
            >
              Предыдущие mappings
            </Button>
            <Button
              disabled={!catalog.data?.nextCursor}
              onClick={() => setCatalogCursors((c) => [...c, catalog.data!.nextCursor])}
            >
              Следующие mappings
            </Button>
          </Group>
        )}
        {(error || diagramQuery.isError || viewQuery.isError) && (
          <Alert color="red" role="alert">
            {error ??
              "Точный mapping или сохранённый вид недоступен. Выберите существующий mapping или вернитесь к Context."}
            <Button variant="default" onClick={() => onNavigate({})}>
              Вернуться к Context
            </Button>
          </Alert>
        )}
        {!error && !chosen && !creating && catalog.data && (
          <Alert color="yellow">
            {catalog.data.items.length === 0
              ? "Архитектурный mapping ещё не создан"
              : "Выберите mapping: автоматический выбор при нескольких mappings запрещён"}
            . Существующий инвентарь доступен ниже.
          </Alert>
        )}
        {chosen && diagramQuery.isPending && !error && (
          <Loader aria-label="Загружаем точный C4 mapping" />
        )}
        {((selected && !error) || (creating && newDoc)) && (
          <ArchitectureWorkspace
            key={creating ? `create:${newDoc?.kind}` : `${projectId}:${selected!.pin.id}`}
            {...props}
            onDirty={notifyDirty}
            diagram={creating ? undefined : selected}
            initialDocument={creating ? newDoc : undefined}
            savedView={viewQuery.data}
            onCreated={() => {
              setCreating(false);
              void catalog.refetch();
              void queryClient.invalidateQueries({ queryKey: ["c4-saved-list", projectId] });
            }}
          />
        )}
        <ArchitectureSavedLists
          projectId={projectId}
          onNavigate={onNavigate}
          locked={contentDirty}
        />
      </Stack>
    </Paper>
  );
}
function ArchitectureWorkspace({
  projectId,
  search,
  onNavigate,
  onDetailedNavigate,
  onDirty,
  diagram,
  initialDocument,
  savedView,
  onCreated,
}: Props & {
  diagram?: BackendDiagramVersion;
  initialDocument?: BackendDiagramDocument;
  savedView?: BackendDiagramView;
  onCreated: () => void;
}) {
  const architecture =
    diagram?.document.kind === "architecture" ? diagram.document.payload : undefined;
  const [draft, setDraft] = useState<BackendDiagramDocument | undefined>(initialDocument);
  const fallback = diagram ? initialState(diagram, search) : undefined;
  const semanticIdentity = JSON.stringify(diagram?.pin);
  const routeIdentity = JSON.stringify([
    semanticIdentity,
    savedView?.id,
    savedView?.version,
    search.diagramLevel,
    search.diagramRoot,
    search.diagramSelection,
  ]);
  const [storedState, setState] = usePinnedValue<BackendDiagramViewState | undefined>(
    semanticIdentity,
    savedView?.state ?? fallback,
  );
  const [appliedRoute, setAppliedRoute] = useState(routeIdentity);
  let state = storedState;
  if (appliedRoute !== routeIdentity) {
    state =
      savedView?.state ??
      (fallback ? reconcileDiagramPresentation(storedState, fallback) : undefined);
    setAppliedRoute(routeIdentity);
    setState(state);
  }
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const pendingKey = `mocker:diagram-pending:${projectId}`;
  const [pending, setPending] = useState<Pending | undefined>(() => {
    try {
      return JSON.parse(sessionStorage.getItem(pendingKey) ?? "null") ?? undefined;
    } catch {
      return undefined;
    }
  });
  const [artifactRef, setArtifactRef] =
    useState<Extract<BackendDiagramRef, { kind: "artifact" }>>();
  const [layoutNode, setLayoutNode] = useState("");
  const [layoutX, setLayoutX] = useState("0");
  const [layoutY, setLayoutY] = useState("0");
  const [viewOwner, setViewOwner] = useState(savedView);
  if (savedView && (savedView.id !== viewOwner?.id || savedView.version !== viewOwner.version))
    setViewOwner(savedView);
  const selectedView = savedView ?? viewOwner;
  const activeView =
    selectedView && diagram && sameDiagramPin(selectedView.state.diagram, diagram.pin)
      ? selectedView
      : undefined;
  const [viewName, setViewName] = usePinnedValue<string>(
    activeView ? `${activeView.id}:${activeView.version}` : `new:${semanticIdentity}`,
    activeView?.name ??
      (architecture
        ? "C4 architecture"
        : diagram?.document.kind === "business_map"
          ? "Business map"
          : diagram?.document.kind === "lifecycle"
            ? "Lifecycle"
            : "Static interactions"),
  );
  const viewDirty =
    !!activeView &&
    !!state &&
    (activeView.name !== viewName || JSON.stringify(activeView.state) !== JSON.stringify(state));
  const [comparePin, setComparePin] = useState("");
  const [comparison, setComparison] = useState<BackendDiagramComparison>();
  const [forkTarget, setForkTarget] = useState("");
  const [forkReason, setForkReason] = useState("");
  const [forkArchitecture, setForkArchitecture] = useState("");
  const dirty = !!draft || !!pending || busy;
  const changeHandoff = useDiagramChangeHandoff();
  const c4Read = useRef<AbortController | null>(null);
  const c4Scope = useRef("");
  useLayoutEffect(() => {
    c4Scope.current = JSON.stringify([routeIdentity, dirty]);
    return () => {
      c4Read.current?.abort();
    };
  }, [routeIdentity, dirty]);

  useEffect(() => {
    onDirty?.(dirty || viewDirty);
    return () => onDirty?.(false);
  }, [dirty, viewDirty, onDirty]);
  useEffect(() => {
    if (!diagram || !state) return;
    try {
      sessionStorage.setItem(`mocker:c4:${projectId}:${diagram.targetHash}`, JSON.stringify(state));
    } catch {
      /* Remembered presentation is optional. */
    }
  }, [diagram, state, projectId]);
  const readInput = state
    ? {
        pin: state.diagram,
        level: state.level,
        rootId: state.rootId,
        search: state.search,
        origin: state.origin,
        limit: 100,
      }
    : {
        pin: { id: "", version: 1, contentHash: "" },
        level: "context" as const,
        rootId: "",
        search: "",
        origin: "all" as const,
        limit: 100,
      };
  const [ec, setEC] = usePinnedValue<string[]>(JSON.stringify(readInput), [""]);
  const [lc, setLC] = usePinnedValue<string[]>(JSON.stringify(readInput), [""]);
  const elements = useDiagramPage(
    projectId,
    { ...readInput, section: "elements", ...(ec.at(-1) ? { cursor: ec.at(-1) } : {}) },
    !!diagram && !!state,
  );
  const links = useDiagramPage(
    projectId,
    { ...readInput, section: "links", ...(lc.at(-1) ? { cursor: lc.at(-1) } : {}) },
    !!diagram && !!state,
  );
  const changeState = (next: BackendDiagramViewState) => {
    if (dirty) return;
    setState(next);
    onNavigate(diagramSearch(next.diagram, next));
  };
  const attempt = async (request: Pending) => {
    if (busy) return;
    setBusy(true);
    setMessage("");
    setPending(request);
    try {
      sessionStorage.setItem(pendingKey, JSON.stringify(request));
    } catch {
      setBusy(false);
      setMessage("Не удалось сохранить запрос для восстановления. Освободите session storage.");
      return;
    }
    try {
      let result: BackendDiagramVersion | BackendDiagramView;
      switch (request.operation) {
        case "create": {
          const r = await createBackendDiagram(
            projectId,
            request.body as Parameters<typeof createBackendDiagram>[1],
          );
          if (r.status !== 200) throw new Error("Mapping не сохранён");
          result = r.data;
          break;
        }
        case "save": {
          const r = await saveBackendDiagram(
            projectId,
            request.id!,
            request.body as Parameters<typeof saveBackendDiagram>[2],
          );
          if (r.status !== 200) throw new Error("Mapping не сохранён");
          result = r.data;
          break;
        }
        case "fork": {
          const r = await forkBackendDiagram(
            projectId,
            request.body as Parameters<typeof forkBackendDiagram>[1],
          );
          if (r.status !== 200) throw new Error("Fork не сохранён");
          result = r.data;
          break;
        }
        case "view-create": {
          const r = await createBackendDiagramView(
            projectId,
            request.body as Parameters<typeof createBackendDiagramView>[1],
          );
          if (r.status !== 200) throw new Error("Вид не сохранён");
          result = r.data;
          break;
        }
        case "view-save": {
          const r = await saveBackendDiagramView(
            projectId,
            request.id!,
            request.body as Parameters<typeof saveBackendDiagramView>[2],
          );
          if (r.status !== 200) throw new Error("Вид не сохранён");
          result = r.data;
          break;
        }
      }
      sessionStorage.removeItem(pendingKey);
      setPending(undefined);
      setDraft(undefined);
      setMessage("Сохранено");
      onDirty?.(false);
      onCreated();
      if ("pin" in result)
        onNavigate(
          diagramSearch(result.pin, {
            ...(result.document.kind === "architecture"
              ? { level: "context" as const, rootId: result.document.payload.primarySystemId }
              : {}),
            selection: null,
          }),
        );
      else onNavigate({ diagramViewId: result.id, diagramViewVersion: result.version });
    } catch (error) {
      setMessage(describeApiFailureDetailed(error));
      if (error instanceof ApiFailure && error.status >= 400 && error.status < 500) {
        sessionStorage.removeItem(pendingKey);
        setPending(undefined);
        setMessage(
          `${describeApiFailureDetailed(error)} Локальные изменения сохранены. Перечитайте mapping или создайте новый.`,
        );
      }
    } finally {
      setBusy(false);
    }
  };
  const select = (selection: NonNullable<BackendDiagramViewState["selection"]>) => {
    if (state) {
      changeState({ ...state, selection });
      focusWorkspaceRegion(
        diagram?.document.kind === "business_map"
          ? "#business-map-inspector-title"
          : diagram?.document.kind === "lifecycle"
            ? "#lifecycle-inspector-title"
            : diagram?.document.kind === "interactions"
              ? "#interactions-inspector-title"
              : "#c4-inspector-title",
      );
    }
  };
  const open = (ref: BackendDiagramRef) => {
    if (!diagram) return;
    if (ref.kind === "artifact") {
      setArtifactRef(ref);
      return;
    }
    const target = diagram.document.target;
    const pin: BackendSourcePin =
      "revisionId" in target
        ? { revisionId: target.revisionId }
        : {
            changeProposalId: target.changeProposal.proposalId,
            proposalRevisionId: target.changeProposal.proposalRevisionId,
          };
    onDetailedNavigate({ ...pin, recordId: ref.id, recordType: ref.recordType });
    focusWorkspaceRegion('[data-testid="backend-project-page"]');
  };
  const es =
    elements.data?.items.flatMap((row) =>
      row.rowType === "architecture_element" ? [row.data] : [],
    ) ?? [];
  const layoutElements =
    diagram?.document.kind === "business_map"
      ? diagram.document.payload.elements
      : diagram?.document.kind === "lifecycle"
        ? diagram.document.payload.states
        : diagram?.document.kind === "interactions"
          ? diagram.document.payload.participants
          : es;
  const ls =
    links.data?.items.flatMap((row) => (row.rowType === "architecture_link" ? [row.data] : [])) ??
    [];
  return (
    <Stack>
      {message && (
        <Alert role="alert" color={pending ? "yellow" : "blue"}>
          {message}
        </Alert>
      )}
      {pending && (
        <Alert color="yellow">
          Результат запроса может быть неизвестен. Смена mapping не меняет сохранённый запрос.
          <Button disabled={busy} onClick={() => void attempt(pending)}>
            Повторить точный запрос
          </Button>
        </Alert>
      )}
      {draft && (
        <DiagramEditor
          document={draft}
          onChange={setDraft}
          busy={busy || !!pending}
          onCancel={() => {
            setDraft(undefined);
            if (!diagram) onCreated();
          }}
          onSave={() =>
            void attempt({
              operation: diagram ? "save" : "create",
              id: diagram?.pin.id,
              body: {
                document: draft,
                ...(diagram ? { expectedVersion: diagram.pin.version } : {}),
                idempotencyKey: crypto.randomUUID(),
              },
            })
          }
        />
      )}
      {diagram && state && (
        <>
          <Group>
            <Badge>Mapping v{diagram.pin.version}</Badge>
            <Text size="sm">
              {diagram.author} · {diagram.createdAt}
            </Text>
            <Button
              variant="default"
              disabled={dirty}
              onClick={() => setDraft(structuredClone(diagram.document))}
            >
              Редактировать mapping
            </Button>
          </Group>
          <Code style={{ whiteSpace: "normal", overflowWrap: "anywhere" }}>
            {JSON.stringify(diagram.document.target)}
          </Code>
          {architecture && (
            <Group grow align="end">
              <NativeSelect
                label="Уровень C4"
                value={state.level}
                disabled={dirty}
                onChange={(e) => {
                  const level = e.currentTarget.value as BackendDiagramViewState["level"];
                  const root =
                    level === "components"
                      ? architecture!.elements.find((n) => n.role === "application")?.id
                      : architecture!.primarySystemId;
                  if (root)
                    changeState({
                      ...state,
                      level,
                      rootId: root,
                      selection: null,
                      positions: [],
                      collapsedIds: [],
                    });
                  else setMessage("Для Components сначала добавьте application mapping.");
                }}
                data={["context", "containers", "components"]}
              />
              <NativeSelect
                label="Корень C4"
                value={state.rootId}
                disabled={dirty}
                onChange={(e) =>
                  changeState({
                    ...state,
                    rootId: e.currentTarget.value,
                    selection: null,
                    positions: [],
                    collapsedIds: [],
                  })
                }
                data={architecture!.elements
                  .filter(
                    (n) =>
                      n.role === (state.level === "components" ? "application" : "software_system"),
                  )
                  .map((n) => ({ value: n.id, label: n.label }))}
              />
              <Button
                variant="subtle"
                disabled={dirty}
                onClick={() =>
                  changeState({
                    ...state,
                    level: "context",
                    rootId: architecture!.primarySystemId,
                    selection: null,
                    positions: [],
                    collapsedIds: [],
                  })
                }
              >
                Context /{" "}
                {architecture!.elements.find((n) => n.id === architecture!.primarySystemId)?.label}
              </Button>
            </Group>
          )}
          <Group grow>
            <TextInput
              label="Поиск по всей проекции"
              value={state.search}
              disabled={dirty}
              onChange={(e) => setState({ ...state, search: e.currentTarget.value })}
            />
            <NativeSelect
              label="Происхождение"
              value={state.origin}
              disabled={dirty}
              data={[
                { value: "all", label: "Все" },
                { value: "source_assertion", label: "Исходные утверждения" },
                { value: "authored", label: "Авторский замысел" },
              ]}
              onChange={(e) =>
                setState({
                  ...state,
                  origin: e.currentTarget.value as BackendDiagramViewState["origin"],
                })
              }
            />
          </Group>
          {(elements.isError || links.isError) && (
            <Alert color="red" role="alert">
              Точная C4 проекция недоступна.
              <Button
                onClick={() => {
                  void elements.refetch();
                  void links.refetch();
                }}
              >
                Повторить C4
              </Button>
            </Alert>
          )}
          {architecture && (
            <SimpleGrid cols={{ base: 1, lg: 2 }}>
              <Stack>
                <Title order={3}>Элементы · {elements.data?.total ?? 0}</Title>
                {es.map((e) => (
                  <Group key={e.id} wrap="wrap">
                    <Button
                      variant="default"
                      h="auto"
                      style={{ whiteSpace: "normal", overflowWrap: "anywhere" }}
                      disabled={dirty}
                      data-c4-selection={e.id}
                      onClick={() => select({ type: "element", id: e.id })}
                    >
                      {e.label} · {e.role} · {e.origin.kind === "authored" ? "Замысел" : "Source"}
                    </Button>
                    {["software_system", "application"].includes(e.role) && (
                      <Button
                        variant="subtle"
                        disabled={dirty}
                        onClick={() =>
                          changeState({
                            ...state,
                            level: e.role === "application" ? "components" : "containers",
                            rootId: e.id,
                            selection: null,
                            positions: [],
                            collapsedIds: [],
                          })
                        }
                      >
                        Что внутри {e.label}
                      </Button>
                    )}
                  </Group>
                ))}
                <Group>
                  <Button disabled={ec.length === 1} onClick={() => setEC((c) => c.slice(0, -1))}>
                    Предыдущие элементы
                  </Button>
                  <Button
                    disabled={!elements.data?.nextCursor}
                    onClick={() => setEC((c) => [...c, elements.data!.nextCursor])}
                  >
                    Следующие элементы
                  </Button>
                </Group>
              </Stack>
              <Stack>
                <Title order={3}>Зависимости · {links.data?.total ?? 0}</Title>
                {ls.map((l) => (
                  <Button
                    key={l.id}
                    variant="default"
                    h="auto"
                    style={{ whiteSpace: "normal", overflowWrap: "anywhere" }}
                    disabled={dirty}
                    data-c4-selection={l.id}
                    onClick={() => select({ type: "link", id: l.id })}
                  >
                    {es.find((e) => e.id === l.from)?.label ?? l.from} →{" "}
                    {es.find((e) => e.id === l.to)?.label ?? l.to} · {l.relation} ·{" "}
                    {l.origin.kind === "authored" ? "Замысел" : "Source"}
                  </Button>
                ))}
                <Group>
                  <Button disabled={lc.length === 1} onClick={() => setLC((c) => c.slice(0, -1))}>
                    Предыдущие связи
                  </Button>
                  <Button
                    disabled={!links.data?.nextCursor}
                    onClick={() => setLC((c) => [...c, links.data!.nextCursor])}
                  >
                    Следующие связи
                  </Button>
                </Group>
              </Stack>
            </SimpleGrid>
          )}
          <Paper withBorder p="md">
            <Stack gap="xs">
              <Text fw={600}>Расположение и свёрнутые элементы</Text>
              <Group grow align="end">
                <NativeSelect
                  label="Элемент для расположения"
                  value={layoutNode}
                  onChange={(e) => setLayoutNode(e.currentTarget.value)}
                  data={[
                    { value: "", label: "Выберите элемент" },
                    ...layoutElements.map((e) => ({ value: e.id, label: e.label })),
                  ]}
                />
                <TextInput
                  label={
                    diagram.document.kind !== "interactions"
                      ? "Координата X"
                      : "Смещение участника по X"
                  }
                  type="number"
                  value={layoutX}
                  onChange={(e) => setLayoutX(e.currentTarget.value)}
                />
                <TextInput
                  label="Координата Y"
                  disabled={diagram.document.kind === "interactions"}
                  type="number"
                  value={layoutY}
                  onChange={(e) => setLayoutY(e.currentTarget.value)}
                />
                <Button
                  disabled={
                    !layoutNode ||
                    dirty ||
                    !Number.isFinite(Number(layoutX)) ||
                    !Number.isFinite(Number(layoutY))
                  }
                  onClick={() =>
                    setState({
                      ...state,
                      positions: [
                        ...state.positions.filter((p) => p.id !== layoutNode),
                        {
                          id: layoutNode,
                          x: Number(layoutX),
                          y: diagram.document.kind === "interactions" ? 0 : Number(layoutY),
                        },
                      ],
                    })
                  }
                >
                  Применить координаты
                </Button>
                <Button
                  variant="default"
                  disabled={!layoutNode || dirty}
                  onClick={() =>
                    setState({
                      ...state,
                      collapsedIds: state.collapsedIds.includes(layoutNode)
                        ? state.collapsedIds.filter((id) => id !== layoutNode)
                        : [...state.collapsedIds, layoutNode],
                    })
                  }
                >
                  Свернуть / раскрыть элемент
                </Button>
              </Group>
            </Stack>
          </Paper>
          {architecture && (
            <Suspense fallback={<Loader aria-label="Загружаем C4 canvas" />}>
              <BackendArchitectureGraph elements={es} links={ls} state={state} onSelect={select} />
            </Suspense>
          )}
          {state.selection && (
            <details
              key={`${diagram.pin.id}:${diagram.pin.version}:${state.selection.id}:${state.level}:${state.rootId}`}
            >
              <summary>Диагностика выбранного элемента</summary>
              <BackendAnalysisJobs
                projectId={projectId}
                sourceRevisionId={
                  "revisionId" in diagram.document.target ? diagram.document.target.revisionId : ""
                }
                target={diagram.document.target}
                diagramScope={{
                  pin: diagram.pin,
                  selectors: [
                    architecture && state.selection.type === "link"
                      ? {
                          kind: "architecture_relation",
                          id: state.selection.id,
                          projection: {
                            policy: "architecture-v1",
                            level: state.level!,
                            rootId: state.rootId!,
                          },
                        }
                      : { kind: "semantic", id: state.selection.id },
                  ],
                }}
              />
            </details>
          )}
          {architecture && (
            <BackendArchitectureInspector
              projectId={projectId}
              diagram={diagram}
              state={state}
              onOpen={open}
              onClose={() => {
                const id = state.selection?.id;
                changeState({ ...state, selection: null });
                if (id) focusWorkspaceRegion(`[data-c4-selection="${id}"]`);
              }}
            />
          )}
          {diagram.document.kind === "interactions" && (
            <BackendInteractions
              payload={diagram.document.payload}
              gaps={diagram.gaps}
              selection={state.selection}
              onSelect={select}
              onOpen={open}
              disabled={dirty}
              search={state.search}
              origin={state.origin}
              presentation={state}
            />
          )}
          {diagram.document.kind === "business_map" && (
            <BackendBusinessMap
              payload={diagram.document.payload}
              gaps={diagram.gaps}
              selection={state.selection}
              onSelect={select}
              onOpen={open}
              disabled={dirty}
              search={state.search}
              origin={state.origin}
              presentation={state}
              onDesign={
                changeHandoff
                  ? (refs) => {
                      changeHandoff.setHandoff({
                        projectId,
                        diagram: diagram.pin,
                        target: diagram.document.target,
                        refs: structuredClone(refs),
                      });
                      focusWorkspaceRegion("#backend-change-proposals-title");
                    }
                  : undefined
              }
              onArchitecture={(pin, elementId) => {
                if (dirty) return;
                c4Read.current?.abort();
                const controller = new AbortController();
                c4Read.current = controller;
                const scope = c4Scope.current;
                void readDiagram(projectId, pin, controller.signal)
                  .then((arch) => {
                    if (controller.signal.aborted || scope !== c4Scope.current) return;
                    onNavigate(businessMapArchitectureSearch(arch, diagram.targetHash, elementId));
                  })
                  .catch((error) => {
                    if (!controller.signal.aborted && scope === c4Scope.current)
                      setMessage(describeApiFailureDetailed(error));
                  });
              }}
            />
          )}
          {diagram.document.kind === "lifecycle" && (
            <BackendLifecycle
              payload={diagram.document.payload}
              gaps={diagram.gaps}
              selection={state.selection}
              onSelect={select}
              onOpen={open}
              disabled={dirty}
              search={state.search}
              origin={state.origin}
              presentation={state}
            />
          )}
          {artifactRef && (
            <Paper withBorder p="md">
              <Stack>
                <Text>Точный artifact row: {artifactRef.rowId}</Text>
                <BackendArtifactProjections
                  projectId={projectId}
                  target={diagram.document.target}
                  initialArtifact={`${artifactRef.locator.pin.kind}:${artifactRef.locator.pin.id}`}
                  initialView={artifactRef.locator.view}
                  initialEmbedded={artifactRef.locator.embedded?.contractId}
                  readOnly
                />
                <Button variant="default" onClick={() => setArtifactRef(undefined)}>
                  Вернуться к C4 members
                </Button>
              </Stack>
            </Paper>
          )}
          {(elements.data?.gaps.length ?? 0) > 0 && (
            <Alert
              color="yellow"
              title="Неполные и неизвестные границы"
              styles={{ title: { color: "var(--mantine-color-text)" } }}
            >
              {elements.data?.gaps.slice(0, 30).map((g) => (
                <Text key={g.id} size="sm">
                  {g.code}: {g.explanation}
                </Text>
              ))}
              <Text size="sm">
                Всего gaps: {elements.data?.gaps.length}. Runtime не подтверждён.
              </Text>
            </Alert>
          )}
          <Paper withBorder p="md">
            <Stack>
              <Title order={3}>Сохранить точный вид</Title>
              <TextInput
                label={
                  architecture
                    ? "Название C4 вида"
                    : diagram.document.kind === "business_map"
                      ? "Название business map вида"
                      : diagram.document.kind === "lifecycle"
                        ? "Название lifecycle вида"
                        : "Название interactions вида"
                }
                value={viewName}
                onChange={(e) => setViewName(e.currentTarget.value)}
              />
              <Group>
                <Button
                  disabled={dirty || !viewName.trim()}
                  onClick={() =>
                    void attempt({
                      operation: "view-create",
                      body: { name: viewName, state, idempotencyKey: crypto.randomUUID() },
                    })
                  }
                >
                  {architecture
                    ? "Сохранить новый C4 вид"
                    : diagram.document.kind === "business_map"
                      ? "Сохранить новый business map вид"
                      : diagram.document.kind === "lifecycle"
                        ? "Сохранить новый lifecycle вид"
                        : "Сохранить новый interactions вид"}
                </Button>
                <Button
                  disabled={dirty || !activeView}
                  onClick={() =>
                    void attempt({
                      operation: "view-save",
                      id: activeView!.id,
                      body: {
                        name: viewName,
                        state,
                        expectedVersion: activeView!.version,
                        idempotencyKey: crypto.randomUUID(),
                      },
                    })
                  }
                >
                  Сохранить расположение вида
                </Button>
              </Group>
              <Text size="sm">
                Новая версия mapping не изменяет сохранённый pin. Для неё создайте новый вид.
              </Text>
            </Stack>
          </Paper>
          <Paper withBorder p="md">
            <Stack>
              <Title order={3}>Fork и сравнение</Title>
              <TextInput
                label="Новый target для fork (JSON)"
                placeholder='{"revisionId":"…"} или {"changeProposal":{…}}'
                value={forkTarget}
                onChange={(e) => setForkTarget(e.currentTarget.value)}
              />
              {(diagram.document.kind === "interactions" ||
                diagram.document.kind === "business_map") && (
                <TextInput
                  label="Точный architecture pin для нового target (JSON)"
                  value={forkArchitecture}
                  onChange={(e) => setForkArchitecture(e.currentTarget.value)}
                />
              )}
              <TextInput
                label="Причина fork"
                value={forkReason}
                onChange={(e) => setForkReason(e.currentTarget.value)}
              />
              <Button
                disabled={dirty || !forkTarget || !forkReason.trim()}
                onClick={() => {
                  try {
                    void attempt({
                      operation: "fork",
                      body: {
                        source: diagram.pin,
                        target: JSON.parse(forkTarget),
                        ...(forkArchitecture ? { architecture: JSON.parse(forkArchitecture) } : {}),
                        reason: forkReason,
                        idempotencyKey: crypto.randomUUID(),
                      },
                    });
                  } catch {
                    setMessage("Неверный JSON target");
                  }
                }}
              >
                Создать fork
              </Button>
              <TextInput
                label="Точный pin для сравнения (JSON)"
                placeholder='{"id":"…","version":1,"contentHash":"…"}'
                value={comparePin}
                onChange={(e) => setComparePin(e.currentTarget.value)}
              />
              <Button
                disabled={!comparePin || dirty}
                onClick={() => {
                  void (async () => {
                    try {
                      const after = JSON.parse(comparePin) as BackendDiagramPin;
                      if (!Number.isSafeInteger(after.version))
                        throw new Error("Версия должна быть точной");
                      const r = await compareBackendDiagrams(projectId, {
                        before: diagram.pin,
                        after,
                        limit: 100,
                      });
                      if (r.status !== 200) throw new Error("Сравнение недоступно");
                      setComparison(r.data);
                    } catch (e) {
                      setMessage(describeApiFailureDetailed(e));
                    }
                  })();
                }}
              >
                Сравнить mappings
              </Button>
              {comparison && (
                <>
                  <Text>Изменений на странице: {comparison.items.length}</Text>
                  {comparison.items.map((item) => (
                    <Text key={item.id}>
                      {item.kind} · {item.id} · {item.fields.join(", ")}
                    </Text>
                  ))}
                  {comparison.nextCursor && (
                    <Button
                      onClick={() => {
                        void compareBackendDiagrams(projectId, {
                          before: comparison.before,
                          after: comparison.after,
                          limit: 100,
                          cursor: comparison.nextCursor,
                        })
                          .then((r) => {
                            if (r.status === 200) setComparison(r.data);
                          })
                          .catch((e) => setMessage(describeApiFailureDetailed(e)));
                      }}
                    >
                      Следующие изменения
                    </Button>
                  )}
                  <Button
                    variant="default"
                    onClick={() => onNavigate(diagramSearch(comparison.before))}
                  >
                    Открыть исходную версию сравнения
                  </Button>
                </>
              )}
            </Stack>
          </Paper>
        </>
      )}
    </Stack>
  );
}
function ArchitectureSavedLists({
  projectId,
  onNavigate,
  locked,
}: {
  projectId: string;
  onNavigate: Props["onNavigate"];
  locked: boolean;
}) {
  const [dc, setDC] = useState([""]);
  const [lc, setLC] = useState([""]);
  const [selection, setSelection] = useState("");
  const diagrams = useQuery({
    queryKey: ["c4-saved-list", projectId, dc.at(-1)],
    retry: false,
    queryFn: async ({ signal }) => {
      const r = await listBackendDiagramViews(
        projectId,
        { limit: 100, ...(dc.at(-1) ? { cursor: dc.at(-1) } : {}) },
        { signal },
      );
      if (r.status !== 200) throw new Error("C4 views unavailable");
      return r.data;
    },
  });
  const legacy = useQuery({
    queryKey: ["c4-legacy-saved-list", projectId, lc.at(-1)],
    retry: false,
    queryFn: async ({ signal }) => {
      const r = await listBackendSavedViews(
        projectId,
        { limit: 50, ...(lc.at(-1) ? { cursor: lc.at(-1) } : {}) },
        { signal },
      );
      if (r.status !== 200) throw new Error("Legacy views unavailable");
      return r.data;
    },
  });
  return (
    <Paper withBorder p="md">
      <Stack>
        <Title order={3}>Сохранённые виды проекта</Title>
        <NativeSelect
          label="Открыть сохранённый C4 / interactions / lifecycle / business map / Flow / Database вид"
          value={selection}
          onChange={(e) => setSelection(e.currentTarget.value)}
          data={[
            { value: "", label: "Выберите точный вид" },
            ...(diagrams.data?.items ?? []).map((v) => ({
              value: JSON.stringify({ diagramViewId: v.id, diagramViewVersion: v.version }),
              label: `${v.kind === "business_map" ? "Business map" : v.kind === "lifecycle" ? "Lifecycle" : v.kind === "interactions" ? "Interactions" : "C4"} · ${v.name} · v${v.version}`,
            })),
            ...(legacy.data?.items ?? []).map((v) => ({
              value: JSON.stringify({ viewId: v.id, viewVersion: v.version }),
              label: `${v.kind} · ${v.name} · v${v.version}`,
            })),
          ]}
        />
        <Button disabled={locked || !selection} onClick={() => onNavigate(JSON.parse(selection))}>
          Открыть точный сохранённый вид
        </Button>
        {(diagrams.isError || legacy.isError) && (
          <Alert role="alert" color="red">
            Не удалось загрузить один из списков видов.
            <Button
              onClick={() => {
                setDC([""]);
                setLC([""]);
                void diagrams.refetch();
                void legacy.refetch();
              }}
            >
              Обновить списки видов
            </Button>
          </Alert>
        )}
        <Group>
          <Button disabled={dc.length === 1} onClick={() => setDC((c) => c.slice(0, -1))}>
            Предыдущие C4 виды
          </Button>
          <Button
            disabled={!diagrams.data?.nextCursor}
            onClick={() => setDC((c) => [...c, diagrams.data!.nextCursor])}
          >
            Следующие C4 виды
          </Button>
          <Button disabled={lc.length === 1} onClick={() => setLC((c) => c.slice(0, -1))}>
            Предыдущие Flow/Database виды
          </Button>
          <Button
            disabled={!legacy.data?.nextCursor}
            onClick={() => setLC((c) => [...c, legacy.data!.nextCursor])}
          >
            Следующие Flow/Database виды
          </Button>
        </Group>
      </Stack>
    </Paper>
  );
}

function DiagramEditor(props: {
  document: BackendDiagramDocument;
  onChange: (d: BackendDiagramDocument) => void;
  onSave: () => void;
  onCancel: () => void;
  busy: boolean;
}) {
  return props.document.kind === "architecture" ? (
    <BackendArchitectureEditor {...props} document={props.document} />
  ) : props.document.kind === "business_map" ? (
    <BackendBusinessMapEditor {...props} document={props.document} />
  ) : props.document.kind === "lifecycle" ? (
    <BackendLifecycleEditor {...props} document={props.document} />
  ) : (
    <BackendInteractionsEditor {...props} document={props.document} />
  );
}
