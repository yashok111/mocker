import { useQuery } from "@tanstack/react-query";
import { Button, Group, Loader, Text } from "@mantine/core";
import { listBackendDiagrams } from "@/api/generated/backend-projects/backend-projects";
import type { BackendReadTarget, ListBackendDiagramsParams } from "@/api/generated/schemas";
import type { BackendWorkspaceSearch } from "../backendWorkspaceSearch";
import { diagramSearch } from "../backendWorkspaceSearch";
import { backendReadTargetKey } from "../backendReadTargets";
import { readExplore } from "./reads";
import { ok } from "./catalogs";
import { diagramKindNames } from "./model";
import type { View } from "./navigation";
import styles from "./Explorer.module.css";
export function ViewChooser({
  projectId,
  target,
  scope,
  view,
  onNavigate,
}: {
  projectId: string;
  target: BackendReadTarget;
  scope?: string;
  view: View;
  onNavigate: (s: BackendWorkspaceSearch) => void;
}) {
  const query = useQuery({
    queryKey: [
      "workbench-catalog",
      projectId,
      "view-chooser",
      backendReadTargetKey(target),
      scope,
      view,
    ],
    retry: false,
    queryFn: async ({ signal }) => {
      const overview = await readExplore(
        projectId,
        { target, mode: "objects", ...(scope ? { nodeIds: [scope] } : {}), limit: 1 },
        signal,
      );
      if (scope && overview.nodes.length === 0) return [];
      const kinds: NonNullable<ListBackendDiagramsParams["kind"]>[] =
        view === "data"
          ? ["lifecycle"]
          : view === "scenarios"
            ? ["interactions", "business_map"]
            : ["architecture"];
      const pages = await Promise.all(
        kinds.map((kind) =>
          listBackendDiagrams(
            projectId,
            { kind, targetHash: overview.targetHash, subjectId: scope, limit: 20, order: "desc" },
            { signal },
          ).then(ok),
        ),
      );
      return pages.flatMap((p) => p.items);
    },
  });
  return (
    <div className={styles.empty}>
      {query.isPending ? (
        <Loader aria-label="Ищем связанные схемы" />
      ) : (
        <>
          <h2 className={styles.stageTitle}>
            {query.data?.length
              ? "Выберите схему"
              : view === "scenarios"
                ? "Выберите сценарий или операцию"
                : "Выберите область данных"}
          </h2>
          <Text size="sm" c="dimmed" mt="sm">
            {scope
              ? "Схемы, явно связанные с этой областью выбранной версии."
              : "Сохранённые схемы выбранной версии проекта."}
          </Text>
          {query.isError && (
            <Text size="sm" c="red">
              Не удалось прочитать схемы этой области.
            </Text>
          )}
          <Group mt="lg" justify="center">
            {query.data?.map((item) => (
              <Button
                key={item.id}
                variant="light"
                onClick={() => onNavigate({ ...diagramSearch(item.pin), wbView: view })}
              >
                {item.name ?? diagramKindNames[item.kind]} · {diagramKindNames[item.kind]}
              </Button>
            ))}
          </Group>
          {query.data?.length === 0 && (
            <Text size="sm" c="dimmed" mt="md">
              Связанный вид ещё не размечен. Можно явно перейти к обзору проекта.
            </Text>
          )}
        </>
      )}
    </div>
  );
}
