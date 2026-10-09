import {
  listBackendDiagrams,
  listBackendDiagramViews,
  getBackendDiagramView,
} from "@/api/generated/backend-projects/backend-projects";
import type {
  BackendReadTarget,
  BackendDiagramVersion,
  BackendDiagramView,
} from "@/api/generated/schemas";
import { diagramSearch } from "../backendWorkspaceSearch";
import { diagramKindNames } from "./model";
import { readDiagram } from "../backendDiagramReads";
import { backendReadTargetKey } from "../backendReadTargets";
import { readPages, uniqueBy } from "./readPages";
import { ok } from "./catalogs";
import {
  architectureDestinations,
  architecturePlace,
  architectureLevelNames,
  explicitArchitectureSearch,
  type ArchitectureDestination,
} from "./architectureNavigation";

export type ArchitectureViewName = Pick<BackendDiagramView, "id" | "version" | "name"> & {
  state: Pick<BackendDiagramView["state"], "diagram" | "level" | "rootId">;
};
export type NamedArchitectureDestination = ArchitectureDestination & {
  diagramId: string;
  nested?: boolean;
};

export async function readArchitectureChoices(
  projectId: string,
  target: BackendReadTarget,
  targetHash: string | undefined,
  subjects: string[],
  signal: AbortSignal,
  excludeDiagramId?: string,
  readView?: (id: string, version: number) => Promise<ArchitectureViewName>,
): Promise<NamedArchitectureDestination[]> {
  const ids = [...new Set(subjects)];
  // A component can map many records. Resolve their union from the same catalog
  // instead of issuing one catalog request per function.
  const pages = await readPages(
    async (cursor) => {
      const page = ok(
        await listBackendDiagrams(
          projectId,
          {
            targetHash,
            subjectId: ids.length === 1 ? ids[0] : undefined,
            limit: 100,
            cursor,
          },
          { signal },
        ),
      );
      if (
        page.items.some(
          (item) =>
            (targetHash !== undefined && item.targetHash !== targetHash) || item.id !== item.pin.id,
        )
      )
        throw new Error("Получены схемы другой версии модели.");
      return page;
    },
    signal,
    (page) => String(page.catalogVersion),
  );
  const items = pages.flatMap((p) => p.items);
  const choices: NamedArchitectureDestination[] = [];
  const documents = new Map<string, BackendDiagramVersion>();
  const pinKey = (pin: BackendDiagramVersion["pin"]) =>
    `${pin.id}:${pin.version}:${pin.contentHash}`;
  for (const item of uniqueBy(
    items,
    (item) => `${item.pin.id}:${item.pin.version}:${item.pin.contentHash}`,
  )) {
    if (item.id === excludeDiagramId) continue;
    if (item.target && backendReadTargetKey(item.target) !== backendReadTargetKey(target)) continue;
    const diagram = await readDiagram(projectId, item.pin, signal);
    if (
      targetHash === undefined &&
      backendReadTargetKey(diagram.document.target) !== backendReadTargetKey(target)
    )
      continue;
    if (
      (targetHash !== undefined && diagram.targetHash !== targetHash) ||
      backendReadTargetKey(diagram.document.target) !== backendReadTargetKey(target)
    )
      throw new Error("Схема относится к другому источнику.");
    documents.set(pinKey(diagram.pin), diagram);
    if (diagram.document.kind !== "architecture") {
      const companion = diagram.document;
      const refs =
        companion.kind === "interactions"
          ? [
              ...companion.payload.scopeRefs,
              ...companion.payload.participants.flatMap((e) => e.refs),
              ...companion.payload.steps.flatMap((e) => e.refs),
            ]
          : companion.kind === "business_map"
            ? [
                ...companion.payload.elements.flatMap((e) => e.refs),
                ...companion.payload.links.flatMap((e) => e.refs),
              ]
            : [
                companion.payload.entity,
                ...companion.payload.stateFields,
                ...companion.payload.states.flatMap((e) => e.refs),
                ...companion.payload.transitions.flatMap((e) => [
                  ...e.refs,
                  ...e.triggers,
                  ...e.writes,
                  ...e.events,
                ]),
                ...companion.payload.rules.map((e) => e.trigger),
              ];
      if (
        ids.length > 1 &&
        !refs.some(
          (ref) => ref.kind === "record" && ref.recordType === "node" && ids.includes(ref.id),
        )
      ) {
        documents.delete(pinKey(diagram.pin));
        continue;
      }
      choices.push({
        key: pinKey(diagram.pin),
        diagramId: diagram.pin.id,
        name: item.name ?? diagramKindNames[diagram.document.kind] ?? diagram.document.kind,
        description: diagramKindNames[diagram.document.kind] ?? diagram.document.kind,
        search: {
          ...diagramSearch(diagram.pin),
          wbView: diagram.document.kind === "lifecycle" ? "data" : "scenarios",
        },
      });
      continue;
    }
    const components =
      diagram.document.kind === "architecture"
        ? diagram.document.payload.elements.filter((e) => e.role === "component").length
        : 0;
    for (const choice of architectureDestinations(diagram, ids))
      choices.push({
        ...choice,
        diagramId: diagram.pin.id,
        description: `${choice.description}${choice.description ? " · " : ""}Компонентов: ${components}`,
      });
  }
  // Saved views carry the author's meaningful scope names; diagram catalog
  // labels often repeat the same primary system for every domain map.
  const named: NamedArchitectureDestination[] = [];
  const covered = new Set<string>();
  if (documents.size) {
    const viewPages = await readPages(
      async (cursor) =>
        ok(await listBackendDiagramViews(projectId, { limit: 100, cursor }, { signal })),
      signal,
      (p) => String(p.catalogVersion),
    );
    for (const item of viewPages.flatMap((p) => p.items)) {
      signal.throwIfAborted();
      const view = readView
        ? await readView(item.id, item.version)
        : ok(await getBackendDiagramView(projectId, item.id, item.version, { signal }));
      signal.throwIfAborted();
      if (view.id !== item.id || view.version !== item.version)
        throw new Error("Получен другой сохранённый вид.");
      const diagram = documents.get(pinKey(view.state.diagram));
      if (!diagram) continue;
      if (diagram.document.kind !== "architecture") {
        named.push({
          diagramId: diagram.pin.id,
          key: `view:${view.id}:${view.version}`,
          name: view.name,
          description: diagramKindNames[diagram.document.kind] ?? diagram.document.kind,
          search: {
            diagramViewId: view.id,
            diagramViewVersion: view.version,
            wbView: diagram.document.kind === "lifecycle" ? "data" : "scenarios",
          },
        });
        covered.add(pinKey(diagram.pin));
        continue;
      }
      if (!view.state.level || !view.state.rootId) continue;
      const matching = choices.filter(
        (c) =>
          c.search.diagramId === diagram.pin.id &&
          c.search.diagramVersion === diagram.pin.version &&
          c.search.diagramHash === diagram.pin.contentHash &&
          c.search.diagramLevel === view.state.level &&
          c.search.diagramRoot === view.state.rootId,
      );
      if (ids.length && !matching.length) continue;
      const elements =
        diagram.document.kind === "architecture" ? diagram.document.payload.elements : [];
      const contents = elements.filter((element) => element.parentId === view.state.rootId);
      const description = [
        architectureLevelNames[view.state.level],
        `Объектов: ${contents.length}`,
        contents
          .slice(0, 3)
          .map((element) => element.label)
          .join(", "),
      ]
        .filter(Boolean)
        .join(" · ");
      named.push({
        diagramId: diagram.pin.id,
        key: `view:${view.id}:${view.version}`,
        name: view.name,
        description,
        search: { diagramViewId: view.id, diagramViewVersion: view.version, wbView: "structure" },
      });
      covered.add(pinKey(diagram.pin));
    }
  }
  // Internal drill-down maps are destinations, not independent project entry
  // points. Their authored incoming links identify both scope and exact level.
  // Never borrow a label from another version or redirect an immutable pin.
  const nested = new Map<string, NamedArchitectureDestination[]>();
  if (!ids.length) {
    for (const source of documents.values()) {
      if (source.document.kind !== "architecture") continue;
      for (const element of source.document.payload.elements) {
        for (const link of element.navigation ?? []) {
          if (link.kind !== "diagram" || link.diagram.id === source.pin.id) continue;
          const destination = documents.get(pinKey(link.diagram));
          if (!destination || destination.document.kind !== "architecture") continue;
          const search = explicitArchitectureSearch(link);
          if (architecturePlace(destination.document, search).error) continue;
          const key = pinKey(link.diagram);
          const components = destination.document.payload.elements.filter(
            (e) => e.role === "component" && e.parentId === link.rootId,
          ).length;
          const entry: NamedArchitectureDestination = {
            key: `${key}:${link.level}:${link.rootId}:${link.focusId ?? ""}`,
            diagramId: link.diagram.id,
            name: `${element.label} — ${link.label}`,
            description: `${architectureLevelNames[link.level]}${link.level === "components" ? ` · Компонентов: ${components}` : ""}`,
            search,
            nested: true,
          };
          nested.set(key, [...(nested.get(key) ?? []), entry]);
        }
      }
    }
  }
  signal.throwIfAborted();
  return uniqueBy(
    [
      ...named,
      ...choices.flatMap((c) => {
        const key = `${c.search.diagramId}:${c.search.diagramVersion}:${c.search.diagramHash}`;
        if (!c.search.diagramFocus && covered.has(key)) return [];
        return nested.get(key) ?? [c];
      }),
    ],
    (c) => c.key,
  );
}
