import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Alert, Button, Loader, Text, TextInput } from "@mantine/core";
import type { BackendReadTarget } from "@/api/generated/schemas";
import type { BackendWorkspaceSearch } from "../backendWorkspaceSearch";
import { backendReadTargetKey } from "../backendReadTargets";
import { nodeOf } from "./reads";
import { readBackendNode } from "../backendGraphReads";
import { readArchitectureChoices, type NamedArchitectureDestination } from "./architectureReads";
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
  const [filter, setFilter] = useState("");
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
            {!!choices.data?.length && (
              <TextInput
                label="Поиск схем"
                placeholder="Название области или содержимое"
                value={filter}
                onChange={(event) => setFilter(event.currentTarget.value)}
                mt="md"
              />
            )}
            <ArchitectureChoices
              choices={choices.data ?? []}
              filter={filter}
              onNavigate={onNavigate}
            />
          </>
        )}
      </div>
    </div>
  );
}

function ArchitectureChoices({
  choices,
  filter,
  onNavigate,
}: {
  choices: NamedArchitectureDestination[];
  filter: string;
  onNavigate: (search: BackendWorkspaceSearch) => void;
}) {
  const query = filter.trim().toLocaleLowerCase();
  const matching = choices.filter((choice) =>
    `${choice.name} ${choice.description}`.toLocaleLowerCase().includes(query),
  );
  if (!matching.length && query) return <Text mt="md">Схемы не найдены.</Text>;
  return (
    <section aria-label="Список схем" className={styles.architectureChoices}>
      {matching.map((choice) => (
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
    </section>
  );
}
