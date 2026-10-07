import { useEffect, useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import { Alert, Button, Loader, Text } from "@mantine/core";
import type { BackendReadTarget } from "@/api/generated/schemas";
import type { BackendWorkspaceSearch } from "../backendWorkspaceSearch";
import { backendReadTargetKey } from "../backendReadTargets";
import { nodeOf } from "./reads";
import { readBackendNode } from "../backendGraphReads";
import { readArchitectureChoices } from "./architectureReads";
import { resolvedTargetSearch } from "./navigation";
import styles from "./Explorer.module.css";

export function ArchitectureScope({
  projectId,
  target,
  scope,
  onNavigate,
}: {
  projectId: string;
  target: BackendReadTarget;
  scope?: string;
  onNavigate: (s: BackendWorkspaceSearch, replace?: boolean) => void;
}) {
  const exact = backendReadTargetKey(target);
  const source = useQuery({
    queryKey: ["architecture-source", projectId, exact, scope],
    retry: false,
    staleTime: Infinity,
    enabled: !!scope,
    queryFn: async ({ signal }) => {
      const value = await readBackendNode(projectId, target, scope!, signal);
      const raw = "node" in value ? value.node : value;
      if (!("kind" in raw) || !("attributes" in raw)) throw new Error("Объект недоступен");
      return nodeOf(raw);
    },
  });
  const choices = useQuery({
    queryKey: ["workbench-catalog", projectId, "architecture-scope", exact, scope],
    enabled: !scope || !!source.data,
    retry: false,
    queryFn: ({ signal }) =>
      readArchitectureChoices(projectId, target, undefined, scope ? [scope] : [], signal),
  });
  const opened = useRef("");
  useEffect(() => {
    const key = `${exact}:${scope}`;
    if (!choices.data || opened.current === key) return;
    opened.current = key;
    if (choices.data.length === 1) onNavigate(choices.data[0]!.search, true);
  }, [choices.data, exact, scope, onNavigate]);
  const missing = !!scope && !!source.data && source.data.id !== scope;
  return (
    <div className={styles.stage}>
      <div className={styles.architectureChooser}>
        <span className={styles.scenarioEyebrow}>Архитектура</span>
        <h2 className={styles.stageTitle}>
          {scope ? (source.data?.name ?? "Выбранная область") : "Схемы проекта"}
        </h2>
        {(!!scope && source.isPending) || (!missing && choices.isPending && !source.isError) ? (
          <Loader size="sm" aria-label="Ищем архитектуру области" mt="lg" />
        ) : source.isError || choices.isError ? (
          <Alert color="red" mt="lg">
            Не удалось прочитать архитектуру этой версии.{" "}
            <Button
              variant="subtle"
              onClick={() => {
                if (source.isError) void source.refetch();
                else void choices.refetch();
              }}
            >
              Повторить
            </Button>
          </Alert>
        ) : missing ? (
          <Text mt="lg">Объект недоступен в выбранной версии.</Text>
        ) : (
          <>
            <Text size="sm" c="dimmed" mt="md">
              {choices.data?.length
                ? "Выберите представление этой области."
                : "Архитектура этой области ещё не описана. Доступны объекты исходников и их связи."}
            </Text>
            <div className={styles.architectureChoices}>
              {choices.data?.map((choice) => (
                <button
                  className={styles.catalogRow}
                  key={choice.key}
                  onClick={() => onNavigate(choice.search)}
                >
                  <span className={styles.catalogIdentity}>
                    <strong>{choice.name}</strong>
                    <span>{choice.description}</span>
                  </span>
                  <span aria-hidden="true">→</span>
                </button>
              ))}
            </div>
            <Button
              variant="subtle"
              mt="md"
              onClick={() =>
                onNavigate({
                  ...resolvedTargetSearch(target),
                  wbView: "structure",
                  wbMode: scope ? "children" : "overview",
                  wbScope: scope,
                })
              }
            >
              Открыть исходники
            </Button>
          </>
        )}
      </div>
    </div>
  );
}
