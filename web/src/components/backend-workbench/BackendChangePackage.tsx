import { Button, Stack, Text, Title } from "@mantine/core";
import type {
  BackendChangeProposalDetail,
  StartBackendAnalysisRequest,
} from "@/api/generated/schemas";
export type B43Start = (input: StartBackendAnalysisRequest) => Promise<void>;
export function BackendChangePackage({
  proposal,
  disabled,
  onStart,
}: {
  proposal: BackendChangeProposalDetail;
  disabled: boolean;
  onStart: B43Start;
}) {
  return (
    <section aria-label="Пакет изменений">
      <Stack gap="xs">
        <Title order={4}>Пакет изменений</Title>
        <Text size="sm">
          Пакет закрепляет сохранённый черновик, его базу, критерии и основания. Несохранённый буфер
          не входит в пакет.
        </Text>
        <Text size="xs" style={{ overflowWrap: "anywhere" }}>
          Черновик: {proposal.revision.id} · хеш {proposal.revision.semanticHash}
        </Text>
        <Button
          disabled={disabled}
          onClick={() =>
            void onStart({
              kind: "change_package",
              changeProposal: {
                proposalId: proposal.proposal.id,
                proposalRevisionId: proposal.revision.id,
              },
              limits: {},
              observationMode: "none",
              idempotencyKey: crypto.randomUUID(),
            })
          }
        >
          Собрать пакет изменений
        </Button>
      </Stack>
    </section>
  );
}
