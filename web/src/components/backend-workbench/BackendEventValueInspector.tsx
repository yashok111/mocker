import { lazy, Suspense, useEffect, useRef, useState } from "react";
import { Alert, Button, Code, Group, Paper, Stack, Text, Title } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import {
  getBackendRevision,
  getBackendNode,
} from "@/api/generated/backend-projects/backend-projects";
import type { BackendLineageValueRef } from "@/api/generated/schemas";
import { LoadState, Pages } from "./BackendGraphInventory";
import { DatabaseEvidence } from "./BackendDatabaseInspector";
import { BackendLineageActions } from "./BackendLineageActions";
import { BackendFlowInspector } from "./BackendFlowInspector";
import { BackendValueInspector } from "./BackendValueInspector";
import {
  databaseButtonStyles,
  databaseWrap,
  useDatabaseCancellation,
} from "./backendDatabaseReads";
import { eventsItemKey, eventsPayload, eventsReason, readEventsPage } from "./backendEventsReads";
import { lineageRefKey, lineageRefLabel } from "./backendLineageReads";
import type { BackendSourcePin, FlowSelection } from "./backendFlowReads";
import { useBackendAPIDeparture } from "./useBackendAPIDeparture";
const BackendFlow = lazy(() =>
  import("./BackendFlow").then((module) => ({ default: module.BackendFlow })),
);

export function BackendEventValueInspector({
  projectId,
  revisionId,
  value,
  onClose,
  onValueSelect,
}: {
  projectId: string;
  revisionId: string;
  value: Extract<BackendLineageValueRef, { kind: "event_field" }>;
  onClose: () => void;
  onValueSelect: (ref: BackendLineageValueRef) => void;
}) {
  const key = ["backend-event-value", projectId, revisionId, lineageRefKey(value)];
  useDatabaseCancellation(key);
  const heading = useRef<HTMLHeadingElement>(null);
  const origin = useRef<HTMLElement | null>(null);
  const [selection, setSelection] = useState<FlowSelection>();
  const [flow, setFlow] = useState<BackendSourcePin>();
  const [cursors, setCursors] = useState([""]);
  const depart = useBackendAPIDeparture();
  useEffect(() => {
    heading.current?.focus();
  }, [value]);
  const source = useQuery({
    queryKey: [...key, "source"],
    staleTime: Infinity,
    retry: false,
    queryFn: async ({ signal }) => {
      const [revision, node] = await Promise.all([
        getBackendRevision(projectId, revisionId, { signal }),
        getBackendNode(projectId, revisionId, value.nodeId, { signal }),
      ]);
      signal.throwIfAborted();
      if (
        revision.status !== 200 ||
        revision.data.id !== revisionId ||
        revision.data.projectId !== projectId ||
        revision.data.schemaVersion !== "5" ||
        node.status !== 200 ||
        node.data.id !== value.nodeId ||
        node.data.kind !== "event_field"
      )
        throw new Error("Значение события отсутствует в выбранном source5");
      return { revision: revision.data, node: node.data };
    },
  });
  const routeKey = [
    ...key,
    "routes",
    source.data?.revision.semanticHash ?? "",
    cursors.at(-1) ?? "",
  ];
  useDatabaseCancellation(routeKey);
  const routes = useQuery({
    queryKey: routeKey,
    enabled: !!source.data,
    staleTime: Infinity,
    retry: false,
    queryFn: ({ signal }) =>
      readEventsPage(
        projectId,
        {
          revisionId,
          view: "routes",
          seedNodeId: value.endpointId,
          limit: 100,
          cursor: cursors.at(-1) ?? "",
        },
        source.data!.revision.semanticHash,
        signal,
      ),
  });
  const exact = routes.data?.items.filter((item) => {
    const r = eventsPayload(item).references;
    return (
      r.messageId === source.data?.node.parentId &&
      ((r.producerId === value.endpointId && r.emitsEdgeId === value.routeId) ||
        (r.consumerId === value.endpointId && r.deliveryEdgeId === value.routeId))
    );
  });
  function record(id: string, type: FlowSelection["type"], trigger: HTMLElement) {
    depart(() => {
      origin.current = trigger;
      setSelection({ id, type });
    });
  }
  function closeRecord() {
    depart(() => {
      setSelection(undefined);
      requestAnimationFrame(() =>
        (origin.current?.isConnected ? origin.current : heading.current)?.focus(),
      );
    });
  }
  return (
    <Paper
      component="section"
      withBorder
      p="md"
      style={{ minWidth: 0 }}
      aria-label="Значение события"
    >
      <Stack>
        <Group justify="space-between">
          <Title order={3} tabIndex={-1} ref={heading} style={databaseWrap}>
            {source.data?.node.name ?? "Значение события"}
          </Title>
          <Button variant="default" onClick={() => depart(onClose)}>
            Закрыть значение события
          </Button>
        </Group>
        <Text size="sm" style={databaseWrap}>
          Источник: {revisionId} · {lineageRefLabel(value)}
        </Text>
        <Text style={databaseWrap}>
          Маршрут значения: {value.routeId} · endpoint: {value.endpointId}
        </Text>
        <Group>
          <Button
            variant="subtle"
            h="auto"
            styles={databaseButtonStyles}
            onClick={(event) => record(value.endpointId, "node", event.currentTarget)}
          >
            Открыть endpoint {value.endpointId}
          </Button>
          <Button
            variant="subtle"
            h="auto"
            styles={databaseButtonStyles}
            onClick={(event) => record(value.routeId, "edge", event.currentTarget)}
          >
            Открыть маршрут значения {value.routeId}
          </Button>
        </Group>
        <BackendLineageActions
          projectId={projectId}
          revisionId={revisionId}
          seed={value}
          onValueSelect={onValueSelect}
        />
        <LoadState query={source} label="значения события" />
        {source.data && <LoadState query={routes} label="точного маршрута значения" />}
        {source.data && (
          <>
            <Text size="sm" style={databaseWrap}>
              Сообщение: {source.data.node.parentId}
            </Text>
            <details>
              <summary>Структура поля события</summary>
              <Code block style={databaseWrap}>
                {JSON.stringify(source.data.node.attributes, null, 2)}
              </Code>
            </details>
            <DatabaseEvidence
              context={{ projectId, revisionId, datastoreId: "", facetKey: "" }}
              subjectId={value.nodeId}
            />
          </>
        )}
        {exact?.map((item) => {
          const payload = eventsPayload(item),
            refs = payload.references;
          return (
            <Stack key={eventsItemKey(item)} gap="xs">
              <Text style={databaseWrap}>
                Отправитель {refs.producerId ?? "неизвестен"} → канал{" "}
                {refs.channelId ?? "неизвестен"} → получатель {refs.consumerId ?? "неизвестен"}
              </Text>
              <Group>
                {refs.producerId && (
                  <Button
                    variant="default"
                    h="auto"
                    styles={databaseButtonStyles}
                    onClick={(event) => record(refs.producerId!, "node", event.currentTarget)}
                  >
                    Отправитель {refs.producerId}
                  </Button>
                )}
                {refs.consumerId && (
                  <Button
                    variant="default"
                    h="auto"
                    styles={databaseButtonStyles}
                    onClick={(event) => record(refs.consumerId!, "node", event.currentTarget)}
                  >
                    Получатель {refs.consumerId}
                  </Button>
                )}
                {payload.dispatch.flatMap((dispatch) =>
                  dispatch.flowIds.map((flowId) => (
                    <Button
                      key={`${dispatch.handlerId}:${flowId}`}
                      variant="default"
                      h="auto"
                      styles={databaseButtonStyles}
                      onClick={() =>
                        depart(() =>
                          setFlow({
                            revisionId,
                            entrypointId: refs.consumerId,
                            flowId,
                            recordId: dispatch.handlerId,
                            recordType: "node",
                          }),
                        )
                      }
                    >
                      Открыть Flow {flowId}
                    </Button>
                  )),
                )}
              </Group>
              {item.kind === "boundary" && <Text style={databaseWrap}>{item.boundary.reason}</Text>}
            </Stack>
          );
        })}
        {routes.data && (
          <>
            <Text size="sm">
              Показана выбранная страница маршрутов.{" "}
              {routes.data.nextCursor || routes.data.truncated
                ? "Перечисление ограничено; точный маршрут может находиться за этой страницей."
                : "Отсутствующий маршрут остаётся неизвестным в области анализа."}
            </Text>
            {routes.data.truncated && (
              <Alert color="yellow">
                Чтение ограничено: {routes.data.truncationReasons.map(eventsReason).join("; ")}
              </Alert>
            )}
            <Pages
              label="маршруты значения"
              cursors={cursors}
              setCursors={(update) => depart(() => setCursors(update))}
              busy={routes.isFetching}
              next={routes.data.nextCursor}
            />
          </>
        )}
        {selection?.valueRef ? (
          <BackendValueInspector
            key={lineageRefKey(selection.valueRef)}
            projectId={projectId}
            revisionId={revisionId}
            value={selection.valueRef}
            onClose={closeRecord}
          />
        ) : (
          selection && (
            <BackendFlowInspector
              key={JSON.stringify(selection)}
              projectId={projectId}
              revisionId={revisionId}
              selection={selection}
              onSelect={(next) => depart(() => setSelection(next))}
              onClose={closeRecord}
            />
          )
        )}
        {flow && (
          <Suspense
            fallback={
              <Text component="output" aria-live="polite">
                Загружаем Flow
              </Text>
            }
          >
            <BackendFlow
              projectId={projectId}
              revisionId={revisionId}
              pin={flow}
              onPinChange={setFlow}
            />
          </Suspense>
        )}
      </Stack>
    </Paper>
  );
}
