import { useQuery } from "@tanstack/react-query";
import { Button, Loader, Text } from "@mantine/core";
import type { BackendDiagramVersion, BackendDiagramViewState } from "@/api/generated/schemas";
import type { BackendWorkspaceSearch } from "../backendWorkspaceSearch";
import {
  architecturePlace,
  architectureSearch,
  architectureSourceSearch,
  architectureMemberIds,
} from "./architectureNavigation";
import { readArchitectureChoices } from "./architectureReads";
import { readExploreNodes } from "./reads";
import { kindName, sourceNode } from "./model";
import styles from "./Explorer.module.css";

export function ArchitectureRail({
  projectId,
  diagram,
  search,
  saved,
  onNavigate,
}: {
  projectId: string;
  diagram: BackendDiagramVersion;
  search: BackendWorkspaceSearch;
  saved?: BackendDiagramViewState;
  onNavigate: (s: BackendWorkspaceSearch, replace?: boolean) => void;
}) {
  const doc = diagram.document;
  if (doc.kind !== "architecture") return null;
  return (
    <ArchitectureTree
      projectId={projectId}
      diagram={diagram}
      doc={doc}
      search={search}
      saved={saved}
      onNavigate={onNavigate}
    />
  );
}
function ArchitectureTree({
  projectId,
  diagram,
  doc,
  search,
  saved,
  onNavigate,
}: Parameters<typeof ArchitectureRail>[0] & {
  doc: Extract<BackendDiagramVersion["document"], { kind: "architecture" }>;
}) {
  const place = architecturePlace(doc, search, saved);
  const systems = doc.payload.elements.filter((e) => e.role === "software_system");
  const apps = doc.payload.elements.filter(
    (e) => e.role === "application" && e.parentId === place.systemId,
  );
  const allComponents = doc.payload.elements.filter(
    (e) => e.role === "component" && e.parentId === place.rootId,
  );
  const components = allComponents.filter(
    (e) =>
      (!saved?.search || e.label.toLocaleLowerCase().includes(saved.search.toLocaleLowerCase())) &&
      (!saved?.origin || saved.origin === "all" || saved.origin === e.origin.kind),
  );
  const focus = place.focus;
  const ids = architectureMemberIds(focus);
  const related = useQuery({
    queryKey: [
      "workbench-catalog",
      projectId,
      "architecture-related",
      diagram.targetHash,
      diagram.pin.id,
      ids,
    ],
    enabled: !!focus && ids.length > 0,
    retry: false,
    queryFn: ({ signal }) =>
      readArchitectureChoices(
        projectId,
        doc.target,
        diagram.targetHash,
        ids,
        signal,
        diagram.pin.id,
      ),
  });
  const sources = useQuery({
    queryKey: ["architecture-refs", projectId, diagram.targetHash, ids],
    enabled: !!focus && ids.length > 0,
    retry: false,
    staleTime: Infinity,
    queryFn: ({ signal }) => readExploreNodes(projectId, doc.target, ids, signal),
  });
  const focusComponent = (id?: string) =>
    onNavigate({
      ...search,
      diagramFocus: id,
      recordId: undefined,
      wbSelection: "",
      diagramSelection: undefined,
      wbMemberCursor: undefined,
    });
  return (
    <aside className={styles.architectureRail} aria-label="Навигация по архитектуре">
      <nav className={styles.architectureLevels} aria-label="Уровни архитектуры">
        <span className={styles.scenarioEyebrow}>Архитектура</span>
        <button
          className={styles.architectureLevel}
          aria-current={place.level === "context" ? "page" : undefined}
          onClick={() => onNavigate(architectureSearch(diagram.pin, "context", place.systemId))}
        >
          Окружение
        </button>
        {systems.map((system) => (
          <div key={system.id}>
            <button
              className={styles.architectureLevel}
              aria-current={
                place.level === "containers" && place.rootId === system.id ? "page" : undefined
              }
              onClick={() => onNavigate(architectureSearch(diagram.pin, "containers", system.id))}
            >
              <strong>{system.label}</strong>
              <small>Приложения и данные</small>
            </button>
            {system.id === place.systemId &&
              apps.map((app) => (
                <button
                  key={app.id}
                  className={`${styles.architectureLevel} ${styles.architectureChild}`}
                  aria-current={
                    place.level === "components" && place.rootId === app.id && !focus
                      ? "page"
                      : undefined
                  }
                  onClick={() =>
                    place.rootId === app.id && place.level === "components"
                      ? focusComponent()
                      : onNavigate(architectureSearch(diagram.pin, "components", app.id))
                  }
                >
                  <strong>{app.label}</strong>
                  <small>
                    Компоненты ·{" "}
                    {
                      doc.payload.elements.filter(
                        (e) => e.role === "component" && e.parentId === app.id,
                      ).length
                    }
                  </small>
                </button>
              ))}
          </div>
        ))}
      </nav>
      {place.level === "components" && !focus && (
        <section className={styles.architectureSection} aria-label="Компоненты приложения">
          <Text size="xs" fw={600} mb="xs">
            Компоненты · {components.length}
          </Text>
          {components.length === 0 && (
            <Text size="xs" c="dimmed">
              {allComponents.length
                ? "Компоненты скрыты фильтром сохранённого вида."
                : "Компоненты приложения ещё не описаны."}
            </Text>
          )}
          {components.map((component) => (
            <button
              key={component.id}
              className={styles.architectureLevel}
              aria-current={search.diagramFocus === component.id ? "page" : undefined}
              onClick={() => focusComponent(component.id)}
            >
              {component.label}
            </button>
          ))}
        </section>
      )}
      {focus && (
        <section className={styles.architectureSection} aria-label="Выбранный компонент">
          <span className={styles.scenarioEyebrow}>Компонент и соседи</span>
          <h2>{focus.label}</h2>
          {focus.responsibility && (
            <Text size="xs" c="dimmed">
              {focus.responsibility}
            </Text>
          )}
          <Button size="compact-xs" variant="subtle" mt="sm" onClick={() => focusComponent()}>
            Все компоненты
          </Button>
          <h3>Связанные схемы</h3>
          {related.isFetching && <Loader size="xs" aria-label="Ищем связанные схемы" />}
          {related.isError && (
            <Text size="xs" c="red">
              Схемы недоступны.{" "}
              <Button variant="subtle" size="compact-xs" onClick={() => void related.refetch()}>
                Повторить
              </Button>
            </Text>
          )}
          {related.data?.map((choice) => (
            <button
              key={choice.key}
              className={styles.architectureLevel}
              onClick={() => onNavigate(choice.search)}
            >
              {choice.name}
              <span aria-hidden="true"> →</span>
            </button>
          ))}
          {(!ids.length || related.data?.length === 0) && (
            <Text size="xs" c="dimmed">
              Дополнительные схемы этой области пока не связаны.
            </Text>
          )}
          <h3>Код и сценарии</h3>
          {sources.isFetching && <Loader size="xs" aria-label="Читаем объекты компонента" />}
          {sources.isError && (
            <Text size="xs" c="red">
              Объекты недоступны.{" "}
              <Button variant="subtle" size="compact-xs" onClick={() => void sources.refetch()}>
                Повторить
              </Button>
            </Text>
          )}
          {sources.data?.map((n) => (
            <button
              key={n.id}
              className={styles.architectureLevel}
              onClick={() => onNavigate(architectureSourceSearch(doc.target, sourceNode(n)))}
            >
              <strong>{n.name}</strong>
              <small>{kindName(n.kind)}</small>
            </button>
          ))}
          {!ids.length && (
            <Text size="xs" c="dimmed">
              Связи с исходниками ещё не указаны.
            </Text>
          )}
        </section>
      )}
    </aside>
  );
}
