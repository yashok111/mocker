import { Alert, Button, Group, Paper, Stack, Text } from "@mantine/core";
import type { ResponseRuleExecution } from "@/api/generated/schemas";

export type ExecutionControls = {
  data: ResponseRuleExecution | null;
  blocked: string | null;
  pending: boolean;
  error: string | null;
  onApply: (ruleId: string) => void;
  onRemove: (ruleId: string) => void;
  onRefresh: () => void;
};

const labels = {
  current: "Совпадает с сохранённым правилом",
  outdated: "Есть изменения для повторного применения",
  missing: "Исходное правило удалено",
};

export default function ExecutionPanel({
  execution,
  ruleId,
}: {
  execution: ExecutionControls;
  ruleId?: string;
}) {
  const selected = execution.data?.rules.find((item) => item.ruleId === ruleId);
  const disabled = execution.blocked !== null || execution.pending;
  return (
    <Paper component="section" aria-label="Исполнение правил" withBorder p="md" radius="md">
      <Stack gap="sm">
        <Group justify="space-between" align="flex-start">
          <div>
            <Text fw={600}>В моке черновика</Text>
            <Text size="sm" c="dimmed">
              Применение сохраняет отдельную копию правила. После правок примените его повторно.
              Опубликованный мок обновится через проверку и публикацию API.
            </Text>
          </div>
          {ruleId && (
            <Button
              size="sm"
              disabled={disabled}
              loading={execution.pending}
              onClick={() => execution.onApply(ruleId)}
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
        {execution.data && execution.data.rules.length === 0 && (
          <Text size="sm">Применённых правил нет. Мок использует стандартную обработку.</Text>
        )}
        {execution.data?.rules.map((item) => (
          <Group key={item.ruleId} justify="space-between" align="flex-start" wrap="wrap">
            <Stack gap={4} style={{ minWidth: 0, flex: "1 1 220px", overflowWrap: "anywhere" }}>
              <Text size="sm" fw={600}>
                {item.name || "Без названия"}
              </Text>
              <Text size="sm">
                {item.binding.method} {item.binding.path}
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
              onClick={() => execution.onRemove(item.ruleId)}
            >
              Снять применение
            </Button>
          </Group>
        ))}
        <details>
          <summary>Условия исполнения и входные данные HTTP</summary>
          <Stack gap="xs" mt="xs">
            <Text size="sm" c="dimmed">
              Настройки сценария и переопределения ответа могут перекрывать правило. GET-правило
              также обрабатывает HEAD, без тела ответа.
            </Text>
            <Text size="sm" c="dimmed">
              В реальном моке условия тела не работают для GET, HEAD, DELETE и multipart. Для других
              методов принимается JSON с JSON-типом содержимого, text/plain или без Content-Type.
              Некорректное, обрезанное или превышающее 64 КиБ тело, повторяющиеся ключи и
              вложенность больше 64 делают условия тела ложными.
            </Text>
            <Text size="sm" c="dimmed">
              Симуляция принимает явно заданное тело для любого метода и сообщает об ошибках JSON. В
              моке задержка выполняется реально; симуляция только рассчитывает её.
            </Text>
          </Stack>
        </details>
      </Stack>
    </Paper>
  );
}
