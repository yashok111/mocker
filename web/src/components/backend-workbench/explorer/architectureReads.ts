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
import { readDiagram } from "../backendDiagramReads";
import { backendReadTargetKey } from "../backendReadTargets";
import { readPages, uniqueBy } from "./readPages";
import { ok } from "./catalogs";
import { architectureDestinations, type ArchitectureDestination } from "./architectureNavigation";

export type ArchitectureViewName = Pick<BackendDiagramView, "id" | "version" | "name"> & {
  state: Pick<BackendDiagramView["state"], "diagram" | "level" | "rootId">;
};
type NamedArchitectureDestination = ArchitectureDestination & { diagramId: string };

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
            kind: "architecture",
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
            (targetHash !== undefined && item.targetHash !== targetHash) ||
            item.id !== item.pin.id ||
            item.kind !== "architecture",
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
        ok(
          await listBackendDiagramViews(
            projectId,
            { kind: "architecture", limit: 100, cursor },
            { signal },
          ),
        ),
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
      if (!diagram || !view.state.level || !view.state.rootId) continue;
      const matching = choices.filter(
        (c) =>
          c.search.diagramId === diagram.pin.id &&
          c.search.diagramVersion === diagram.pin.version &&
          c.search.diagramHash === diagram.pin.contentHash &&
          c.search.diagramLevel === view.state.level &&
          c.search.diagramRoot === view.state.rootId,
      );
      if (ids.length && !matching.length) continue;
      named.push({
        diagramId: diagram.pin.id,
        key: `view:${view.id}:${view.version}`,
        name: view.name,
        description: matching[0]?.description ?? "Сохранённое представление архитектуры",
        search: { diagramViewId: view.id, diagramViewVersion: view.version, wbView: "structure" },
      });
      covered.add(pinKey(diagram.pin));
    }
  }
  signal.throwIfAborted();
  return uniqueBy(
    [
      ...named,
      ...choices.filter(
        (c) =>
          c.search.diagramFocus ||
          !covered.has(`${c.search.diagramId}:${c.search.diagramVersion}:${c.search.diagramHash}`),
      ),
    ],
    (c) => c.key,
  );
}
