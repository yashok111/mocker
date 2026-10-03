import { getBackendNode } from "@/api/generated/backend-projects/backend-projects";
import { useEffect, useRef, useState } from "react";
import {
  Alert,
  Badge,
  Button,
  Group,
  NativeSelect,
  NumberInput,
  Paper,
  Stack,
  Text,
  Title,
} from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import type { BackendLineageValueRef } from "@/api/generated/schemas";
import { LoadState } from "./BackendGraphInventory";
import { DatabaseEvidence } from "./BackendDatabaseInspector";
import {
  databaseButtonStyles,
  databaseStatus,
  databaseWrap,
  useDatabaseCancellation,
} from "./backendDatabaseReads";
import { lineageRefKey, lineageRefLabel, readLineagePage } from "./backendLineageReads";
export type LineageProps = {
  projectId: string;
  revisionId: string;
  seed: BackendLineageValueRef;
  onClose: () => void;
  onValueSelect: (ref: BackendLineageValueRef) => void;
  initialDirection?: "forward" | "reverse";
};
export function BackendLineage(props: LineageProps) {
  return (
    <LineagePanel
      key={JSON.stringify([props.projectId, props.revisionId, lineageRefKey(props.seed)])}
      {...props}
    />
  );
}
function LineagePanel({
  projectId,
  revisionId,
  seed,
  onClose,
  onValueSelect,
  initialDirection = "forward",
}: LineageProps) {
  const [direction, setDirection] = useState(initialDirection);
  const [depth, setDepth] = useState(8);
  const [cursors, setCursors] = useState([""]);
  const [separate, setSeparate] = useState<BackendLineageValueRef | null>(null);
  const active = separate ?? seed;
  const input = {
    revisionId,
    seed: active,
    direction,
    maxDepth: depth,
    limit: 50,
    cursor: cursors.at(-1) ?? "",
  };
  const key = ["backend-lineage", projectId, revisionId, JSON.stringify(input)];
  useDatabaseCancellation(key);
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    heading.current?.focus();
  }, []);
  const query = useQuery({
    queryKey: key,
    staleTime: Infinity,
    retry: false,
    queryFn: ({ signal }) => readLineagePage(projectId, input, signal),
  });
  const page = query.data;
  function value(ref: BackendLineageValueRef, boundary = false) {
    return (
      <LineageValue
        key={lineageRefKey(ref)}
        projectId={projectId}
        revisionId={revisionId}
        value={ref}
        onValueSelect={onValueSelect}
        onSeparate={
          boundary
            ? () => {
                setSeparate(ref);
                setCursors([""]);
              }
            : undefined
        }
      />
    );
  }
  return (
    <Paper
      component="section"
      withBorder
      p="md"
      aria-label="Происхождение значения"
      style={{ minWidth: 0 }}
    >
      <Stack>
        <Group justify="space-between">
          <Title order={3} ref={heading} tabIndex={-1}>
            Происхождение значения
          </Title>
          <Button variant="default" h="auto" styles={databaseButtonStyles} onClick={onClose}>
            Закрыть происхождение значения
          </Button>
        </Group>
        <Text size="xs" style={databaseWrap}>
          Ревизия: {revisionId} · {lineageRefLabel(active)}
        </Text>
        {separate && (
          <Alert color="yellow">
            Отдельный запрос за границей не доказывает сквозной путь.
            <Button
              variant="subtle"
              h="auto"
              styles={databaseButtonStyles}
              onClick={() => {
                setSeparate(null);
                setCursors([""]);
              }}
            >
              Вернуться к исходному значению
            </Button>
          </Alert>
        )}
        <Group align="flex-end">
          <NativeSelect
            label="Направление происхождения"
            value={direction}
            data={[
              { value: "forward", label: "Куда передаётся значение" },
              { value: "reverse", label: "Происхождение значения" },
            ]}
            onChange={(event) => {
              setDirection(event.currentTarget.value as "forward" | "reverse");
              setCursors([""]);
            }}
            style={{ minWidth: 0, maxWidth: "100%" }}
          />
          <NumberInput
            label="Глубина происхождения"
            min={1}
            max={32}
            allowDecimal={false}
            value={depth}
            onChange={(next) => {
              if (typeof next === "number" && Number.isInteger(next) && next >= 1 && next <= 32) {
                setDepth(next);
                setCursors([""]);
              }
            }}
            w={140}
          />
        </Group>
        <LoadState query={query} label="происхождения значения" />
        {page && (
          <>
            <Text>
              Загружено соответствий на странице: {page.items.length}. Изучено:{" "}
              {page.examinedMappingCount}; значений: {page.visitedValueCount}.
            </Text>
            <Text style={databaseWrap}>
              Покрытие ревизии: {databaseStatus(page.coverage.coverage.status)} · объектов{" "}
              {page.coverage.coverage.knownObjects}; всего{" "}
              {page.coverage.coverage.denominator ?? "неизвестно"}.
            </Text>
            <Text size="sm">
              Статические зависимости исходника; свидетельство — один объясняющий путь, не трасса
              исполнения.
            </Text>
            {page.truncated && (
              <Alert color="yellow">Обход ограничен: {page.truncationReasons.join(", ")}</Alert>
            )}
            {page.limitations.map((text, i) => (
              <Text key={i} size="sm" style={databaseWrap}>
                {text}
              </Text>
            ))}
            {page.items.length === 0 && (
              <Text>
                Нет импортированных соответствий в этой области. Наличие других зависимостей
                неизвестно.
              </Text>
            )}
            {page.items.map((item) => {
              const attrs = item.mapping.attributes;
              if (!("sources" in attrs) || !("destination" in attrs) || !("transform" in attrs))
                return (
                  <Alert key={item.mapping.id} color="red">
                    Неполное соответствие {item.mapping.id}
                  </Alert>
                );
              return (
                <Stack
                  key={item.mapping.id}
                  gap="xs"
                  style={{
                    borderTop: "1px solid var(--mantine-color-default-border)",
                    paddingTop: 16,
                    minWidth: 0,
                  }}
                >
                  <Title order={4} style={databaseWrap}>
                    {item.mapping.name}
                  </Title>
                  <Group>
                    <Badge>{databaseStatus(item.status)}</Badge>
                    <Badge>
                      {item.expansion === "boundary" ? "Граница обхода" : "Продолжение"}
                    </Badge>
                    {item.requiresReview && <Badge color="yellow">Требует проверки</Badge>}
                    <Text size="sm">Глубина {item.depth}</Text>
                  </Group>
                  <Text fw={600}>Входы в объявленном порядке</Text>
                  <ol style={{ paddingLeft: 20, margin: 0 }}>
                    {attrs.sources.map((ref, i) => (
                      <li key={i}>{value(ref, item.expansion === "boundary")}</li>
                    ))}
                  </ol>
                  {attrs.sources.length === 0 && (
                    <Text>
                      {attrs.transform.kind === "constant"
                        ? "Константа: входов нет"
                        : "Входы не установлены"}
                    </Text>
                  )}
                  <Text fw={600}>Назначение</Text>
                  {value(attrs.destination, item.expansion === "boundary")}
                  <Text fw={600}>Преобразование · {attrs.transform.kind}</Text>
                  <Text style={databaseWrap}>{attrs.transform.description}</Text>
                  {attrs.transform.redacted && <Text size="sm">Чувствительные детали скрыты</Text>}
                  <Text>Анализ: {databaseStatus(attrs.analysisStatus)}</Text>
                  {[...attrs.gaps, ...item.reasons].map((text, i) => (
                    <Text key={i} size="sm" style={databaseWrap}>
                      {text}
                    </Text>
                  ))}
                  <Text size="sm" style={databaseWrap}>
                    Через: {lineageRefLabel(item.via)}
                  </Text>
                  <Text size="sm" style={databaseWrap}>
                    Свидетельство: {item.witnessMappingIds.join(" → ")}
                  </Text>
                  <details>
                    <summary>Свойства и происхождение соответствия</summary>
                    <Text size="xs" style={databaseWrap}>
                      {JSON.stringify(item.mapping)}
                    </Text>
                  </details>
                  <Text size="sm" style={databaseWrap}>
                    Основания: {item.mapping.evidenceIds.join(", ") || "не указаны"}
                  </Text>
                  <DatabaseEvidence
                    context={{ projectId, revisionId, datastoreId: "", facetKey: "" }}
                    subjectId={item.mapping.id}
                  />
                </Stack>
              );
            })}
            <Group>
              <Button
                variant="default"
                h="auto"
                styles={databaseButtonStyles}
                disabled={cursors.length === 1 || query.isFetching}
                onClick={() => setCursors((old) => old.slice(0, -1))}
              >
                Предыдущая страница происхождения
              </Button>
              <Text size="sm">Страница {cursors.length}</Text>
              <Button
                variant="default"
                h="auto"
                styles={databaseButtonStyles}
                disabled={!page.nextCursor || query.isFetching || cursors.includes(page.nextCursor)}
                onClick={() => {
                  if (page.nextCursor && !cursors.includes(page.nextCursor))
                    setCursors((old) => [...old, page.nextCursor]);
                }}
              >
                Следующая страница происхождения
              </Button>
            </Group>
          </>
        )}
      </Stack>
    </Paper>
  );
}

function LineageValue({
  projectId,
  revisionId,
  value,
  onValueSelect,
  onSeparate,
}: {
  projectId: string;
  revisionId: string;
  value: BackendLineageValueRef;
  onValueSelect: (ref: BackendLineageValueRef) => void;
  onSeparate?: () => void;
}) {
  const key = ["backend-lineage-value", projectId, revisionId, value.nodeId];
  useDatabaseCancellation(key);
  const query = useQuery({
    queryKey: key,
    retry: false,
    staleTime: Infinity,
    queryFn: async ({ signal }) => {
      const response = await getBackendNode(projectId, revisionId, value.nodeId, { signal });
      signal.throwIfAborted();
      if (response.status !== 200 || response.data.id !== value.nodeId)
        throw new Error("Значение отсутствует в выбранном снимке");
      return response.data;
    },
  });
  const node = query.data;
  let label = node?.name ?? value.nodeId;
  if (value.kind === "column") label += ` · колонка · ${value.facetKey}`;
  if (value.kind === "port") label += ` · ${value.collection} · ${value.portKey}`;
  if (value.kind === "api_field") label += " · поле API";
  if (value.kind === "event_field")
    label += ` · поле события · ${value.endpointId} · маршрут ${value.routeId}`;
  return (
    <Stack gap={2}>
      <Group>
        <Button
          variant="subtle"
          h="auto"
          styles={databaseButtonStyles}
          aria-label={`Открыть значение ${lineageRefLabel(value)}`}
          onClick={() => onValueSelect(value)}
        >
          {label}
        </Button>
        {onSeparate && (
          <Button
            variant="default"
            h="auto"
            styles={databaseButtonStyles}
            aria-label={`Начать отдельный запрос ${lineageRefLabel(value)}`}
            onClick={onSeparate}
          >
            Начать отдельный запрос {label}
          </Button>
        )}
      </Group>
      {node && (
        <Text size="xs" c="dimmed" style={databaseWrap}>
          {lineageRefLabel(value)}
        </Text>
      )}
    </Stack>
  );
}
