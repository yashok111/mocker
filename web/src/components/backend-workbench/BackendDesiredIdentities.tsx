import { Stack, Text, Title } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import type { BackendEffectiveGraphPins, BackendReadTarget } from "@/api/generated/schemas";
import { backendReadQueryKey, readBackendGraph } from "./backendGraphReads";
import { LoadState } from "./BackendReadUI";
import { BackendReadError } from "./backendReadTargets";
const wrap = { overflowWrap: "anywhere" as const, whiteSpace: "pre-wrap" as const };
export function BackendDesiredIdentities({
  projectId,
  target,
  pins,
  recordType,
  id,
}: {
  projectId: string;
  target: BackendReadTarget;
  pins?: BackendEffectiveGraphPins;
  recordType: "node" | "edge";
  id: string;
}) {
  const input = {
    recordType: recordType === "node" ? ("nodes" as const) : ("edges" as const),
    id,
    limit: 1,
  };
  const query = useQuery({
    queryKey: backendReadQueryKey("intended-identities", projectId, target, input),
    queryFn: async ({ signal }) => {
      const page = await readBackendGraph(projectId, target, input, signal, pins);
      const identities = "identities" in page ? (page.identities ?? []) : [];
      for (const identity of identities) {
        const ref =
          identity.target.kind === "source_identity" ? identity.target.source : identity.target;
        if (ref.id !== id || ref.recordType !== recordType)
          throw new BackendReadError("Получена идентичность другого объекта.");
      }
      return identities;
    },
    retry: false,
    staleTime: Infinity,
  });
  return (
    <Stack gap="xs" aria-label="Ключи предложения">
      <Title order={5}>Ключи предложения</Title>
      <LoadState query={query} label="ключей предложения" />
      {query.data?.map((identity) => (
        <div key={JSON.stringify(identity.target)}>
          {identity.target.kind === "source_identity" ? (
            <>
              <Text size="sm" style={wrap}>
                {identity.target.source.providerNamespace} · {identity.target.source.externalKey} →{" "}
                {identity.externalKey ?? "не задан"}
              </Text>
              <Text size="xs" style={wrap}>
                {identity.target.source.repositoryId} · {identity.target.source.assertionHash}
              </Text>
            </>
          ) : (
            <Text size="sm" style={wrap}>
              Новая идентичность: {identity.externalKey ?? "ключ не задан"}
            </Text>
          )}
          {identity.origin.kind === "intent" && (
            <Text size="sm" style={wrap}>
              Намерение: {identity.origin.reason}
            </Text>
          )}
        </div>
      ))}
    </Stack>
  );
}
