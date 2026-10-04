import { Alert, Badge, Button, Group, Loader, Stack, Text } from "@mantine/core";
import type {
  BackendCoverageResponse,
  BackendEmbeddedCoverageResponse,
} from "@/api/generated/schemas";
import { describeApiFailureDetailed } from "@/api/errors";
import { BackendReadError } from "./backendReadTargets";
const wrap = { overflowWrap: "anywhere" as const, whiteSpace: "pre-wrap" as const };
const categories: Record<string, string> = {
  files: "Файлы",
  endpoints: "Точки входа",
  datastores: "Хранилища",
  migrations: "Миграции",
  producers: "Отправители событий",
  consumers: "Получатели событий",
  jobs: "Фоновые задачи",
  contracts: "Контракты",
  tests: "Тесты",
};
const statuses: Record<string, string> = {
  complete: "Полное",
  partial: "Частичное",
  unsupported: "Не поддерживается",
  excluded: "Исключено",
};
export function LoadState({
  query,
  label,
}: {
  query: { isPending: boolean; isError: boolean; error: unknown; refetch: () => unknown };
  label: string;
}) {
  return (
    <>
      {query.isPending && <Loader aria-label={`Загружаем ${label}`} />}
      {query.isError && (
        <Alert color="red" role="alert">
          {query.error instanceof BackendReadError
            ? query.error.message
            : describeApiFailureDetailed(query.error)}
          <Button mt="sm" variant="light" onClick={() => void query.refetch()}>
            Повторить загрузку {label}
          </Button>
        </Alert>
      )}
    </>
  );
}

export function Pages({
  label,
  cursors,
  next,
  busy,
  setCursors,
}: {
  label: string;
  cursors: string[];
  next?: string;
  busy: boolean;
  setCursors: (update: (previous: string[]) => string[]) => void;
}) {
  if (cursors.length === 1 && !next) return null;
  return (
    <Group>
      <Button
        variant="default"
        disabled={busy || cursors.length === 1}
        onClick={() => setCursors((previous) => previous.slice(0, -1))}
      >
        Предыдущие {label}
      </Button>
      <Button
        variant="default"
        disabled={busy || !next}
        onClick={() => {
          if (next) setCursors((previous) => [...previous, next]);
        }}
      >
        Следующие {label}
      </Button>
    </Group>
  );
}

export function CoverageDetails({
  data,
}: {
  data: BackendCoverageResponse | BackendEmbeddedCoverageResponse;
}) {
  return (
    <Stack gap="sm">
      <Group>
        <Badge color={data.coverage.status === "complete" ? "green" : "yellow"}>
          {data.coverage.status === "complete" ? "Полное покрытие" : "Частичное покрытие"}
        </Badge>
        <Text size="sm">
          Объектов: {data.coverage.knownObjects}.{" "}
          {data.coverage.denominator === null
            ? "Общее количество неизвестно."
            : `Всего: ${data.coverage.denominator}.`}
        </Text>
      </Group>
      {data.staleCounts && (
        <Text size="sm">
          Устаревшие: объекты {data.staleCounts.nodes} · связи {data.staleCounts.edges} · основания{" "}
          {data.staleCounts.evidence}
        </Text>
      )}
      {data.reconciliationGaps?.map((gap, index) => (
        <Text key={`reconcile:${index}`} size="sm" style={wrap}>
          {gap}
        </Text>
      ))}
      {data.coverage.gaps.map((gap, index) => (
        <Text key={index} size="sm" style={wrap}>
          {gap}
        </Text>
      ))}
      {data.snapshots.length === 0 && <Text c="dimmed">Исходный код ещё не импортирован.</Text>}
      {data.snapshots.map((snapshot) => (
        <div key={snapshot.id}>
          <Group gap="sm">
            <Text fw={600}>Снимок исходников</Text>
            <Badge color="gray">
              {snapshot.role === "retained_provenance"
                ? "Историческое основание"
                : "Основной снимок"}
            </Badge>
            <Badge color={snapshot.consistency === "verified" ? "teal" : "yellow"}>
              {snapshot.consistency === "verified"
                ? "Стабильность проверена агентом"
                : "Стабильность не проверена"}
            </Badge>
            {snapshot.dirty && <Badge color="gray">Рабочее дерево изменено</Badge>}
          </Group>
          <Text size="sm" style={wrap}>
            {snapshot.id}
          </Text>
          <Text size="sm" c="dimmed" style={wrap}>
            {snapshot.provider.name} · {snapshot.provider.version} ·{" "}
            {new Date(snapshot.capturedAt).toLocaleString("ru-RU")}
          </Text>
          <Text size="sm" style={wrap}>
            Хеш manifest: {snapshot.manifestHash}
          </Text>
        </div>
      ))}
      {data.inventory.length > 0 && (
        <Stack gap="xs" aria-label="Инвентаризация исходников">
          {data.inventory.map((item) => (
            <div key={item.category}>
              <Group gap="sm">
                <Text fw={500} size="sm">
                  {categories[item.category] ?? item.category}
                </Text>
                <Badge color={item.status === "complete" ? "teal" : "gray"}>
                  {statuses[item.status] ?? item.status}
                </Badge>
                <Text size="sm">
                  {item.knownCount}
                  {item.denominator === null ? " · всего неизвестно" : ` / ${item.denominator}`}
                </Text>
              </Group>
              <Text size="xs" c="dimmed" style={wrap}>
                Источник: {item.discoverySource}
              </Text>
              {item.reason && (
                <Text size="sm" style={wrap}>
                  {item.reason}
                </Text>
              )}
              {item.gaps.map((gap, index) => (
                <Text key={index} size="sm" c="dimmed" style={wrap}>
                  {gap}
                </Text>
              ))}
            </div>
          ))}
        </Stack>
      )}
    </Stack>
  );
}
