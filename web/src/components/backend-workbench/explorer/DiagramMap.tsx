import { useMemo, useRef } from "react";
import { Alert, Button, Group, Loader, Text } from "@mantine/core";
import type {
  BackendDiagramDocument,
  BackendDiagramOrigin,
  BackendDiagramRef,
  BackendDiagramRow,
} from "@/api/generated/schemas";
import { useDiagramPage } from "../backendDiagramReads";
import { diagramSearch, type BackendWorkspaceSearch } from "../backendWorkspaceSearch";
import type { Entry } from "./reads";
import type { useExplorerNavigation } from "./navigation";
import { ExploreCanvas } from "./LazyExploreCanvas";
import { Inspector } from "./Inspector";
import type { MapData, MapNode, MapEdge } from "./model";
import { kindName, diagramKindNames, relationNames } from "./model";
import { ArchitectureRail } from "./ArchitectureRail";
import {
  architecturePlace,
  architectureSearch,
  focusArchitecture,
  sourceRefIds,
  explicitArchitectureSearch,
} from "./architectureNavigation";
import { useObservedLayer } from "./observedLayer";
import { ReadOnlyValue } from "./Results";
import styles from "./Explorer.module.css";
import { useCardClicks } from "./cardClicks";

const origin = (o: BackendDiagramOrigin) =>
  o.kind === "authored" ? "Авторское утверждение" : "Исходный код";
function node(
  id: string,
  name: string,
  kind: string,
  o: BackendDiagramOrigin,
  refs: BackendDiagramRef[],
  description = "",
): MapNode {
  return {
    id,
    name,
    kind,
    description,
    origin: origin(o),
    refs,
    parentId: null,
    attributes: { provenance: o },
    childCount: 0,
  };
}
export function mapDiagram(
  doc: BackendDiagramDocument,
  rows?: BackendDiagramRow[],
  rootId?: string,
): MapData {
  let nodes: MapNode[] = [];
  let edges: MapEdge[] = [];
  if (doc.kind === "architecture") {
    for (const r of rows ?? []) {
      if (r.rowType === "architecture_element") {
        const e = r.data;
        nodes.push({
          ...node(e.id, e.label, e.role, e.origin, e.refs, e.responsibility),
          parentId: e.parentId ?? null,
          boundary: e.id === (rootId ?? doc.payload.primarySystemId),
          attributes: { technology: e.technology, provenance: e.origin },
        });
      } else if (r.rowType === "architecture_link") {
        const e = r.data;
        edges.push({
          id: e.id,
          kind: e.relation,
          from: e.from,
          to: e.to,
          label:
            e.label && e.label !== e.relation ? e.label : (relationNames[e.relation] ?? e.relation),
          refs: e.refs,
          origin: origin(e.origin),
        });
      }
    }
  } else if (doc.kind === "business_map") {
    nodes = doc.payload.elements.map((e) =>
      node(e.id, e.label, e.role, e.origin, e.refs, e.responsibility),
    );
    edges = doc.payload.links.map((e) => ({
      id: e.id,
      kind: e.relation,
      from: e.from,
      to: e.to,
      label: e.label || e.relation,
      origin: origin(e.origin),
      refs: e.refs,
    }));
  } else if (doc.kind === "lifecycle") {
    nodes = doc.payload.states.map((e) => ({
      ...node(e.id, e.label, "state", e.origin, e.refs),
      badge: e.initial ? "Начальное" : e.terminal ? "Конечное" : undefined,
    }));
    edges = doc.payload.transitions.map((e) => ({
      id: e.id,
      kind: "transition",
      from: e.from,
      to: e.to,
      label: e.label,
      origin: origin(e.origin),
      refs: [...e.refs, ...e.triggers, ...e.writes, ...e.events],
      details: {
        Условие: e.guard.kind === "opaque" ? e.guard.text : "Без дополнительного условия",
        Триггеры: `${e.triggers.length} явных связей`,
      },
    }));
  } else {
    nodes = doc.payload.steps.map((e) => {
      const branches = e.branchPath.map((id) => doc.payload.branches.find((b) => b.id === id));
      return {
        ...node(
          e.id,
          e.label,
          e.kind,
          e.origin,
          e.refs,
          `${doc.payload.participants.find((p) => p.id === e.from)?.label ?? "Участник"} → ${doc.payload.participants.find((p) => p.id === e.to)?.label ?? "Граница"}`,
        ),
        details: {
          ...(branches.length
            ? {
                Ветвь: branches
                  .map((b) =>
                    b
                      ? `${b.label}${b.guardText ? ` · ${b.guardText}` : ""} (${b.kind})`
                      : "Условие неизвестно",
                  )
                  .join(" → "),
              }
            : {}),
          ...(e.replyTo
            ? {
                "Ответ на":
                  doc.payload.steps.find((s) => s.id === e.replyTo)?.label ??
                  "Связанный шаг недоступен",
              }
            : {}),
        },
      };
    });
    edges = doc.payload.order.map((e) => ({
      id: e.id,
      kind: "order",
      from: e.from,
      to: e.to,
      label: "Заявленный порядок",
      origin: origin(e.origin),
    }));
  }
  return {
    nodes,
    edges,
    total: nodes.length,
    title: diagramKindNames[doc.kind]!,
    metadata:
      doc.kind === "lifecycle"
        ? [{ title: "Правила переходов", value: doc.payload.rules }]
        : doc.kind === "interactions"
          ? [{ title: "Ветвления и условия", value: doc.payload.branches }]
          : [],
    subtitle:
      doc.kind === "interactions"
        ? "Заявленные взаимодействия · порядок и ветвления сохранены"
        : "Источник каждого утверждения доступен в сведениях",
  };
}
export function DiagramMap({
  projectId,
  entry,
  search,
  onNavigate,
  nav,
  list,
}: {
  projectId: string;
  entry: Entry;
  search: BackendWorkspaceSearch;
  onNavigate: (s: BackendWorkspaceSearch, replace?: boolean) => void;
  nav: ReturnType<typeof useExplorerNavigation>;
  list: boolean;
}) {
  const diagram = entry.diagram!;
  const doc = diagram.document;
  const saved = entry.diagramView?.state;
  const observed = useObservedLayer(projectId, diagram, search.wbObservation);
  const architecture = doc.kind === "architecture";
  const place = architecture
    ? architecturePlace(doc, {
        diagramLevel: search.diagramLevel ?? saved?.level,
        diagramRoot: search.diagramRoot ?? saved?.rootId,
        diagramFocus: search.diagramFocus,
      })
    : undefined;
  const systems = architecture
    ? doc.payload.elements.filter((e) => e.role === "software_system")
    : [];
  const root =
    search.diagramRoot ?? saved?.rootId ?? (architecture ? doc.payload.primarySystemId : undefined);
  const level = search.diagramLevel ?? saved?.level ?? "containers";
  const hiddenFocus =
    !!place?.focus &&
    !!saved &&
    ((!!saved.search && !place.focus.label.toLowerCase().includes(saved.search.toLowerCase())) ||
      (saved.origin !== "all" && saved.origin !== place.focus.origin.kind));
  const input = {
    pin: diagram.pin,
    level: architecture ? level : undefined,
    rootId: architecture ? root : undefined,
    search: saved?.search ?? "",
    origin: saved?.origin ?? ("all" as const),
    section: "elements" as const,
    limit: 100,
  };
  const elements = useDiagramPage(
    projectId,
    input,
    architecture && !!root && !place?.error && !hiddenFocus,
    diagram.targetHash,
    true,
  );
  const links = useDiagramPage(
    projectId,
    { ...input, section: "links", cursor: undefined, limit: 300 },
    architecture && !!root && !place?.error && !hiddenFocus && !!elements.data,
    diagram.targetHash,
    true,
  );
  const data = useMemo(() => {
    const value = mapDiagram(
      doc,
      [...(elements.data?.items ?? []), ...(links.data?.items ?? [])],
      root,
    );
    return {
      ...value,
      nodes: value.nodes.map((n) => {
        const p = saved?.positions.find((p) => p.id === n.id);
        return p ? { ...n, x: p.x, y: p.y } : n;
      }),
    };
  }, [doc, elements.data, links.data, saved, root]);
  const presented = useMemo(() => {
    const visible = data.nodes.filter(
      (n) =>
        search.wbExpandGroups ||
        n.id === search.diagramFocus ||
        !saved?.collapsedIds.includes(n.id),
    );
    const ids = new Set(visible.map((n) => n.id));
    return focusArchitecture(
      {
        ...data,
        nodes: visible.map((n) => {
          if (!observed.data) return n;
          const count = observed.data.report.elements.filter((e) =>
            e.selectors.some((s) => s.kind === "semantic" && s.id === n.id),
          ).length;
          return { ...n, badge: count ? `Наблюдалось · ${count}` : "Нет данных наблюдений" };
        }),
        edges: data.edges.filter((e) => ids.has(e.from) && ids.has(e.to)),
      },
      search.diagramFocus,
    );
  }, [data, observed.data, saved, search.wbExpandGroups, search.diagramFocus]);
  const selection =
    search.wbSelection ??
    search.recordId ??
    search.diagramSelection?.split(":")[1] ??
    saved?.selection?.id;
  const selected = data.nodes.find((n) => n.id === selection);
  const selectedEdge = data.edges.find((e) => e.id === selection);
  const destinations = (id: string) =>
    architecture ? (doc.payload.elements.find((e) => e.id === id)?.navigation ?? []) : [];
  const selectedDestinations = selection ? destinations(selection) : [];
  const members = useDiagramPage(
    projectId,
    {
      ...input,
      section: "members",
      search: "",
      origin: "all",
      subjectId: selection ?? "",
      cursor: search.wbMemberCursor,
      limit: 500,
    },
    architecture && !!selection && !place?.error && !!links.data,
    diagram.targetHash,
    true,
  );
  const opener = useRef<HTMLElement | null>(null);
  const select = (id: string, type: "node" | "edge" = "node") => {
    opener.current = document.activeElement as HTMLElement;
    onNavigate(
      {
        ...search,
        recordId: id,
        wbSelection: undefined,
        recordType: type,
        wbMemberCursor: undefined,
      },
      true,
    );
  };
  const enter = (n: MapNode) => {
    const entries = destinations(n.id);
    if (entries.length) {
      if (entries.length === 1) onNavigate(explicitArchitectureSearch(entries[0]!));
      else select(n.id);
      return;
    }
    if (n.kind === "software_system" || n.kind === "application") {
      onNavigate({
        ...diagramSearch(diagram.pin, {
          level: n.kind === "software_system" ? "containers" : "components",
          rootId: n.id,
          selection: null,
        }),
        wbView: "structure",
      });
      return;
    }
    if (architecture && n.kind === "component") {
      onNavigate({
        ...search,
        diagramFocus: n.id,
        recordId: undefined,
        wbSelection: "",
        diagramSelection: undefined,
        wbMemberCursor: undefined,
      });
      return;
    }
    const refs = sourceRefIds(n.refs);
    if (refs.length === 1)
      onNavigate({
        ...("revisionId" in entry.target
          ? { revisionId: entry.target.revisionId ?? undefined }
          : {
              changeProposalId: entry.target.changeProposal?.proposalId,
              proposalRevisionId: entry.target.changeProposal?.proposalRevisionId,
            }),
        wbMode: "neighborhood",
        wbScope: refs[0],
        recordId: refs[0],
      });
    else select(n.id);
  };
  const clicks = useCardClicks(
    select,
    (id) => {
      const node = data.nodes.find((n) => n.id === id);
      if (node) enter(node);
    },
    data,
  );
  if (place?.error)
    return (
      <div className={styles.empty}>
        <Alert color="red">{place.error}</Alert>
        <Button
          variant="subtle"
          mt="md"
          onClick={() =>
            onNavigate(
              architectureSearch(
                diagram.pin,
                "containers",
                doc.kind === "architecture" ? doc.payload.primarySystemId : "",
              ),
            )
          }
        >
          К системе
        </Button>
      </div>
    );
  if (architecture && !root)
    return (
      <div className={styles.resultPanel}>
        <h2>Выберите систему</h2>
        {systems.map((s) => (
          <Button
            key={s.id}
            variant="subtle"
            onClick={() =>
              onNavigate({
                ...diagramSearch(diagram.pin, {
                  level: "containers",
                  rootId: s.id,
                  selection: null,
                }),
                wbView: "structure",
              })
            }
          >
            {s.label}
          </Button>
        ))}
      </div>
    );
  const controls = (
    <>
      {entry.diagramView && (
        <Button
          component="a"
          href={`/api/backend-projects/${projectId}/diagram-views/${entry.diagramView.id}/versions/${entry.diagramView.version}/svg`}
          download
          size="compact-xs"
          variant="subtle"
        >
          Скачать SVG
        </Button>
      )}
    </>
  );
  return (
    <>
      {architecture && (
        <ArchitectureRail
          projectId={projectId}
          diagram={diagram}
          search={search}
          saved={saved}
          onNavigate={onNavigate}
        />
      )}
      <div className={styles.stage}>
        <h2 className={styles.srOnly}>
          {architecture
            ? (systems.find((s) => s.id === root)?.label ?? "Структура приложения")
            : data.title}
        </h2>
        {elements.data?.cache?.status === "not_retained" && (
          <Alert color="yellow" mx="xl" my="xs">
            Эта карта превышает лимит кэша ({Math.round(elements.data.cache.limitBytes / 1048576)}{" "}
            MiB). Повторное чтение потребует построить её заново.
          </Alert>
        )}
        {saved && (saved.search || saved.origin !== "all") && (
          <Alert color="blue" mx="xl" my="xs" title="Активные фильтры сохранённого вида">
            {[
              saved.search && `Поиск: «${saved.search}»`,
              saved.origin !== "all" &&
                `Основание: ${saved.origin === "authored" ? "авторское" : "исходный код"}`,
            ]
              .filter(Boolean)
              .join(" · ")}
            . Фильтр может скрывать концы связей.
            <Button
              size="compact-xs"
              variant="subtle"
              onClick={() =>
                onNavigate({
                  ...diagramSearch(diagram.pin),
                  wbView: search.wbView,
                  diagramLevel: level,
                  diagramRoot: root,
                })
              }
            >
              Открыть без фильтров
            </Button>
          </Alert>
        )}
        {!!saved?.collapsedIds.length && (
          <Button
            variant="subtle"
            size="compact-xs"
            style={{ alignSelf: "start", marginLeft: 24 }}
            onClick={() => onNavigate({ ...search, wbExpandGroups: !search.wbExpandGroups }, true)}
          >
            {search.wbExpandGroups ? "Вернуть свёрнутый вид" : "Показать скрытые объекты"}
          </Button>
        )}
        {observed.isError && (
          <Alert color="red" className={styles.notice}>
            Выбранные наблюдения недоступны для этой точной схемы.
          </Alert>
        )}
        {observed.data && (
          <Alert color="blue" className={styles.notice}>
            <Group justify="space-between">
              <Text size="sm">
                Наблюдения: {observed.data.name}. Неотмеченные пути остаются непроверенными.
              </Text>
              <Button
                size="compact-xs"
                variant="subtle"
                onClick={() => onNavigate({ ...search, wbObservation: undefined })}
              >
                Снять слой
              </Button>
            </Group>
            <details className={styles.diagramDetails}>
              <summary>Среды и периоды наблюдений</summary>
              <ReadOnlyValue value={observed.data.context} />
            </details>
          </Alert>
        )}
        {doc.kind === "interactions" && (
          <Group px="xl" py="xs">
            <Text size="xs" c="dimmed">
              По заявленным шагам
            </Text>
            {doc.payload.steps
              .filter((step) =>
                selection
                  ? doc.payload.order.some((o) => o.from === selection && o.to === step.id)
                  : !doc.payload.order.some((o) => o.to === step.id),
              )
              .map((step) => (
                <Button
                  key={step.id}
                  size="compact-xs"
                  variant="light"
                  onClick={() => select(step.id)}
                >
                  {selection ? "Далее: " : ""}
                  {step.label}
                </Button>
              ))}
          </Group>
        )}
        {list &&
          data.metadata
            ?.filter((m) => Array.isArray(m.value) && m.value.length > 0)
            .map((m) => (
              <details key={m.title} className={styles.diagramDetails}>
                <summary>{m.title}</summary>
                <ReadOnlyValue value={m.value} />
              </details>
            ))}
        {list && diagram.gaps.length > 0 && (
          <details className={styles.diagramDetails}>
            <summary>Ограничения модели · {diagram.gaps.length}</summary>
            <ReadOnlyValue value={diagram.gaps} />
          </details>
        )}
        {hiddenFocus ? (
          <div className={styles.empty}>
            <Text size="sm">Компонент скрыт фильтром сохранённого вида.</Text>
            <Button
              variant="light"
              mt="md"
              onClick={() =>
                onNavigate(
                  architectureSearch(diagram.pin, "components", root!, search.diagramFocus),
                )
              }
            >
              Открыть компонент без фильтра
            </Button>
          </div>
        ) : elements.isError || links.isError ? (
          <Alert color="red" className={styles.notice}>
            Не удалось открыть схему.
            <Button
              variant="subtle"
              onClick={() => {
                if (elements.isError) void elements.refetch();
                else void links.refetch();
              }}
            >
              Повторить
            </Button>
          </Alert>
        ) : architecture && (!elements.data || !links.data) ? (
          <div className={styles.empty}>
            <Loader />
          </div>
        ) : list ? (
          <div className={styles.list}>
            <Group justify="space-between" mb="sm">
              <Text size="xs" c="dimmed">
                {data.nodes.length} объектов · {data.subtitle}
              </Text>
              {controls}
            </Group>
            {presented.nodes.map((n) => (
              <button
                className={styles.listRow}
                key={n.id}
                onClick={(e) => clicks.click(n.id, e.detail)}
                onDoubleClick={() => clicks.doubleClick(n.id)}
                aria-pressed={selection === n.id}
              >
                {n.name}
                <Text size="xs">{kindName(n.kind)}</Text>
              </button>
            ))}
            {presented.edges.map((e) => (
              <button
                key={e.id}
                className={styles.listRow}
                onClick={() => {
                  clicks.cancel();
                  select(e.id, "edge");
                }}
              >
                {e.label}
              </button>
            ))}
          </div>
        ) : (
          <ExploreCanvas
            key={JSON.stringify([
              diagram.pin,
              root,
              level,
              search.diagramFocus,
              entry.diagramView?.id,
              entry.diagramView?.version,
            ])}
            nodes={presented.nodes}
            edges={presented.edges}
            selection={selection}
            camera={nav.presentation.camera}
            startId={search.diagramFocus}
            onCamera={nav.updateCamera}
            onSelect={select}
            onEnter={enter}
            controls={controls}
          />
        )}
      </div>
      {selection && (
        <Inspector
          id={selection}
          projectId={projectId}
          target={entry.target}
          targetHash={diagram.targetHash}
          extraRefs={members.data?.items.flatMap((row) =>
            row.rowType === "member" ? [row.data.ref] : [],
          )}
          referencesStatus={
            architecture &&
            (members.isFetching ? (
              <Text size="xs" component="output">
                Загружаем состав связей…
              </Text>
            ) : members.isError ? (
              <Alert color="red">
                Не удалось загрузить состав связей.{" "}
                <Button variant="subtle" size="compact-xs" onClick={() => void members.refetch()}>
                  Повторить
                </Button>
              </Alert>
            ) : undefined)
          }
          search={search}
          node={selected}
          edge={selectedEdge}
          onNavigate={onNavigate}
          onEnter={enter}
          canEnter={selectedDestinations.length ? false : undefined}
          navigationActions={selectedDestinations.map((destination, index) => (
            <Button
              key={index}
              variant="light"
              onClick={() => onNavigate(explicitArchitectureSearch(destination))}
            >
              {destination.label}
            </Button>
          ))}
          enterLabel={
            architecture && selected?.kind === "component" ? "Компонент и его связи" : undefined
          }
          onClose={() => {
            onNavigate(
              {
                ...search,
                recordId: undefined,
                wbSelection: "",
                diagramSelection: undefined,
                wbMemberCursor: undefined,
              },
              true,
            );
            requestAnimationFrame(() => opener.current?.focus({ preventScroll: true }));
          }}
        />
      )}
    </>
  );
}
