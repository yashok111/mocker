import { useQuery } from "@tanstack/react-query";
import { Button, Group, Loader, Text } from "@mantine/core";
import type { BackendReadTarget } from "@/api/generated/schemas";
import type { BackendWorkspaceSearch } from "../backendWorkspaceSearch";
import { backendReadTargetKey } from "../backendReadTargets";
import { readSourceMap } from "./reads";
import { SourceCatalog, catalogScope } from "./SourceCatalog";
import { ViewChooser } from "./ViewChooser";
import { resolvedTargetSearch } from "./navigation";
import styles from "./Explorer.module.css";

export function ScenarioStart({
  projectId,
  target,
  search,
  onNavigate,
}: {
  projectId: string;
  target: BackendReadTarget;
  search: BackendWorkspaceSearch;
  onNavigate: (s: BackendWorkspaceSearch, replace?: boolean) => void;
}) {
  const kind = search.wbKind ?? "http_operation";
  const scope: BackendWorkspaceSearch = {
    wbMode: "objects",
    wbKind: kind,
    wbScope: search.wbScope,
  };
  const query = useQuery({
    queryKey: ["workbench-scenario-start", projectId, backendReadTargetKey(target), scope],
    enabled: kind !== "saved",
    retry: false,
    staleTime: Infinity,
    queryFn: ({ signal }) => readSourceMap(projectId, target, scope, signal),
  });
  return (
    <div className={styles.stage}>
      <Group className={styles.scenarioKinds} gap="xs">
        {[
          ["http_operation", "API-операции"],
          ["job", "Фоновые задачи"],
          ["saved", "Сохранённые схемы"],
        ].map(([value, label]) => (
          <Button
            key={value}
            size="compact-sm"
            variant={kind === value ? "light" : "subtle"}
            onClick={() => onNavigate({ ...search, wbKind: value, wbFilter: undefined }, true)}
          >
            {label}
          </Button>
        ))}
      </Group>
      {kind === "saved" ? (
        <ViewChooser
          projectId={projectId}
          target={target}
          view="scenarios"
          scope={search.wbScope}
          onNavigate={onNavigate}
        />
      ) : query.isPending ? (
        <div className={styles.empty}>
          <Loader aria-label="Загружаем точки входа" />
        </div>
      ) : query.isError ? (
        <div className={styles.empty}>
          <Text>Не удалось загрузить каталог этой версии.</Text>
          <Button onClick={() => void query.refetch()}>Повторить</Button>
        </div>
      ) : (
        query.data && (
          <SourceCatalog
            data={query.data}
            filter={search.wbFilter}
            onFilter={(wbFilter) => onNavigate({ ...search, wbFilter }, true)}
            onOpen={(node) =>
              onNavigate({
                ...resolvedTargetSearch(target),
                wbView: "scenarios",
                wbMode: "flow",
                entrypointId: node.id,
                wbCatalog: catalogScope({ ...scope, wbFilter: search.wbFilter }),
              })
            }
          />
        )
      )}
      {query.data?.nodes.length === 0 && search.wbScope && (
        <Button
          variant="subtle"
          onClick={() => onNavigate({ ...search, wbScope: undefined }, true)}
        >
          Показать весь проект
        </Button>
      )}
    </div>
  );
}
