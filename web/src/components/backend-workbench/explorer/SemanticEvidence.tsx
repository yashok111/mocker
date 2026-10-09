import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Button, Stack, Text } from "@mantine/core";
import type { BackendDiagramRef, BackendReadTarget } from "@/api/generated/schemas";
import type { BackendWorkspaceSearch } from "../backendWorkspaceSearch";
import { readBackendNode, readBackendGraph } from "../backendGraphReads";
import { backendReadTargetKey } from "../backendReadTargets";
import { resolvedTargetSearch } from "./navigation";
import styles from "./Explorer.module.css";

export function SemanticEvidence({
  projectId,
  target,
  refs,
  onNavigate,
}: {
  projectId: string;
  target: BackendReadTarget;
  refs: BackendDiagramRef[];
  onNavigate: (search: BackendWorkspaceSearch) => void;
}) {
  const [open, setOpen] = useState(false);
  const unique = [...new Map(refs.map((ref) => [JSON.stringify(ref), ref])).values()];
  if (!unique.length) return null;
  return (
    <details
      className={styles.sourceCodeDetails}
      onToggle={(event) => setOpen(event.currentTarget.open)}
    >
      <summary>Основания сценария · {unique.length}</summary>
      {open && (
        <Stack gap="xs" mt="sm">
          {unique.map((ref) =>
            ref.kind === "record" ? (
              <SourceReference
                key={JSON.stringify(ref)}
                projectId={projectId}
                target={target}
                reference={ref}
                onNavigate={onNavigate}
              />
            ) : (
              <details key={JSON.stringify(ref)}>
                <summary>Точная ссылка на сохранённый артефакт</summary>
                <pre style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>
                  {JSON.stringify(ref, null, 2)}
                </pre>
              </details>
            ),
          )}
        </Stack>
      )}
    </details>
  );
}
function SourceReference({
  projectId,
  target,
  reference,
  onNavigate,
}: {
  projectId: string;
  target: BackendReadTarget;
  reference: Extract<BackendDiagramRef, { kind: "record" }>;
  onNavigate: (search: BackendWorkspaceSearch) => void;
}) {
  const query = useQuery({
    queryKey: [
      "semantic-evidence-name",
      projectId,
      backendReadTargetKey(target),
      reference.recordType,
      reference.id,
    ],
    retry: false,
    staleTime: Infinity,
    queryFn: async ({ signal }) => {
      if (reference.recordType === "edge") {
        const page = await readBackendGraph(
          projectId,
          target,
          { recordType: "edges", id: reference.id, limit: 1 },
          signal,
        );
        const edge = page.edges.find((e) => e.id === reference.id);
        if (!edge) throw new Error("Основание недоступно");
        return "Связь: " + edge.kind;
      }
      const response = await readBackendNode(projectId, target, reference.id, signal);
      const value = "node" in response ? response.node : response;
      if (!("id" in value) || value.id !== reference.id || !("name" in value))
        throw new Error("Получено другое основание");
      return String(value.name);
    },
  });
  return (
    <div>
      <Button
        variant="subtle"
        size="compact-xs"
        h="auto"
        styles={{ label: { whiteSpace: "normal", overflowWrap: "anywhere" } }}
        onClick={() =>
          onNavigate({
            ...resolvedTargetSearch(target),
            wbView: "structure",
            wbMode: reference.recordType === "node" ? "neighborhood" : "overview",
            wbScope: reference.recordType === "node" ? reference.id : undefined,
            recordId: reference.id,
            recordType: reference.recordType,
          })
        }
      >
        {query.data ?? (query.isError ? "Открыть недоступное основание" : "Читаем основание…")}
      </Button>
      {query.isError && (
        <Text size="xs" c="dimmed">
          Источник в этой версии не прочитан.{" "}
          <Button variant="subtle" size="compact-xs" onClick={() => void query.refetch()}>
            Повторить
          </Button>
        </Text>
      )}
    </div>
  );
}
