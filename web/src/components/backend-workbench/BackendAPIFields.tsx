import { Stack, Text, Title } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import type {
  BackendLineageValueRef,
  BackendReadTarget,
  BackendEffectiveGraphPins,
} from "@/api/generated/schemas";
import { databaseWrap } from "./backendDatabaseReads";
import { usePinnedValue } from "./backendFlowReads";
import { backendReadTargetFrom, backendReadTargetKey } from "./backendReadTargets";
import { readBackendGraph, backendReadQueryKey } from "./backendGraphReads";
import { BackendValueSeeds } from "./BackendLineageActions";
import { LoadState, Pages } from "./BackendReadUI";
import { BackendAPIArtifacts } from "./BackendAPIArtifacts";
export function BackendAPIFields({
  projectId,
  revisionId,
  target: selectedTarget,
  pins,
  operationId,
  onValueSelect,
}: {
  projectId: string;
  revisionId?: string;
  target?: BackendReadTarget;
  pins?: BackendEffectiveGraphPins;
  operationId: string;
  onValueSelect: (ref: BackendLineageValueRef) => void;
}) {
  const target = selectedTarget ?? backendReadTargetFrom({ revisionId });
  const [cursors, setCursors] = usePinnedValue<string[]>(
    JSON.stringify([projectId, backendReadTargetKey(target), operationId]),
    [""],
  );
  const input = {
    recordType: "nodes" as const,
    kind: "api_field",
    parentId: operationId,
    limit: 100,
    cursor: cursors.at(-1) ?? "",
  };
  const key = backendReadQueryKey("api-fields", projectId, target, input);
  const query = useQuery({
    queryKey: key,
    retry: false,
    staleTime: Infinity,
    queryFn: ({ signal }) => readBackendGraph(projectId, target, input, signal, pins),
  });
  return (
    <Stack>
      <Title order={4}>Поля API</Title>
      <LoadState query={query} label="полей API" />
      {query.data?.nodes.map((node) => (
        <Stack key={node.id} gap="xs">
          <APIFieldDescription attributes={node.attributes} />
          <BackendAPIArtifacts
            projectId={projectId}
            {...(target.revisionId ? { revisionId: target.revisionId } : { target, pins })}
            sourceNodeId={node.id}
            sourceKind="api_field"
          />
          <BackendValueSeeds
            projectId={projectId}
            target={target}
            pins={pins}
            node={node}
            onValueSelect={onValueSelect}
          />
        </Stack>
      ))}
      <Pages
        label="поля API"
        cursors={cursors}
        next={query.data?.nextCursor}
        busy={query.isFetching}
        setCursors={setCursors}
      />
      {query.data?.nodes.length === 0 && <Text>Поля API в этом снимке не импортированы.</Text>}
    </Stack>
  );
}

function APIFieldDescription({
  attributes,
}: {
  attributes:
    | import("@/api/generated/schemas").BackendNode["attributes"]
    | import("@/api/generated/schemas").BackendComposedNode["attributes"];
}) {
  if (!("direction" in attributes) || !("location" in attributes) || !("selector" in attributes))
    return <Text>Структура поля неизвестна</Text>;
  return (
    <Stack gap="xs">
      <Text style={databaseWrap}>
        Направление: {attributes.direction} · расположение: {attributes.location}
        {"responseStatus" in attributes ? ` · статус: ${attributes.responseStatus}` : ""}
        {"mediaType" in attributes ? ` · media: ${attributes.mediaType}` : ""}
      </Text>
      <Text size="sm" style={databaseWrap}>
        Структурный путь: {JSON.stringify(attributes.selector)}
      </Text>
    </Stack>
  );
}
