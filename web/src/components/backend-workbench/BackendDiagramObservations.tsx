import { Alert, Code, Stack, Text, Title } from "@mantine/core";
import type { BackendDiagramObserved, BackendDiagramScope } from "@/api/generated/schemas";
export function BackendDiagramObservations({
  report,
  scope,
}: {
  report: BackendDiagramObserved;
  scope?: BackendDiagramScope;
}) {
  if (
    !scope ||
    report.diagramScope.pin.id !== scope.pin.id ||
    report.diagramScope.pin.version !== scope.pin.version ||
    report.diagramScope.pin.contentHash !== scope.pin.contentHash ||
    report.diagramScope.scopeHash !== scope.scopeHash
  )
    return <Alert>Этот overlay относится к другой точной области диаграммы.</Alert>;
  return (
    <Stack data-testid="backend-diagram-observations">
      <Title order={4}>Observed sequence · diagram v{report.diagramScope.pin.version}</Title>
      <Text>
        Альтернативы source/intent остаются непроверенными. Parent — containment; send → receive
        упорядочивает события, а не завершение spans.
      </Text>
      {report.elements.map((e) => (
        <Text key={e.id}>
          {e.recordId} · {e.basis} ·{" "}
          {e.selectors.map((s) => s.id).join(", ") || "mapping неизвестен"}
        </Text>
      ))}
      {report.relations.map((e, i) => (
        <Text key={i}>
          {e.from} → {e.to}: {e.kind}
        </Text>
      ))}
      {report.gaps.map((g) => (
        <Alert key={g} color="yellow">
          {g}
        </Alert>
      ))}
      <details>
        <summary>Точные pins overlay</summary>
        <Code block>{JSON.stringify(report.pins, null, 2)}</Code>
      </details>
    </Stack>
  );
}
