import { Alert, Code, Stack, Text, Title } from "@mantine/core";
import type { BackendReadTarget } from "@/api/generated/schemas";
import { backendReadTargetKey } from "./backendReadTargets";
import { BackendExactGraph } from "./BackendExactGraph";
export function BackendImportCandidate({
  projectId,
  target,
}: {
  projectId: string;
  target: BackendReadTarget | null;
}) {
  if (!target) return null;
  let key: string;
  try {
    key = backendReadTargetKey(target);
    if (!target.importCandidate) throw new Error("Выберите подготовленный граф.");
  } catch (error) {
    return (
      <Alert color="red" role="alert">
        {error instanceof Error ? error.message : "Некорректная версия кандидата."}
      </Alert>
    );
  }
  return (
    <Stack key={key} aria-label="Подготовленный граф импорта">
      <Title order={2}>Подготовленный граф</Title>
      <Text size="sm">
        Версия импорта: {target.importCandidate.importVersion}. Обновление импорта требует нового
        Preview.
      </Text>
      <Code style={{ overflowWrap: "anywhere", whiteSpace: "pre-wrap" }}>
        {target.importCandidate.candidateHash}
      </Code>
      <BackendExactGraph projectId={projectId} target={target} />
    </Stack>
  );
}
