import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Button, NativeSelect, Stack, Text, TextInput, Title } from "@mantine/core";
import type { BackendReadTarget } from "@/api/generated/schemas";
import {
  readBackendGraph,
  readBackendCoverage,
  backendReadQueryKey,
  type BackendGraphFilters,
} from "./backendGraphReads";
import { LoadState, Pages } from "./BackendReadUI";
export type WorkspaceSelection = { recordType: "node" | "edge"; id: string };
export function BackendModelSearchPanel({
  projectId,
  target,
  onSelect,
  disabled = false,
}: {
  projectId: string;
  target: BackendReadTarget;
  onSelect: (selection: WorkspaceSelection) => void;
  disabled?: boolean;
}) {
  const [draft, setDraft] = useState({
    search: "",
    kind: "",
    serviceId: "",
    sourceSnapshotId: "",
    certainty: "" as "" | "explicit" | "inferred" | "unresolved" | "desired" | "stale",
  });
  const [filters, setFilters] = useState(draft);
  const [cursors, setCursors] = useState([""]);
  const query: BackendGraphFilters = {
    recordType: "nodes",
    limit: 50,
    cursor: cursors.at(-1),
    search: filters.search,
    kind: filters.kind,
    ...(filters.serviceId ? { serviceId: filters.serviceId } : {}),
    ...(filters.sourceSnapshotId ? { sourceSnapshotId: filters.sourceSnapshotId } : {}),
    ...(filters.certainty ? { certainty: filters.certainty } : {}),
  };
  const result = useQuery({
    queryKey: backendReadQueryKey("workspace-search", projectId, target, query),
    queryFn: ({ signal }) => readBackendGraph(projectId, target, query, signal),
    retry: false,
  });
  const services = useQuery({
    queryKey: backendReadQueryKey("workspace-service-tree", projectId, target),
    queryFn: ({ signal }) =>
      readBackendGraph(
        projectId,
        target,
        { recordType: "nodes", kind: "service", limit: 500 },
        signal,
      ),
    retry: false,
  });
  const coverage = useQuery({
    queryKey: backendReadQueryKey("workspace-search-sources", projectId, target),
    queryFn: ({ signal }) => readBackendCoverage(projectId, target, signal),
    retry: false,
  });
  return (
    <Stack component="section" aria-label="Модель: поиск и сервисы">
      <Title order={3}>Модель и сервисы</Title>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          setFilters(draft);
          setCursors([""]);
        }}
      >
        <Stack gap="xs">
          <TextInput
            label="Поиск по всей модели"
            value={draft.search}
            onChange={(e) => setDraft({ ...draft, search: e.currentTarget.value })}
          />
          <NativeSelect
            label="Тип в модели"
            value={draft.kind}
            data={[
              "",
              "service",
              "module",
              "symbol",
              "http_operation",
              "table",
              "column",
              "query",
              "channel",
              "message",
            ]}
            onChange={(e) => setDraft({ ...draft, kind: e.currentTarget.value })}
          />
          <NativeSelect
            label="Сервис в дереве модели"
            value={draft.serviceId}
            data={[
              { value: "", label: "Все сервисы" },
              ...(services.data?.nodes ?? []).map((n) => ({ value: n.id, label: n.name })),
            ]}
            onChange={(e) => setDraft({ ...draft, serviceId: e.currentTarget.value })}
          />
          <NativeSelect
            label="Source snapshot"
            value={draft.sourceSnapshotId}
            data={[
              { value: "", label: "Все источники" },
              ...(coverage.data?.snapshots ?? []).map((s) => ({
                value: s.id,
                label: `${s.provider.name} · ${s.capturedAt}`,
              })),
            ]}
            onChange={(e) => setDraft({ ...draft, sourceSnapshotId: e.currentTarget.value })}
          />
          <NativeSelect
            label="Certainty модели"
            value={draft.certainty}
            data={["", "explicit", "inferred", "unresolved", "desired", "stale"]}
            onChange={(e) =>
              setDraft({ ...draft, certainty: e.currentTarget.value as typeof draft.certainty })
            }
          />
          <Button type="submit" disabled={disabled}>
            Искать в точной модели
          </Button>
        </Stack>
      </form>
      <LoadState query={result} label="поиска модели" />
      {services.data?.nextCursor && (
        <Text>В дереве первые 500 сервисов; остальные доступны через поиск по всей модели.</Text>
      )}
      {result.data && (
        <>
          <Text>
            В модели: {"total" in result.data ? result.data.total : "неизвестно"}; на странице:{" "}
            {result.data.nodes.length}. Canvas — отдельная проекция.
          </Text>
          {result.data.nodes.map((n) => (
            <Button
              key={n.id}
              variant="subtle"
              disabled={disabled}
              h="auto"
              styles={{ label: { whiteSpace: "normal" } }}
              onClick={() => onSelect({ recordType: "node", id: n.id })}
            >
              {n.name} · {n.kind}
            </Button>
          ))}
          <Pages
            label="модель"
            cursors={cursors}
            next={result.data.nextCursor}
            busy={result.isFetching}
            setCursors={setCursors}
          />
        </>
      )}
    </Stack>
  );
}
