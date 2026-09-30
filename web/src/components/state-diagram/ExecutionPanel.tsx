import { Alert, Button, Group, Paper, Stack, Text } from "@mantine/core";
import type { StateDiagramExecution } from "@/api/generated/schemas";

export type ExecutionControls = {
  data: StateDiagramExecution | null;
  blocked: string | null;
  pending: boolean;
  error: string | null;
  onApply: (diagramId: string) => void;
  onRemove: (diagramId: string) => void;
  onRefresh: () => void;
};

const labels = {
  current: "Совпадает с сохранённой диаграммой",
  outdated: "Есть изменения для повторного применения",
  missing: "Исходная диаграмма удалена",
};

export default function ExecutionPanel({
  execution,
  diagramId,
}: {
  execution: ExecutionControls;
  diagramId?: string;
}) {
  const selected = execution.data?.diagrams.find((item) => item.diagramId === diagramId);
  const disabled = execution.blocked !== null || execution.pending;
  return (
    <Paper component="section" aria-label="Исполнение диаграмм" withBorder p="md" radius="md">
      <Stack gap="sm">
        <Group justify="space-between" align="flex-start">
          <div>
            <Text fw={600}>В моке черновика</Text>
            <Text size="sm" c="dimmed">
              Применение сохраняет отдельную копию диаграммы. После любых правок примените её
              повторно. Опубликованный мок обновится через проверку и публикацию API.
            </Text>
          </div>
          {diagramId && (
            <Button
              size="sm"
              disabled={disabled}
              loading={execution.pending}
              onClick={() => execution.onApply(diagramId)}
            >
              {selected ? "Применить повторно" : "Применить к моку"}
            </Button>
          )}
        </Group>
        {execution.blocked && (
          <Text size="sm" c="dimmed">
            {execution.blocked}
          </Text>
        )}
        {execution.error && (
          <Alert color="red" role="alert">
            {execution.error}
            <Button
              size="xs"
              variant="subtle"
              display="block"
              mt="xs"
              onClick={execution.onRefresh}
            >
              Обновить состояние
            </Button>
          </Alert>
        )}
        {execution.data?.diagrams.length === 0 && <Text size="sm">Применённых диаграмм нет.</Text>}
        {execution.data?.diagrams.map((item) => (
          <Group key={item.diagramId} justify="space-between" align="flex-start" wrap="wrap">
            <Stack gap={4} style={{ minWidth: 0, flex: "1 1 220px", overflowWrap: "anywhere" }}>
              <Text size="sm" fw={600}>
                {item.name || "Без названия"}
              </Text>
              <Text size="sm">
                {item.entity.family} · поле {item.entity.stateField} · операций:{" "}
                {item.operationCount}
              </Text>
              <Text size="xs" c={item.state === "current" ? "green" : "orange"}>
                {labels[item.state]}
              </Text>
            </Stack>
            <Button
              size="xs"
              variant="default"
              disabled={disabled}
              aria-label={`Снять применение: ${item.name || "Без названия"}`}
              onClick={() => execution.onRemove(item.diagramId)}
            >
              Снять применение
            </Button>
          </Group>
        ))}
        <details>
          <summary>Условия исполнения HTTP</summary>
          <Text size="sm" c="dimmed" mt="xs">
            POST, PUT, PATCH и DELETE выполняют переход для записи по ключу из пути. Состояние
            хранится в выбранном поле данных сущности. Отсутствующее поле означает начальное
            состояние; неизвестное значение и отказ перехода возвращают 409, отсутствующая запись —
            404. Переопределения ответа и настройки сессии могут перекрывать переход.
          </Text>
        </details>
      </Stack>
    </Paper>
  );
}
