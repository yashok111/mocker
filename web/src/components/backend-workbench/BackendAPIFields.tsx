import { Stack, Text, Title } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import type { BackendLineageValueRef } from "@/api/generated/schemas";
import { readDatabaseGraph, useDatabaseCancellation, databaseWrap } from "./backendDatabaseReads";
import { BackendValueSeeds } from "./BackendLineageActions";
import { LoadState } from "./BackendGraphInventory";
import { BackendAPIArtifacts } from "./BackendAPIArtifacts";
export function BackendAPIFields({
  projectId,
  revisionId,
  operationId,
  onValueSelect,
}: {
  projectId: string;
  revisionId: string;
  operationId: string;
  onValueSelect: (ref: BackendLineageValueRef) => void;
}) {
  const key = ["backend-api-fields", projectId, revisionId, operationId];
  useDatabaseCancellation(key);
  const query = useQuery({
    queryKey: key,
    retry: false,
    staleTime: Infinity,
    queryFn: ({ signal }) =>
      readDatabaseGraph(
        projectId,
        { revisionId, recordType: "nodes", kind: "api_field", parentId: operationId },
        signal,
      ),
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
            revisionId={revisionId}
            sourceNodeId={node.id}
            sourceKind="api_field"
          />
          <BackendValueSeeds
            projectId={projectId}
            revisionId={revisionId}
            node={node}
            onValueSelect={onValueSelect}
          />
        </Stack>
      ))}
      {query.data?.nodes.length === 0 && <Text>Поля API в этом снимке не импортированы.</Text>}
    </Stack>
  );
}

function APIFieldDescription({
  attributes,
}: {
  attributes: import("@/api/generated/schemas").BackendNode["attributes"];
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
