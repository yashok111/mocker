import { useQuery } from "@tanstack/react-query";
import { Button, Loader, Stack, Text, Alert } from "@mantine/core";
import {
  getBackendRevision,
  getBackendChangeProposal,
} from "@/api/generated/backend-projects/backend-projects";
import type { BackendReadTarget } from "@/api/generated/schemas";
import { backendReadTargetKey } from "../backendReadTargets";
import { readAPIArtifacts } from "../backendAPIArtifactReads";
import { artifactNavigationHref } from "../BackendAPIArtifacts";
import { verifyChangeDetail } from "../backendChangeReads";
import { ok } from "./catalogs";
export function Contracts({
  projectId,
  target,
  nodeId,
}: {
  projectId: string;
  target: BackendReadTarget;
  nodeId: string;
}) {
  const query = useQuery({
    queryKey: ["workbench-contracts", projectId, backendReadTargetKey(target), nodeId],
    retry: false,
    staleTime: Infinity,
    queryFn: async ({ signal }) => {
      const revision = target.changeProposal
        ? verifyChangeDetail(
            ok(
              await getBackendChangeProposal(
                projectId,
                target.changeProposal.proposalId,
                { proposalRevisionId: target.changeProposal.proposalRevisionId, limit: 1 },
                { signal },
              ),
            ),
            projectId,
            target.changeProposal.proposalId,
            target.changeProposal.proposalRevisionId,
          ).revision
        : target.revisionId
          ? ok(await getBackendRevision(projectId, target.revisionId, { signal }))
          : undefined;
      if (!revision) throw new Error("Контракты этого типа предложения недоступны");
      const revisionId = "baseRevisionId" in revision ? revision.baseRevisionId : revision.id;
      return readAPIArtifacts(
        {
          projectId,
          target,
          revisionId,
          semanticHash: revision.semanticHash,
          sourceSnapshotIds: revision.sourceSnapshotIds,
          artifactPins: revision.artifactPins,
        },
        nodeId,
        signal,
      );
    },
  });
  return (
    <Stack gap="sm">
      {query.isPending && <Loader size="sm" />}
      {query.isError && (
        <Alert color="red">
          Закреплённые контракты недоступны.
          <Button variant="subtle" size="compact-xs" onClick={() => void query.refetch()}>
            Повторить
          </Button>
        </Alert>
      )}
      {query.data?.items.length === 0 && (
        <Text size="sm" c="dimmed">
          Для объекта пока нет связанного API-контракта.
        </Text>
      )}
      {query.data?.items.map((item, i) => {
        const ref = item.binding.ref;
        const href = artifactNavigationHref(
          ref,
          projectId,
          target.revisionId ?? "",
          nodeId,
          target,
        );
        return (
          <div key={i}>
            <Text size="sm" fw={550}>
              {ref.lastKnownLabel || item.binding.sourceLastKnownLabel}
            </Text>
            <Text size="xs" c="dimmed">
              {item.binding.reason}
            </Text>
            {href ? (
              <Button component="a" href={href} variant="subtle" size="compact-sm">
                Открыть точный контракт
              </Button>
            ) : (
              <Text size="sm">Точная версия недоступна</Text>
            )}
            {item.resolution.updateAvailable && (
              <Text size="xs" c="dimmed">
                Есть более новая версия; здесь сохранена исходная.
              </Text>
            )}
          </div>
        );
      })}
    </Stack>
  );
}
