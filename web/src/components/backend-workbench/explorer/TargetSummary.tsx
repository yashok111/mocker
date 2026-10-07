import { useQuery } from "@tanstack/react-query";
import { Button, Tooltip } from "@mantine/core";
import { IconVersions } from "@tabler/icons-react";
import {
  getBackendRevision,
  getBackendChangeProposal,
} from "@/api/generated/backend-projects/backend-projects";
import type { BackendReadTarget } from "@/api/generated/schemas";
import { backendReadTargetKey } from "../backendReadTargets";
import { verifyChangeDetail } from "../backendChangeReads";
import { ok } from "./catalogs";
export function TargetSummary({
  projectId,
  target,
  onOpen,
}: {
  projectId: string;
  target: BackendReadTarget;
  onOpen: () => void;
}) {
  const query = useQuery({
    queryKey: ["workbench-target-label", projectId, backendReadTargetKey(target)],
    retry: false,
    staleTime: Infinity,
    queryFn: async ({ signal }) => {
      if (target.changeProposal) {
        const p = target.changeProposal;
        const d = verifyChangeDetail(
          ok(
            await getBackendChangeProposal(
              projectId,
              p.proposalId,
              { proposalRevisionId: p.proposalRevisionId, limit: 1 },
              { signal },
            ),
          ),
          projectId,
          p.proposalId,
          p.proposalRevisionId,
        );
        return {
          label: d.proposal.name,
          description: `Предложение · основание ${d.revision.baseRevisionId}`,
        };
      }
      if (target.revisionId) {
        const r = ok(await getBackendRevision(projectId, target.revisionId, { signal }));
        if (r.id !== target.revisionId || r.projectId !== projectId)
          throw new Error("Другая версия модели");
        return {
          label: new Date(r.createdAt).toLocaleDateString("ru-RU", {
            day: "numeric",
            month: "short",
          }),
          description: r.summary,
        };
      }
      return { label: "Сохранённая версия", description: "Точная версия модели" };
    },
  });
  return (
    <Tooltip label={query.data?.description ?? "Просмотр источников и версий"}>
      <Button
        variant="subtle"
        size="compact-sm"
        leftSection={<IconVersions size={15} />}
        onClick={onOpen}
      >
        {target.changeProposal ? "Предложение" : "Версия модели"}
        {query.data ? ` · ${query.data.label}` : ""}
      </Button>
    </Tooltip>
  );
}
