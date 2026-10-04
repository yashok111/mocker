import { useState } from "react";
import { Button, Stack, Text, Title } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import type {
  BackendComposedNode,
  BackendNode,
  BackendEffectiveGraphPins,
  BackendLineageValueRef,
  BackendReadTarget,
} from "@/api/generated/schemas";
import { backendReadQueryKey, readBackendGraph } from "./backendGraphReads";
import { BackendReadError } from "./backendReadTargets";
import { LoadState, Pages } from "./BackendReadUI";
const wrap = { overflowWrap: "anywhere" as const, whiteSpace: "pre-wrap" as const };
export function BackendRepresentationFields({
  projectId,
  target,
  pins,
  ownerId,
  onInspect,
}: {
  projectId: string;
  target: BackendReadTarget;
  pins?: BackendEffectiveGraphPins;
  ownerId: string;
  onInspect: (id: string) => void;
}) {
  const [cursors, setCursors] = useState([""]);
  const input = {
    recordType: "nodes" as const,
    kind: "representation_field",
    parentId: ownerId,
    limit: 100,
    cursor: cursors.at(-1) ?? "",
  };
  const query = useQuery({
    queryKey: backendReadQueryKey("representation-fields", projectId, target, input),
    queryFn: async ({ signal }) => {
      const page = await readBackendGraph(projectId, target, input, signal, pins);
      if (
        page.nodes.some((node) => node.kind !== "representation_field" || node.parentId !== ownerId)
      )
        throw new BackendReadError("Получены поля другого представления.");
      return page;
    },
    retry: false,
    staleTime: Infinity,
  });
  return (
    <Stack gap="sm" aria-label="Поля представления">
      <Title order={5}>Поля представления</Title>
      <LoadState query={query} label="полей представления" />
      {query.data?.nodes.length === 0 && (
        <Text c="dimmed">Поля не объявлены. Соответствия по именам не определяются.</Text>
      )}
      {query.data?.nodes.map((node) => (
        <div key={node.id}>
          <Button
            variant="subtle"
            h="auto"
            styles={{ label: wrap }}
            onClick={() => onInspect(node.id)}
          >
            Открыть поле {node.name}
          </Button>
          <BackendRepresentationValue node={node} />
        </div>
      ))}
      <Pages
        label="поля представления"
        cursors={cursors}
        next={query.data?.nextCursor}
        busy={query.isFetching}
        setCursors={setCursors}
      />
    </Stack>
  );
}
export function BackendRepresentationValue({
  node,
  onValueSelect,
}: {
  node: BackendComposedNode | BackendNode;
  onValueSelect?: (value: BackendLineageValueRef) => void;
}) {
  if (node.kind !== "representation_field") return null;
  const attrs = node.attributes;
  return (
    <Stack gap="xs" aria-label={`Поле представления ${node.name}`}>
      <Text fw={600} style={wrap}>
        {attrs.selector.map((segment) => segment.property).join(".")}
      </Text>
      <Text size="sm">
        Тип: {attrs.nativeType.status === "known" ? attrs.nativeType.value : "неизвестен"}
      </Text>
      {attrs.nativeType.status === "unknown" && (
        <Text size="sm" style={wrap}>
          {attrs.nativeType.reason}
        </Text>
      )}
      <Text size="sm">
        Nullable: {attrs.nullable.status === "known" ? String(attrs.nullable.value) : "неизвестно"}
      </Text>
      {attrs.nullable.status === "unknown" && (
        <Text size="sm" style={wrap}>
          {attrs.nullable.reason}
        </Text>
      )}
      <Text size="sm">
        Количество: {attrs.cardinality.status === "known" ? attrs.cardinality.value : "неизвестно"}
      </Text>
      {attrs.cardinality.status === "unknown" && (
        <Text size="sm" style={wrap}>
          {attrs.cardinality.reason}
        </Text>
      )}
      <Text size="sm">Анализ: {attrs.analysisStatus}</Text>
      {attrs.gaps.map((gap, index) => (
        <Text key={index} size="sm" style={wrap}>
          {gap}
        </Text>
      ))}
      {onValueSelect && (
        <Button
          variant="default"
          h="auto"
          styles={{ label: wrap }}
          onClick={() => onValueSelect({ kind: "representation_field", nodeId: node.id })}
        >
          Происхождение поля {node.name}
        </Button>
      )}
    </Stack>
  );
}
