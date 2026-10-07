import { useEffect, useRef, useState, useMemo } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Group,
  Loader,
  Menu,
  Text,
  Tooltip,
} from "@mantine/core";
import {
  IconArrowLeft,
  IconChevronRight,
  IconCube,
  IconDots,
  IconList,
  IconSearch,
  IconTopologyStar,
  IconRefresh,
  IconChecks,
} from "@tabler/icons-react";
import {
  useGetBackendProject,
  listBackendDiagrams,
  getGetBackendProjectQueryKey,
} from "@/api/generated/backend-projects/backend-projects";
import type { BackendExplorePage, BackendProject } from "@/api/generated/schemas";
import { backendReadTargetKey } from "../backendReadTargets";
import type { BackendWorkspaceSearch } from "../backendWorkspaceSearch";
import { diagramSearch, workspacePinError } from "../backendWorkspaceSearch";
import { ArchitectureScope } from "./ArchitectureScope";
import { viewOf, useExplorerNavigation, resolvedTargetSearch, type View } from "./navigation";
import { resolveEntry, readSourceMap, readFlowMap, readLineageMap, type Entry } from "./reads";
import { ExploreCanvas } from "./LazyExploreCanvas";
import { Inspector } from "./Inspector";
import { SearchDialog } from "./SearchDialog";
import { ViewChooser } from "./ViewChooser";
import { Results } from "./Results";
import { readComparisonMap } from "./comparisonMap";
import { savedCanvas } from "./savedPresentation";
import { readAccessMap } from "./accessMap";
import { readDatabaseMap } from "./databaseMap";
import { TargetSummary } from "./TargetSummary";
import { RecoveryNotice } from "./RecoveryNotice";
import { useResultSignal } from "./resultSignals";
import { DiagramMap } from "./DiagramMap";
import { kindName, nodeSubtitle, type MapNode, type MapData } from "./model";
import styles from "./Explorer.module.css";
import { useCardClicks } from "./cardClicks";
import { SourceCatalog, catalogScope } from "./SourceCatalog";
import { ScenarioWorkspace } from "./ScenarioWorkspace";
import { ScenarioStart } from "./ScenarioStart";
const EMPTY_SEARCH: BackendWorkspaceSearch = {};
const NO_NAVIGATE = () => {};
const views: [View, string][] = [
  ["structure", "Структура"],
  ["scenarios", "Сценарии"],
  ["data", "Данные"],
];
export type WorkspaceProps = {
  projectId: string;
  sourcePin?: BackendWorkspaceSearch;
  onSourceNavigate?: (pin: BackendWorkspaceSearch, replace?: boolean) => void;
};
export function Workspace(props: WorkspaceProps) {
  let error = workspacePinError(props.sourcePin ?? EMPTY_SEARCH);
  const pin = props.sourcePin ?? EMPTY_SEARCH;
  try {
    if (pin.wbTarget) backendReadTargetKey(JSON.parse(pin.wbTarget));
    else if (pin.changeProposalId !== undefined || pin.proposalRevisionId !== undefined)
      backendReadTargetKey({
        changeProposal: {
          proposalId: pin.changeProposalId ?? "",
          proposalRevisionId: pin.proposalRevisionId ?? "",
        },
      });
    else if (!pin.viewId && !pin.diagramId && !pin.diagramViewId && pin.revisionId !== undefined)
      backendReadTargetKey({ revisionId: pin.revisionId });
  } catch {
    error = "Точная версия источника или черновика предложения указана неполно.";
  }
  if (error)
    return (
      <section className={styles.workspace} data-testid="backend-project-page">
        <Alert color="red" role="alert">
          {error}
        </Alert>
      </section>
    );
  if (pin.viewId || pin.diagramId || pin.diagramViewId) return <PinnedWorkspace {...props} />;
  return <WorkspaceEntry {...props} />;
}
function PinnedWorkspace(props: WorkspaceProps) {
  const search = props.sourcePin ?? EMPTY_SEARCH;
  const onSourceNavigate = props.onSourceNavigate;
  const alias = !!search.viewId && search.viewVersion === undefined;
  const query = useQuery({
    queryKey: [
      "workbench-pin",
      props.projectId,
      search.viewId,
      search.viewVersion,
      search.diagramId,
      search.diagramVersion,
      search.diagramHash,
      search.diagramViewId,
      search.diagramViewVersion,
    ],
    retry: false,
    staleTime: alias ? 0 : Infinity,
    refetchOnMount: alias ? "always" : false,
    queryFn: ({ signal }) => resolveEntry(props.projectId, search, "", signal),
  });
  useEffect(() => {
    if (alias && query.data?.saved && !query.isFetching)
      onSourceNavigate?.({ ...query.data.search, viewVersion: query.data.saved.version }, true);
  }, [alias, query.data, query.isFetching, onSourceNavigate]);
  if (query.isError)
    return (
      <section className={styles.workspace} data-testid="backend-project-page">
        <div className={styles.empty}>
          <Alert color="red" title="Точный вид недоступен">
            {query.error instanceof Error
              ? query.error.message
              : "Не удалось прочитать выбранную версию."}
          </Alert>
          <Button mt="md" onClick={() => void query.refetch()}>
            {search.viewId ? "Повторить загрузку вида" : "Повторить загрузку схемы"}
          </Button>
        </div>
      </section>
    );
  if (!query.data || (alias && query.isFetching))
    return (
      <section className={styles.workspace} data-testid="backend-project-page">
        <div className={styles.empty}>
          <Loader aria-label="Загружаем точный вид" />
        </div>
      </section>
    );
  return <WorkspaceEntry {...props} resolvedEntry={query.data} />;
}
function WorkspaceEntry(props: WorkspaceProps & { resolvedEntry?: Entry }) {
  const project = useGetBackendProject(props.projectId, { query: { retry: false } });
  const search = props.sourcePin ?? EMPTY_SEARCH;
  const entry = useQuery({
    queryKey: [
      "workbench-entry",
      props.projectId,
      search.wbTarget,
      search.revisionId,
      search.changeProposalId,
      search.proposalRevisionId,
      search.diagramId,
      search.diagramVersion,
      search.diagramHash,
      search.diagramViewId,
      search.diagramViewVersion,
      search.viewId,
      search.viewVersion,
    ],
    enabled: !props.resolvedEntry && project.data?.status === 200,
    retry: false,
    staleTime: Infinity,
    queryFn: ({ signal }) =>
      resolveEntry(
        props.projectId,
        search,
        project.data?.status === 200 ? project.data.data.currentRevisionId : "",
        signal,
      ),
  });
  const navigate = props.onSourceNavigate ?? NO_NAVIGATE;
  const entryData = props.resolvedEntry ?? entry.data;
  useEffect(() => {
    if (!entryData) return;
    const t = entryData.target;
    if (
      !search.revisionId &&
      !search.changeProposalId &&
      !search.diagramId &&
      !search.diagramViewId &&
      !search.viewId &&
      !search.wbTarget &&
      "revisionId" in t
    )
      navigate({ ...search, revisionId: t.revisionId ?? undefined }, true);
  }, [entryData, search, navigate]);
  const needsPin =
    !!props.onSourceNavigate &&
    !!entryData &&
    !search.revisionId &&
    !search.changeProposalId &&
    !search.diagramId &&
    !search.diagramViewId &&
    !search.viewId &&
    !search.wbTarget;
  if (needsPin)
    return (
      <section className={styles.workspace} data-testid="backend-project-page">
        <div className={styles.empty}>
          <Loader aria-label="Закрепляем версию модели" />
        </div>
      </section>
    );
  return (
    <section className={styles.workspace} data-testid="backend-project-page">
      {project.isError || entry.isError ? (
        <div className={styles.empty}>
          <Alert color="red" title="Этот вид недоступен">
            Не удалось открыть точную версию. Она не была заменена текущей.
          </Alert>
          <Button
            mt="md"
            onClick={() => void (project.isError ? project.refetch() : entry.refetch())}
          >
            Повторить
          </Button>
          <Button component={Link} to="/backend-projects" variant="subtle">
            К проектам
          </Button>
        </div>
      ) : !entryData || project.data?.status !== 200 ? (
        <div className={styles.empty}>
          <Loader aria-label="Открываем проект" />
        </div>
      ) : (
        <ResolvedWorkspace
          key={props.projectId}
          project={project.data.data}
          routeSearch={search}
          entry={{
            ...entryData,
            search: entryData.saved
              ? { ...entryData.search, ...search }
              : entryData.diagram
                ? {
                    ...search,
                    wbView:
                      search.wbView ??
                      (entryData.diagram.document.kind === "architecture"
                        ? "structure"
                        : entryData.diagram.document.kind === "lifecycle"
                          ? "data"
                          : "scenarios"),
                  }
                : search,
          }}
          navigate={navigate}
        />
      )}
    </section>
  );
}
function ResolvedWorkspace({
  project,
  entry,
  navigate,
  routeSearch,
}: {
  project: BackendProject;
  entry: Entry;
  routeSearch: BackendWorkspaceSearch;
  navigate: (pin: BackendWorkspaceSearch, replace?: boolean) => void;
}) {
  const { target } = entry;
  const search = entry.search;
  const view = viewOf(search);
  const exact = backendReadTargetKey(target);

  const client = useQueryClient();
  const changes = useResultSignal(project.id, "changes", search.wbPanel === "changes");
  const checks = useResultSignal(project.id, "checks", search.wbPanel === "checks");
  const [searchOpen, setSearchOpen] = useState(false);
  const [searchText, setSearchText] = useState("");
  const [copied, setCopied] = useState(false);
  const objectList = !!search.wbList;
  const opener = useRef<HTMLElement | null>(null);
  const pin = useMemo(() => resolvedTargetSearch(target), [target]);
  const nav = useExplorerNavigation(project.id, exact, search, navigate, pin, routeSearch);
  const selected = search.wbSelection ?? search.recordId ?? search.diagramSelection?.split(":")[1];
  function go(next: BackendWorkspaceSearch, replace = false) {
    if (
      Object.hasOwn(next, "recordId") &&
      next.recordId === undefined &&
      next.wbSelection === undefined
    )
      next = { ...next, wbSelection: "" };
    const exactNext =
      next.wbTarget ||
      next.revisionId ||
      next.changeProposalId ||
      next.diagramId ||
      next.diagramViewId ||
      next.viewId
        ? next
        : { ...pin, ...next };
    nav.go(exactNext, replace);
  }
  function closeInspector() {
    go(
      {
        ...search,
        recordId: undefined,
        wbSelection: "",
        recordType: undefined,
        diagramSelection: undefined,
      },
      true,
    );
    requestAnimationFrame(() => {
      const focus = opener.current?.isConnected
        ? opener.current
        : document.querySelector<HTMLElement>(
            `button[data-object-id="${CSS.escape(selected ?? "")}"]`,
          );
      focus?.focus({ preventScroll: true });
    });
  }
  function select(id: string, type: "node" | "edge" = "node") {
    opener.current = document.activeElement as HTMLElement;
    go(
      {
        ...search,
        ...(/^[0-9a-f-]{36}$/.test(id)
          ? { recordId: id, wbSelection: undefined }
          : { wbSelection: id, recordId: undefined }),
        recordType: type,
      },
      true,
    );
  }
  function enter(node: MapNode) {
    if (node.group?.startsWith("db-facet:")) {
      go({ ...search, facetKey: node.group.slice(9), recordId: undefined, wbSelection: undefined });
      return;
    }
    if (node.group?.startsWith("facet:")) {
      go({ ...search, facetKey: node.group.slice(6), recordId: undefined, wbSelection: undefined });
      return;
    }
    if (node.group) {
      const kind = node.group.startsWith("kind:") ? node.group.slice(5) : "http_operation";
      go({
        ...pin,
        wbView: kind === "datastore" || kind === "table" ? "data" : "structure",
        wbMode: node.group === "kind:http_operation" ? "collections" : "objects",
        wbKind: kind === "all" ? undefined : kind,
        wbScope: search.wbScope,
        wbGroup: node.group.startsWith("kind:") ? undefined : node.group,
      });
      return;
    }
    const base = { ...pin, wbScope: node.id };
    if (["http_operation", "job", "handler", "symbol"].includes(node.kind))
      go({
        ...pin,
        wbView: "scenarios",
        wbMode: "flow",
        entrypointId: node.id,
        wbCatalog:
          ["children", "collections", "objects"].includes(search.wbMode ?? "") ||
          search.wbFlowPart === "entrypoints"
            ? catalogScope(search)
            : undefined,
      });
    else if (node.kind === "flow")
      go({
        ...pin,
        wbView: "scenarios",
        wbMode: "flow",
        flowId: node.id,
        entrypointId: search.entrypointId,
      });
    else if (["datastore", "data_store"].includes(node.kind))
      go({
        ...base,
        wbView: "data",
        wbMode: "objects",
        wbKind: "table",
        datastoreId: node.id,
        facetKey:
          typeof node.attributes.facetKey === "string" ? node.attributes.facetKey : undefined,
      });
    else if (node.kind === "table")
      go({ ...base, wbView: "data", wbMode: "neighborhood", recordId: node.id });
    else if (
      view === "structure" &&
      ["service", "application", "component", "module", "system", "software_system"].includes(
        node.kind,
      )
    )
      go({ ...base, wbView: "structure", wbMode: "architecture" });
    else if (
      node.childCount > 0 ||
      ["system", "service", "application", "component", "module"].includes(node.kind)
    )
      go({ ...base, wbView: view, wbMode: "children" });
    else
      go({ ...base, wbView: view, wbMode: "neighborhood", recordId: node.id, recordType: "node" });
  }
  function openObject(node: MapNode) {
    go({
      ...pin,
      wbView: ["datastore", "table", "column"].includes(node.kind)
        ? "data"
        : ["flow", "flow_step", "job"].includes(node.kind)
          ? "scenarios"
          : "structure",
      wbMode: "neighborhood",
      wbScope: node.id,
      recordId: node.id,
    });
    setSearchOpen(false);
  }
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(window.location.href);
      setCopied(true);
      setTimeout(() => setCopied(false), 1800);
    } catch {
      setCopied(false);
    }
  };
  const refresh = () => {
    void client.invalidateQueries({
      predicate: (q) => q.queryKey[0] === "workbench-catalog" && q.queryKey[1] === project.id,
    });
    void client.invalidateQueries({ queryKey: getGetBackendProjectQueryKey(project.id) });
    void client.invalidateQueries({
      predicate: (q) =>
        q.queryKey[0] === "workbench-result" &&
        q.queryKey[1] === project.id &&
        ["check", "replay", "import"].includes(String(q.queryKey[2])),
    });
  };
  const sourceMode =
    !entry.diagram &&
    !search.wbPanel &&
    search.wbMode !== "unmapped" &&
    search.wbMode !== "architecture";
  const flow = search.wbMode === "flow" || !!search.flowId || !!search.entrypointId;
  const scenario =
    flow &&
    !!(search.entrypointId || search.flowId) &&
    !["entrypoints", "accesses", "reverse"].includes(search.wbFlowPart ?? "logic");
  const map = useQuery<MapData & { page?: BackendExplorePage; flowId?: string }>({
    queryKey: [
      "workbench-map",
      project.id,
      exact,
      search.wbMode,
      search.wbScope,
      search.wbGroup,
      search.wbKind,
      search.wbQuery,
      search.flowId,
      search.entrypointId,
      search.facetKey,
      search.datastoreId,
      search.dataNodeId,
      search.wbFlowPart,
      search.wbAccessKind,
      search.wbReverseAccessKind,
      search.wbLineageSeed,
    ],
    enabled: sourceMode && !scenario,
    retry: false,
    staleTime: Infinity,
    queryFn: ({ signal }) =>
      search.wbFlowPart === "accesses" || search.wbFlowPart === "reverse"
        ? readAccessMap(project.id, target, search, signal)
        : search.wbMode === "comparison"
          ? readComparisonMap(project.id, target, search, signal)
          : search.datastoreId
            ? readDatabaseMap(project.id, target, search, signal)
            : search.wbMode === "lineage"
              ? readLineageMap(project.id, target, search, signal)
              : flow
                ? readFlowMap(project.id, target, search, signal)
                : readSourceMap(project.id, target, search, signal),
  });
  const canvas = useMemo(
    () =>
      savedCanvas(map.data?.nodes ?? [], map.data?.edges ?? [], entry.saved, search.wbExpandGroups),
    [map.data, entry.saved, search.wbExpandGroups],
  );
  const diagrams = useQuery({
    queryKey: ["workbench-catalog", project.id, "home-diagrams", map.data?.page?.targetHash],
    enabled: !!map.data && search.wbMode === undefined && !entry.diagram,
    retry: false,
    queryFn: async ({ signal }) => {
      const r = await listBackendDiagrams(
        project.id,
        { kind: "architecture", limit: 20, targetHash: map.data?.page?.targetHash },
        { signal },
      );
      if (r.status !== 200) throw new Error("Каталог схем недоступен");
      return r.data;
    },
  });
  const compatible = useMemo(
    () =>
      diagrams.data?.items.filter((item) => item.targetHash === map.data?.page?.targetHash) ?? [],
    [diagrams.data, map.data?.page?.targetHash],
  );
  const resolvedHomes = useRef(new Set<string>());
  useEffect(() => {
    if (diagrams.isPending || resolvedHomes.current.has(exact)) return;
    resolvedHomes.current.add(exact);
    if (
      search.wbMode === undefined &&
      !search.wbPanel &&
      !entry.diagram &&
      compatible.length === 1 &&
      !diagrams.data?.nextCursor
    )
      navigate({ ...diagramSearch(compatible[0]!.pin), wbView: "structure" }, true);
    else if (
      search.wbMode === undefined &&
      !search.wbPanel &&
      !entry.diagram &&
      compatible.length > 1
    )
      navigate({ ...pin, wbView: "structure", wbMode: "architecture" }, true);
  }, [
    diagrams.isPending,
    diagrams.data,
    exact,
    compatible,
    search.wbMode,
    search.wbPanel,
    entry.diagram,
    navigate,
    pin,
  ]);
  const selectedNode = map.data?.nodes.find((n) => n.id === selected);
  const selectedEdge = map.data?.edges.find((n) => n.id === selected);
  const panelTitle = search.wbPanel
    ? {
        changes: "Изменения",
        checks: "Проверки",
        sources: "Источники и история",
        views: "Сохранённые виды",
        observations: "Наблюдения",
        replay: "Результаты воспроизведения",
      }[search.wbPanel]
    : undefined;
  return (
    <>
      <header className={styles.header}>
        <Link to="/backend-projects" aria-label="Все бэкенд-проекты" className={styles.projectIcon}>
          <IconCube size={19} />
        </Link>
        <h1 className={styles.projectName}>{project.name}</h1>
        <button
          className={`${styles.search} ${styles.searchButton}`}
          aria-label="Поиск по всему проекту"
          onClick={() => setSearchOpen(true)}
        >
          <IconSearch size={16} />
          <span>{searchText || "Найти операцию, таблицу, событие…"}</span>
        </button>
        <TargetSummary
          projectId={project.id}
          target={target}
          onOpen={() => go({ ...search, wbPanel: "sources" })}
        />
        <Button
          variant={search.wbPanel === "changes" ? "light" : "subtle"}
          size="compact-sm"
          onClick={() => go({ ...search, wbPanel: "changes", wbResult: undefined })}
        >
          Изменения{changes.count > 0 ? ` · Новое ${changes.count}` : ""}
        </Button>
        <Button
          variant={search.wbPanel === "checks" ? "light" : "subtle"}
          size="compact-sm"
          leftSection={<IconChecks size={15} />}
          onClick={() => go({ ...search, wbPanel: "checks", wbResult: undefined })}
        >
          Проверки{checks.count > 0 ? ` · Новое ${checks.count}` : ""}
        </Button>
        <Tooltip label="Обновить результаты">
          <ActionIcon variant="subtle" aria-label="Обновить результаты" onClick={refresh}>
            <IconRefresh size={17} />
          </ActionIcon>
        </Tooltip>
        <Menu position="bottom-end">
          <Menu.Target>
            <ActionIcon variant="subtle" aria-label="Меню проекта">
              <IconDots size={19} />
            </ActionIcon>
          </Menu.Target>
          <Menu.Dropdown>
            <Menu.Item onClick={() => void copy()}>
              {copied ? "Ссылка скопирована" : "Скопировать ссылку"}
            </Menu.Item>
            {(["sources", "views", "observations", "replay"] as const).map((panel, i) => (
              <Menu.Item
                key={panel}
                onClick={() => go({ ...search, wbPanel: panel, wbResult: undefined })}
              >
                {["Источники и история", "Сохранённые виды", "Наблюдения", "Воспроизведения"][i]}
              </Menu.Item>
            ))}
          </Menu.Dropdown>
        </Menu>
      </header>
      <div className={styles.toolbar}>
        <nav className={styles.breadcrumbs} aria-label="Положение в проекте">
          <ActionIcon variant="subtle" aria-label="Назад к предыдущему виду" onClick={nav.back}>
            <IconArrowLeft size={17} />
          </ActionIcon>
          <Button
            variant="subtle"
            size="compact-xs"
            onClick={() => go({ ...pin, wbView: "structure", wbMode: "overview" })}
          >
            {project.name}
          </Button>
          {(map.data?.page?.ancestors ?? [])
            .filter((n) => n.kind !== "system")
            .slice(-2)
            .map((n) => (
              <span key={n.id} style={{ display: "contents" }}>
                <IconChevronRight size={12} />
                <Button variant="subtle" size="compact-xs" onClick={() => enter(n)}>
                  {n.name}
                </Button>
              </span>
            ))}
          {map.data?.page?.scope && search.wbGroup && (
            <>
              <IconChevronRight size={12} />
              <Button
                variant="subtle"
                size="compact-xs"
                onClick={() => enter(map.data!.page!.scope!)}
              >
                {map.data.page.scope.name}
              </Button>
            </>
          )}
          {(search.wbScope || search.wbGroup || flow || panelTitle) && (
            <>
              <IconChevronRight size={12} />
              <Text size="xs" lineClamp={1}>
                {panelTitle ??
                  (flow ? "Логика операции" : (map.data?.title ?? "Выбранная область"))}
              </Text>
            </>
          )}
        </nav>
        <nav className={styles.tabs} aria-label="Представления проекта">
          {views.map(([value, label]) => (
            <button
              key={value}
              className={styles.tab}
              aria-current={!search.wbPanel && view === value ? "page" : undefined}
              onClick={() => nav.switchView(value)}
            >
              {label}
            </button>
          ))}
        </nav>
        <Button
          variant="subtle"
          size="compact-sm"
          leftSection={<IconList size={16} />}
          aria-pressed={objectList}
          onClick={() => {
            go({ ...search, wbList: !objectList }, true);
          }}
        >
          Объекты
        </Button>
      </div>
      {"revisionId" in target && target.revisionId !== project.currentRevisionId && (
        <Alert className={styles.notice} color="teal" title="Доступна новая версия">
          <Button
            variant="subtle"
            size="compact-xs"
            onClick={() =>
              go({ revisionId: project.currentRevisionId, wbView: view, wbMode: "overview" })
            }
          >
            Открыть новую версию
          </Button>{" "}
          Текущая карта остаётся на выбранной версии.
        </Alert>
      )}
      <RecoveryNotice projectId={project.id} />
      <output className={styles.srOnly}>
        {changes.count + checks.count > 0
          ? `Новые результаты: ${changes.count + checks.count}`
          : ""}
      </output>
      <div className={styles.body}>
        {search.wbPanel ? (
          <Results projectId={project.id} target={target} search={search} onNavigate={go} />
        ) : entry.diagram ? (
          <DiagramMap
            projectId={project.id}
            entry={entry}
            search={search}
            onNavigate={go}
            nav={nav}
            list={objectList}
          />
        ) : search.wbMode === "architecture" ||
          (search.wbMode === "unmapped" && view === "structure") ? (
          <ArchitectureScope
            projectId={project.id}
            target={target}
            scope={search.wbScope}
            onNavigate={go}
          />
        ) : scenario ? (
          <ScenarioWorkspace
            projectId={project.id}
            target={target}
            search={search}
            saved={entry.saved}
            nav={nav}
            onNavigate={go}
            list={objectList}
          />
        ) : search.wbMode === "unmapped" && view === "scenarios" ? (
          <ScenarioStart projectId={project.id} target={target} search={search} onNavigate={go} />
        ) : search.wbMode === "unmapped" ? (
          <div className={styles.stage}>
            <ViewChooser
              projectId={project.id}
              target={target}
              scope={search.wbScope}
              view={view}
              onNavigate={go}
            />
            <Group justify="center" mb={40}>
              <Button
                variant="light"
                onClick={() =>
                  go({
                    ...pin,
                    wbView: view,
                    wbMode: "objects",
                    wbKind: view === "data" ? "datastore" : "http_operation",
                  })
                }
              >
                Обзор проекта
              </Button>
              <Button variant="subtle" onClick={() => go({ ...search, wbPanel: "views" })}>
                Открыть схему
              </Button>
            </Group>
          </div>
        ) : (
          <>
            <div className={styles.stage}>
              <h2 className={styles.srOnly}>{map.data?.title ?? "Карта проекта"}</h2>
              {objectList && compatible.length > 0 && !search.wbMode && (
                <Alert className={styles.notice} color="teal">
                  {compatible.length === 1
                    ? "Доступна архитектурная схема."
                    : "Доступно несколько архитектурных схем."}{" "}
                  <Button
                    variant="subtle"
                    size="compact-xs"
                    onClick={() => go({ ...search, wbPanel: "views" })}
                  >
                    Выбрать схему
                  </Button>
                </Alert>
              )}
              {flow && (
                <Group px="xl" py="xs">
                  <Button
                    size="compact-xs"
                    variant="subtle"
                    onClick={() =>
                      go({
                        ...search,
                        wbFlowPart: "entrypoints",
                        wbCursor: undefined,
                        recordId: undefined,
                      })
                    }
                  >
                    Операции
                  </Button>
                  {search.flowId && (
                    <Button
                      size="compact-xs"
                      variant="subtle"
                      onClick={() =>
                        go({
                          ...search,
                          wbFlowPart: "logic",
                          wbCursor: undefined,
                          recordId: undefined,
                        })
                      }
                    >
                      Логика
                    </Button>
                  )}
                  {search.entrypointId && (
                    <Button
                      size="compact-xs"
                      variant="subtle"
                      onClick={() =>
                        go({
                          ...search,
                          wbFlowPart: "accesses",
                          wbCursor: undefined,
                          recordId: undefined,
                        })
                      }
                    >
                      Чтение и запись
                    </Button>
                  )}
                  {search.dataNodeId && (
                    <Button
                      size="compact-xs"
                      variant="subtle"
                      onClick={() =>
                        go({
                          ...search,
                          wbFlowPart: "reverse",
                          wbCursor: undefined,
                          recordId: undefined,
                        })
                      }
                    >
                      Обращения к данным
                    </Button>
                  )}
                </Group>
              )}
              {entry.saved?.state.collapsedGroupIds.length ? (
                <Group px="xl">
                  <Text size="xs" c="dimmed">
                    {canvas.hidden
                      ? `Свёрнуто объектов: ${canvas.hidden}`
                      : "Группы развёрнуты локально"}
                  </Text>
                  <Button
                    variant="subtle"
                    size="compact-xs"
                    onClick={() => go({ ...search, wbExpandGroups: !search.wbExpandGroups }, true)}
                  >
                    {search.wbExpandGroups ? "Вернуть свёрнутый вид" : "Показать скрытые объекты"}
                  </Button>
                </Group>
              ) : null}
              {map.isError ? (
                <Empty
                  title="Область недоступна"
                  description="Не удалось прочитать эту область выбранной версии."
                  action={<Button onClick={() => void map.refetch()}>Повторить</Button>}
                />
              ) : !map.data ? (
                <div className={styles.empty}>
                  <Loader aria-label="Загружаем карту" />
                </div>
              ) : map.data.nodes.length === 0 ? (
                <Empty
                  title="В этой области пока нет объектов"
                  description={
                    map.data.partial ??
                    "Импортированная модель не содержит этих сведений. Источники и связи может уточнить агент через MCP."
                  }
                />
              ) : map.data.presentation === "catalog" ? (
                <SourceCatalog
                  data={map.data}
                  onOpen={enter}
                  selected={selected}
                  scroll={nav.presentation.scroll}
                  onScroll={nav.updateScroll}
                  filter={search.wbFilter}
                  onFilter={(wbFilter) => go({ ...search, wbFilter }, true)}
                />
              ) : objectList ? (
                <ObjectList
                  data={map.data}
                  selection={selected}
                  onSelect={select}
                  onEnter={enter}
                  scroll={nav.presentation.scroll}
                  onScroll={nav.updateScroll}
                />
              ) : (
                <ExploreCanvas
                  key={JSON.stringify([
                    exact,
                    search.wbMode,
                    search.wbScope,
                    search.wbGroup,
                    search.flowId,
                    search.entrypointId,
                    search.viewId,
                    search.viewVersion,
                    search.wbFlowPart,
                  ])}
                  nodes={canvas.nodes}
                  edges={canvas.edges}
                  selection={selected}
                  camera={nav.presentation.camera}
                  onCamera={nav.updateCamera}
                  onSelect={select}
                  onEnter={enter}
                />
              )}
            </div>
            {selected && (
              <Inspector
                key={`${exact}:${selected}`}
                projectId={project.id}
                target={target}
                search={search}
                node={selectedNode}
                edge={selectedEdge}
                id={selected}
                onClose={closeInspector}
                onNavigate={go}
                onEnter={enter}
              />
            )}
          </>
        )}
      </div>
      <SearchDialog
        projectId={project.id}
        target={target}
        opened={searchOpen}
        query={searchText}
        onQuery={setSearchText}
        onClose={() => setSearchOpen(false)}
        onOpen={openObject}
      />
    </>
  );
}
export function Empty({
  title,
  description,
  action,
}: {
  title: string;
  description: string;
  action?: React.ReactNode;
}) {
  return (
    <div className={styles.empty}>
      <div className={styles.emptyIcon}>
        <IconTopologyStar size={28} stroke={1.4} />
      </div>
      <h2 className={styles.stageTitle}>{title}</h2>
      <Text size="sm" c="dimmed" mt="sm">
        {description}
      </Text>
      {action && <div style={{ marginTop: 20 }}>{action}</div>}
    </div>
  );
}
function ObjectList({
  data,
  selection,
  onSelect,
  onEnter,
  scroll,
  onScroll,
}: {
  data: MapData;
  selection?: string;
  onSelect: (id: string, type?: "node" | "edge") => void;
  onEnter: (n: MapNode) => void;
  scroll: number;
  onScroll: (n: number) => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const clicks = useCardClicks(
    onSelect,
    (id) => {
      const node = data.nodes.find((n) => n.id === id);
      if (node) onEnter(node);
    },
    data,
  );
  useEffect(() => {
    if (ref.current) ref.current.scrollTop = scroll;
  }, [data, scroll]);
  return (
    <div
      className={styles.list}
      ref={ref}
      onScroll={(e) => onScroll(e.currentTarget.scrollTop)}
      aria-label="Объекты карты"
    >
      <Text size="xs" c="dimmed" mb="sm">
        {data.nodes.length} из {data.total} · {data.subtitle}
      </Text>
      {data.partial && (
        <Text size="sm" mb="md">
          {data.partial}
        </Text>
      )}
      {data.nodes.map((node) => (
        <div key={node.id} className={styles.resultRow}>
          <button
            className={styles.listRow}
            aria-pressed={selection === node.id}
            onClick={(e) => clicks.click(node.id, e.detail)}
            onDoubleClick={() => clicks.doubleClick(node.id)}
          >
            <span>
              <span className={styles.listName}>{node.name}</span>
              <span className={styles.subtitle} style={{ display: "block" }}>
                {nodeSubtitle(node) || node.description}
              </span>
            </span>
            <Badge variant="light" color="gray">
              {kindName(node.kind)}
            </Badge>
          </button>
          <Button
            variant="subtle"
            size="compact-xs"
            onClick={() => {
              clicks.cancel();
              onEnter(node);
            }}
          >
            Что внутри
          </Button>
        </div>
      ))}
      {data.edges.map((edge) => (
        <button
          key={edge.id}
          className={styles.listRow}
          aria-pressed={selection === edge.id}
          onClick={() => {
            clicks.cancel();
            onSelect(edge.id, "edge");
          }}
        >
          {data.nodes.find((n) => n.id === edge.from)?.name} →{" "}
          {data.nodes.find((n) => n.id === edge.to)?.name}
          <span>{edge.label || edge.kind}</span>
        </button>
      ))}
    </div>
  );
}
