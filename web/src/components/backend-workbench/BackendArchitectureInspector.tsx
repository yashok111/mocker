import { Alert, Button, Code, Group, Paper, Stack, Text, Title } from "@mantine/core";
import type {
  BackendDiagramVersion,
  BackendDiagramViewState,
  BackendDiagramRef,
} from "@/api/generated/schemas";
import { useDiagramPage } from "./backendDiagramReads";
import { usePinnedValue } from "./backendFlowReads";
export function BackendArchitectureInspector({
  projectId,
  diagram,
  state,
  onClose,
  onOpen,
}: {
  projectId: string;
  diagram: BackendDiagramVersion;
  state: BackendDiagramViewState;
  onClose: () => void;
  onOpen: (ref: BackendDiagramRef) => void;
}) {
  const identity = JSON.stringify([diagram.pin, state.level, state.rootId, state.selection]);
  const [cursors, setCursors] = usePinnedValue<string[]>(identity, [""]);
  const query = useDiagramPage(
    projectId,
    {
      pin: diagram.pin,
      level: state.level,
      rootId: state.rootId,
      search: "",
      origin: "all",
      section: "members",
      subjectId: state.selection?.id,
      limit: 100,
      ...(cursors.at(-1) ? { cursor: cursors.at(-1) } : {}),
    },
    !!state.selection,
  );
  if (!state.selection) return null;
  const element = diagram.document.payload.elements.find((e) => e.id === state.selection?.id);
  const provenance = diagram.provenance.elements.find((e) => e.elementId === state.selection?.id);
  return (
    <Paper withBorder p="md">
      <Stack aria-label="C4 инспектор">
        <Group justify="space-between">
          <Title order={3} tabIndex={-1} id="c4-inspector-title">
            Основания и участники связи
          </Title>
          <Button variant="subtle" onClick={onClose}>
            Закрыть инспектор
          </Button>
        </Group>
        {element && (
          <Stack gap="xs">
            <Text fw={600}>
              {element.label} · {element.role}
            </Text>
            <Text>{element.responsibility || "Ответственность не описана"}</Text>
            <Text>{element.technology || "Технология не указана"}</Text>
            <Text size="sm">
              {element.origin.kind === "authored"
                ? element.origin.reason
                : "Граница подтверждена исходным утверждением; runtime не проверен"}
            </Text>
          </Stack>
        )}
        {provenance && (
          <Text size="sm">
            Введён: {provenance.introduced.author} · v{provenance.introduced.pin.version}; последняя
            правка: {provenance.lastEdited.author} · v{provenance.lastEdited.pin.version}
          </Text>
        )}
        <Code style={{ overflowWrap: "anywhere", whiteSpace: "normal" }}>{state.selection.id}</Code>
        <Text>
          {state.level} · {state.rootId} · architecture-v1
        </Text>
        {query.isError && (
          <Alert role="alert" color="red">
            Точные members недоступны.{" "}
            <Button onClick={() => void query.refetch()}>Повторить members</Button>
          </Alert>
        )}
        {query.data && (
          <Text>
            Всего участников: {query.data.total}.{" "}
            {query.data.truncated
              ? "Обход ограничен; покрытие неполное."
              : "Обычная пагинация не ограничивает membership."}
          </Text>
        )}
        {query.data?.items.map((row, index) =>
          row.rowType === "member" ? (
            <Paper key={index} withBorder p="sm">
              <Stack gap="xs">
                <Text>
                  {row.data.origin.kind === "authored"
                    ? "Авторский замысел"
                    : "Утверждение исходного кода"}
                </Text>
                <Code style={{ overflowWrap: "anywhere", whiteSpace: "normal" }}>
                  {row.data.ref.kind === "record" ? row.data.ref.id : row.data.ref.rowId}
                </Code>
                <Text size="sm">
                  {row.data.origin.kind === "authored"
                    ? row.data.origin.reason
                    : `Evidence: ${row.data.origin.evidence.map((e) => `${e.revisionId}/${e.evidenceId} → ${e.subjectId}`).join(", ")}`}
                </Text>
                <Button variant="default" onClick={() => onOpen(row.data.ref)}>
                  Открыть точный источник
                </Button>
              </Stack>
            </Paper>
          ) : null,
        )}
        <Group>
          <Button
            disabled={cursors.length === 1 || query.isFetching}
            onClick={() => setCursors((c) => c.slice(0, -1))}
          >
            Предыдущие members
          </Button>
          <Button
            disabled={!query.data?.nextCursor || query.isFetching}
            onClick={() => setCursors((c) => [...c, query.data!.nextCursor])}
          >
            Следующие members
          </Button>
        </Group>
      </Stack>
    </Paper>
  );
}
