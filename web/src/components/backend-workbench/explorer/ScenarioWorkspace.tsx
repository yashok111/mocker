import { useMemo, useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import { Alert, Button, Loader, Text } from "@mantine/core";
import type { BackendReadTarget, BackendSavedViewResponse } from "@/api/generated/schemas";
import type { BackendWorkspaceSearch } from "../backendWorkspaceSearch";
import { backendReadTargetKey } from "../backendReadTargets";
import { readBehavior } from "./behaviorReads";
import { readFlowMap, readSourceMap } from "./reads";
import { SourceCatalog, restoreCatalog, triggerLabel, purposeText } from "./SourceCatalog";
import { ExploreCanvas } from "./LazyExploreCanvas";
import { Inspector } from "./Inspector";
import { savedCanvas } from "./savedPresentation";
import { resolvedTargetSearch, type useExplorerNavigation } from "./navigation";
import type { MapNode, MapData } from "./model";
import styles from "./Explorer.module.css";

export function ScenarioWorkspace({
  projectId,
  target,
  search,
  saved,
  nav,
  onNavigate,
  list,
}: {
  projectId: string;
  target: BackendReadTarget;
  search: BackendWorkspaceSearch;
  saved?: BackendSavedViewResponse;
  nav: ReturnType<typeof useExplorerNavigation>;
  onNavigate: (search: BackendWorkspaceSearch, replace?: boolean) => void;
  list: boolean;
}) {
  const exact = backendReadTargetKey(target);
  const flow = useQuery({
    queryKey: ["workbench-behavior-flow", projectId, exact, search.entrypointId, search.flowId],
    queryFn: ({ signal }) => readFlowMap(projectId, target, search, signal),
    retry: false,
    staleTime: Infinity,
  });
  const query = useQuery({
    queryKey: [
      "workbench-behavior",
      projectId,
      exact,
      search.entrypointId,
      search.flowId,
      search.wbCalls,
    ],
    enabled: !!flow.data,
    queryFn: ({ signal }) => readBehavior(projectId, target, search, signal, flow.data),
    placeholderData: (previous, previousQuery) =>
      previousQuery?.queryKey[1] === projectId &&
      previousQuery.queryKey[2] === exact &&
      previousQuery.queryKey[3] === search.entrypointId &&
      previousQuery.queryKey[4] === search.flowId
        ? previous
        : undefined,
    retry: false,
    staleTime: Infinity,
  });
  const data = query.data;
  const scope = useMemo(
    () =>
      restoreCatalog(search.wbCatalog) ??
      (saved?.state.kind === "flow"
        ? { wbMode: "flow" as const, wbQuery: search.wbQuery ?? saved.state.filters.search }
        : data
          ? {
              wbMode: "objects" as const,
              wbKind: data.entrypoint.kind,
              wbScope: data.entrypoint.parentId ?? undefined,
            }
          : undefined),
    [search.wbCatalog, search.wbQuery, saved, data],
  );
  const catalog = useQuery<MapData>({
    queryKey: ["workbench-behavior-catalog", projectId, exact, scope],
    enabled: !!scope,
    retry: false,
    staleTime: Infinity,
    queryFn: ({ signal }) =>
      scope!.wbMode === "flow"
        ? readFlowMap(projectId, target, { ...scope, wbFlowPart: "entrypoints" }, signal)
        : readSourceMap(projectId, target, scope!, signal),
  });
  const scene = useMemo(
    () =>
      savedCanvas(data?.scene.nodes ?? [], data?.scene.edges ?? [], saved, search.wbExpandGroups),
    [data, saved, search.wbExpandGroups],
  );
  const selected = search.wbSelection ?? search.recordId;
  const selectedNode = data?.scene.nodes.find((n) => n.id === selected);
  const selectedEdge = data?.scene.edges.find((e) => e.id === selected);
  const opener = useRef<HTMLElement | null>(null);
  const pin = resolvedTargetSearch(target);
  const choose = (node: MapNode) =>
    onNavigate({
      ...pin,
      wbView: "scenarios",
      wbMode: "flow",
      ...(node.kind === "flow"
        ? { flowId: node.id, entrypointId: search.entrypointId }
        : { entrypointId: node.id }),
      wbCatalog: search.wbCatalog ?? JSON.stringify(scope),
      wbFilter: search.wbFilter ?? scope?.wbFilter,
    });
  const select = (id: string, recordType: "node" | "edge" = "node") => {
    opener.current = document.activeElement as HTMLElement;
    onNavigate(
      {
        ...search,
        recordId: /^[0-9a-f-]{36}$/.test(id) ? id : undefined,
        wbSelection: id,
        recordType,
      },
      true,
    );
  };
  const enter = (node: MapNode) => {
    if (data?.representation === "choice" || node.kind === "flow") {
      choose(node);
      return;
    }
    if (data?.representation === "dependencies" && !data.expanded.includes(node.id))
      onNavigate(
        {
          ...search,
          wbCalls: [...new Set([...(search.wbCalls ?? []), node.id])],
          recordId: undefined,
          wbSelection: "",
        },
        true,
      );
    else select(node.id);
  };
  return (
    <>
      <aside className={styles.scenarioRail} aria-label="Каталог сценария">
        {data && (
          <div className={styles.scenarioSummary}>
            <span className={styles.scenarioEyebrow}>
              {data.representation === "sequence"
                ? "Ход сценария"
                : data.representation === "choice"
                  ? "Обработчики"
                  : "Связи по исходникам"}
            </span>
            <h2>{data.entrypoint.name}</h2>
            {purposeText(data.entrypoint) && <p>{purposeText(data.entrypoint)}</p>}
            {triggerLabel(data.entrypoint) && (
              <Text size="xs" c="dimmed">
                {triggerLabel(data.entrypoint)}
              </Text>
            )}
            {data.representation === "dependencies" && <p>{data.scene.partial}</p>}
            {query.isFetching && !query.isPending && (
              <Text component="output" size="xs" c="dimmed">
                Читаем связи…
              </Text>
            )}
            {data.representation === "sequence" && data.scene.partial && (
              <details>
                <summary>Есть неразобранные участки</summary>
                <Text size="xs">{data.scene.partial}</Text>
              </details>
            )}
            {search.entrypointId && (
              <Button
                variant="subtle"
                size="compact-xs"
                onClick={() =>
                  onNavigate({
                    ...search,
                    wbFlowPart: "accesses",
                    recordId: undefined,
                    wbSelection: "",
                  })
                }
              >
                Чтение и запись
              </Button>
            )}
            {search.dataNodeId && (
              <Button
                variant="subtle"
                size="compact-xs"
                onClick={() =>
                  onNavigate({
                    ...search,
                    wbFlowPart: "reverse",
                    recordId: undefined,
                    wbSelection: "",
                  })
                }
              >
                Обращения к данным
              </Button>
            )}
          </div>
        )}
        {catalog.isPending && <Loader size="sm" m="md" aria-label="Загружаем каталог" />}
        {catalog.isError && (
          <Text size="xs" c="red" p="md">
            Каталог недоступен.{" "}
            <Button size="compact-xs" variant="subtle" onClick={() => void catalog.refetch()}>
              Повторить
            </Button>
          </Text>
        )}
        {catalog.data && (
          <>
            <Text size="xs" c="dimmed" px="md">
              {scope?.wbQuery ? `Фильтр вида: ${scope.wbQuery}` : null}
            </Text>
            <SourceCatalog
              compact
              data={catalog.data}
              selected={data?.entrypoint.id}
              onOpen={choose}
              filter={search.wbFilter ?? scope?.wbFilter}
              onFilter={(wbFilter) => onNavigate({ ...search, wbFilter }, true)}
            />
          </>
        )}
      </aside>
      <div className={styles.stage}>
        {flow.isError ? (
          <div className={styles.empty}>
            <Alert color="red" title="Сценарий недоступен">
              Не удалось прочитать эту версию.
              <Button onClick={() => void flow.refetch()} variant="subtle">
                Повторить
              </Button>
            </Alert>
          </div>
        ) : query.isPending ? (
          <div className={styles.empty}>
            <Loader aria-label="Загружаем сценарий" />
          </div>
        ) : query.isError ? (
          <div className={styles.empty}>
            <Alert color="red" title="Сценарий недоступен">
              Не удалось прочитать эту версию.
              <Button onClick={() => void query.refetch()} variant="subtle">
                Повторить
              </Button>
            </Alert>
          </div>
        ) : (
          data && (
            <>
              <h2 className={styles.srOnly}>{data.entrypoint.name}</h2>
              {scene.hidden > 0 && (
                <Button
                  size="compact-xs"
                  variant="subtle"
                  onClick={() => onNavigate({ ...search, wbExpandGroups: true }, true)}
                >
                  Показать скрытые объекты · {scene.hidden}
                </Button>
              )}
              {data.representation === "choice" ? (
                <SourceCatalog data={data.scene} onOpen={choose} />
              ) : list ? (
                <div className={styles.list} aria-label="Объекты сценария">
                  {data.scene.nodes.map((n) => (
                    <button key={n.id} className={styles.listRow} onClick={() => select(n.id)}>
                      {n.name}
                    </button>
                  ))}
                  {data.scene.edges.map((e) => (
                    <button
                      key={e.id}
                      className={styles.listRow}
                      onClick={() => select(e.id, "edge")}
                    >
                      {data.scene.nodes.find((n) => n.id === e.from)?.name} →{" "}
                      {data.scene.nodes.find((n) => n.id === e.to)?.name} · {e.label}
                    </button>
                  ))}
                </div>
              ) : (
                <ExploreCanvas
                  key={`${exact}:${data.entrypoint.id}:${data.flowId}`}
                  nodes={scene.nodes}
                  edges={scene.edges}
                  selection={selected}
                  camera={nav.presentation.camera}
                  startId={data.entrypoint.id}
                  anchorId={
                    data.representation === "dependencies" ? search.wbCalls?.at(-1) : undefined
                  }
                  onCamera={nav.updateCamera}
                  onSelect={select}
                  onEnter={enter}
                />
              )}
            </>
          )
        )}
      </div>
      {selected && data && (
        <Inspector
          projectId={projectId}
          target={target}
          search={search}
          id={selected}
          node={selectedNode}
          edge={selectedEdge}
          onEnter={enter}
          onNavigate={onNavigate}
          canEnter={
            data.representation === "dependencies"
              ? !!selectedNode && !data.expanded.includes(selectedNode.id)
              : undefined
          }
          enterLabel={data.representation === "dependencies" ? "Раскрыть вызовы" : undefined}
          onClose={() => {
            onNavigate(
              { ...search, recordId: undefined, wbSelection: "", recordType: undefined },
              true,
            );
            requestAnimationFrame(() => opener.current?.focus({ preventScroll: true }));
          }}
        />
      )}
    </>
  );
}
