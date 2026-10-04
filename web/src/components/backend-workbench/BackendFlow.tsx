import type { BackendReadTarget, BackendEffectiveGraphPins } from "@/api/generated/schemas";
import {
  projectionTarget,
  projectionKey,
  projectionURLTarget,
} from "./backendEffectiveProjectionReads";
import { BackendValueInspector } from "./BackendValueInspector";
import { useContext, useRef, useState } from "react";
import {
  Alert,
  Badge,
  Button,
  Group,
  NativeSelect,
  Paper,
  Stack,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import type {
  BackendFlowResponse as BackendFlowPage,
  QueryBackendFlowRequest,
} from "@/api/generated/schemas";
import { CoverageDetails, LoadState, Pages } from "./BackendGraphInventory";
import { BackendFlowGraph } from "./BackendFlowGraph";
import { BackendFlowInspector } from "./BackendFlowInspector";
import { BackendAPIArtifactsContext } from "./BackendAPIArtifacts";
import {
  databaseButtonStyles,
  databaseStatus,
  databaseWrap,
  readDatabaseGraph,
  useDatabaseCancellation,
} from "./backendDatabaseReads";
import {
  flowPageOf,
  useFlowPage,
  type BackendSourcePin,
  type FlowSelection,
} from "./backendFlowReads";
import { flowStepContext, flowTransitionLabel } from "./backendFlowLayout";
import {
  BackendSavedViewContext,
  useWorkspaceSavedState,
  type SavedViewState,
  type SavedLayoutProps,
} from "./backendSavedViewState";

type Props = {
  projectId: string;
  revisionId?: string;
  target?: BackendReadTarget;
  pins?: BackendEffectiveGraphPins;
  pin?: BackendSourcePin;
  onPinChange?: (pin: BackendSourcePin) => void;
  onDatabaseNavigate?: (pin: BackendSourcePin) => void;
};

export function BackendFlow(props: Props) {
  const session = useContext(BackendSavedViewContext);
  return (
    <FlowWorkspace
      key={`${props.projectId}:${projectionKey(projectionTarget(props), props.pins)}:${session?.restoreGeneration ?? 0}`}
      {...props}
    />
  );
}

function FlowWorkspace({
  projectId,
  revisionId: selectedRevision,
  target: explicitTarget,
  pins,
  pin: externalPin,
  onPinChange,
  onDatabaseNavigate,
}: Props) {
  const target = projectionTarget({ revisionId: selectedRevision, target: explicitTarget, pins });
  const revisionId = pins?.baseRevisionId ?? selectedRevision ?? "";
  const [selectedValue, setSelectedValue] = useState<FlowSelection["valueRef"]>();
  const apiHost = useContext(BackendAPIArtifactsContext);
  const [localPin, setPin] = useState<BackendSourcePin>(externalPin ?? {});
  const initialPin = externalPin ?? localPin;
  const workspace = useWorkspaceSavedState<Extract<SavedViewState, { kind: "flow" }>>(
    target,
    {
      kind: "flow",
      scope: {
        ...(initialPin.entrypointId ? { entrypointId: initialPin.entrypointId } : {}),
        ...(initialPin.flowId ? { flowId: initialPin.flowId } : {}),
        ...(initialPin.dataNodeId ? { dataNodeId: initialPin.dataNodeId } : {}),
      },
      filters: { search: "", accessKind: "", reverseAccessKind: "" },
      selection: initialPin.recordId
        ? { recordType: initialPin.recordType ?? "node", id: initialPin.recordId }
        : null,
      positions: [],
      collapsedGroupIds: [],
    },
    JSON.stringify([
      externalPin?.entrypointId,
      externalPin?.flowId,
      externalPin?.dataNodeId,
      externalPin?.recordId,
      externalPin?.recordType,
    ]),
    pins?.structuralSchemaVersion === "6" ? "saved-view-v2" : undefined,
  );
  const state = workspace.state;
  const pin = {
    ...projectionURLTarget(target, revisionId),
    viewId: initialPin.viewId,
    viewVersion: initialPin.viewVersion,
    datastoreId: initialPin.datastoreId,
    facetKey: initialPin.facetKey,
    ...state.scope,
    recordId: state.selection?.id,
    recordType: state.selection?.recordType,
  };
  const [draft, setDraft] = useState(state.filters.search);
  const search = state.filters.search;
  const [cursors, setCursors] = useState([""]);
  const origin = useRef<HTMLElement | null>(null);
  const heading = useRef<HTMLHeadingElement>(null);
  const query = useFlowPage(
    projectId,
    {
      ...target,
      view: "entrypoints",
      search,
      limit: 100,
      cursor: cursors.at(-1) ?? "",
    },
    pins,
  );
  const page = flowPageOf(query.data, "entrypoints");
  const selection: FlowSelection | null = pin.recordId
    ? { type: pin.recordType ?? "node", id: pin.recordId }
    : null;
  const activeValue =
    selection?.type === "node" && selectedValue?.nodeId === selection.id
      ? selectedValue
      : undefined;
  if (selectedValue && !activeValue) setSelectedValue(undefined);
  function update(next: BackendSourcePin) {
    if (apiHost?.guard && !apiHost.guard()) return;
    const { revisionId: _previousRevision, ...details } = next;
    const value = { ...details, ...projectionURLTarget(target, revisionId) };
    setPin(value);
    workspace.onStateChange({
      ...state,
      scope: {
        ...(value.entrypointId ? { entrypointId: value.entrypointId } : {}),
        ...(value.flowId ? { flowId: value.flowId } : {}),
        ...(value.dataNodeId ? { dataNodeId: value.dataNodeId } : {}),
      },
      selection: value.recordId
        ? { recordType: value.recordType ?? "node", id: value.recordId }
        : null,
      ...(value.flowId !== state.scope.flowId ? { positions: [], collapsedGroupIds: [] } : {}),
    });
    onPinChange?.(value);
  }
  function select(value: FlowSelection, trigger?: HTMLElement) {
    setSelectedValue(value.valueRef);
    origin.current = trigger ?? origin.current;
    update({ ...pin, recordId: value.id, recordType: value.type });
  }
  function close() {
    update({ ...pin, recordId: undefined, recordType: undefined });
    (origin.current?.isConnected ? origin.current : heading.current)?.focus();
  }
  const callbacks = {
    onSelect: select,
    onFlow: (entrypointId: string | undefined, flowId: string) =>
      update({ ...pin, entrypointId, flowId, recordId: undefined, recordType: undefined }),
    onDatabase: (dataNodeId: string, datastoreId: string, facetKey: string) => {
      const value = {
        ...pin,
        ...projectionURLTarget(target, revisionId),
        dataNodeId,
        datastoreId,
        facetKey,
      };
      update(value);
      onDatabaseNavigate?.(value);
    },
  };
  return (
    <Paper withBorder p="md" style={{ minWidth: 0 }}>
      <Stack aria-label="Flow исходников" gap="md">
        <Title order={2} ref={heading} tabIndex={-1}>
          Flow
        </Title>
        {workspace.canCapture && (
          <Button
            variant="default"
            disabled={workspace.captureDisabled}
            onClick={workspace.capture}
          >
            Сохранить этот Flow
          </Button>
        )}
        <Text size="sm" c="dimmed" style={databaseWrap}>
          {target.changeProposal
            ? "Желаемая структура предложения · базовая ревизия"
            : "Статическая модель исходников · только чтение · ревизия"}{" "}
          {revisionId}
        </Text>
        <Text size="sm">
          Шаги и переходы описывают исходный код. Списки упорядочены по идентификаторам; порядок
          выполнения по ним не устанавливается.
        </Text>
        <form
          onSubmit={(event) => {
            event.preventDefault();
            workspace.onStateChange({
              ...state,
              filters: { ...state.filters, search: draft.trim() },
            });
            setCursors([""]);
          }}
        >
          <Group align="flex-end">
            <TextInput
              label="Endpoint или имя операции"
              value={draft}
              onChange={(event) => setDraft(event.currentTarget.value)}
              style={{ flex: "1 1 220px" }}
            />
            <Button type="submit">Найти точки входа</Button>
          </Group>
        </form>
        <LoadState query={query} label="точек входа" />
        {page && (
          <>
            <FlowNotice page={page} />
            <Text component="output" aria-live="polite">
              На странице: {page.entrypointItems.length} точек входа
              {page.nextCursor ? "; есть следующие страницы" : "; последняя страница"}
            </Text>
            {page.entrypointItems.length === 0 && (
              <Text>
                Точки входа не найдены в выбранной странице и области анализа; покрытие может быть
                неполным.
              </Text>
            )}
            <Stack aria-label="Точки входа Flow" gap="xs">
              {page.entrypointItems.map((item) => (
                <Paper key={item.operation.id} withBorder p="sm">
                  <Stack gap="xs">
                    <Button
                      variant="default"
                      h="auto"
                      styles={databaseButtonStyles}
                      aria-label={`Открыть Flow ${item.operation.name}`}
                      aria-pressed={pin.entrypointId === item.operation.id}
                      onClick={() =>
                        update({
                          ...pin,
                          entrypointId: item.operation.id,
                          flowId: item.flowIds[0],
                          recordId: undefined,
                          recordType: undefined,
                        })
                      }
                    >
                      {item.operation.name}
                    </Button>
                    <Group>
                      <Button
                        variant="subtle"
                        onClick={(event) =>
                          select({ type: "node", id: item.operation.id }, event.currentTarget)
                        }
                      >
                        Исходная операция
                      </Button>
                      {item.flowIds.map((flowId) => (
                        <Button
                          key={flowId}
                          variant="subtle"
                          h="auto"
                          styles={databaseButtonStyles}
                          onClick={() => callbacks.onFlow(item.operation.id, flowId)}
                        >
                          Flow {flowId}
                        </Button>
                      ))}
                      {item.handlerIds.map((id) => (
                        <Button
                          key={id}
                          variant="subtle"
                          h="auto"
                          styles={databaseButtonStyles}
                          onClick={(event) => select({ type: "node", id }, event.currentTarget)}
                        >
                          Handler {id}
                        </Button>
                      ))}
                    </Group>
                    {!item.flowIds.length && (
                      <Text size="sm">Flow этой операции не установлен.</Text>
                    )}
                    {item.unresolvedHandles.map((edge) => (
                      <Button
                        key={edge.id}
                        variant="subtle"
                        h="auto"
                        styles={databaseButtonStyles}
                        onClick={(event) =>
                          select({ type: "edge", id: edge.id }, event.currentTarget)
                        }
                      >
                        Неразрешённый handler: {edge.to}
                      </Button>
                    ))}
                    {item.limitations.map((text) => (
                      <Text key={text} size="sm" style={databaseWrap}>
                        {text}
                      </Text>
                    ))}
                  </Stack>
                </Paper>
              ))}
            </Stack>
            <Pages
              label="точки входа"
              cursors={cursors}
              setCursors={setCursors}
              busy={query.isFetching}
              next={page.nextCursor}
            />
          </>
        )}
        {pin.flowId && (
          <FlowSteps
            key={pin.flowId}
            projectId={projectId}
            revisionId={revisionId}
            target={target}
            pins={pins}
            flowId={pin.flowId}
            onSelect={select}
            positions={state.positions}
            collapsedGroupIds={state.collapsedGroupIds}
            onPositionsChange={(positions) => workspace.onStateChange({ ...state, positions })}
            onCollapsedGroupsChange={(collapsedGroupIds) =>
              workspace.onStateChange({ ...state, collapsedGroupIds })
            }
            onPreview={workspace.preview}
          />
        )}
        {pin.entrypointId && (
          <FlowAccesses
            key={`forward:${pin.entrypointId}`}
            projectId={projectId}
            revisionId={revisionId}
            target={target}
            pins={pins}
            selector={{ entrypointId: pin.entrypointId }}
            kind={state.filters.accessKind}
            onKindChange={(accessKind) =>
              workspace.onStateChange({ ...state, filters: { ...state.filters, accessKind } })
            }
            {...callbacks}
          />
        )}
        {pin.dataNodeId && (
          <FlowAccesses
            key={`reverse:${pin.dataNodeId}`}
            projectId={projectId}
            revisionId={revisionId}
            target={target}
            pins={pins}
            selector={{ dataNodeId: pin.dataNodeId }}
            kind={state.filters.reverseAccessKind}
            onKindChange={(reverseAccessKind) =>
              workspace.onStateChange({
                ...state,
                filters: { ...state.filters, reverseAccessKind },
              })
            }
            {...callbacks}
          />
        )}
        {selection && (activeValue?.kind === "column" || activeValue?.kind === "event_field") ? (
          <BackendValueInspector
            key={JSON.stringify(activeValue)}
            projectId={projectId}
            revisionId={revisionId}
            target={target}
            pins={pins}
            value={activeValue}
            onClose={close}
          />
        ) : (
          selection && (
            <BackendFlowInspector
              key={`${selection.type}:${selection.id}`}
              projectId={projectId}
              revisionId={revisionId}
              target={target}
              pins={pins}
              selection={selection}
              selectedValue={activeValue}
              onSelect={select}
              onClose={close}
            />
          )
        )}
      </Stack>
    </Paper>
  );
}

function FlowNotice({ page }: { page: BackendFlowPage }) {
  return (
    <Stack gap="xs">
      <Text size="sm" style={databaseWrap}>
        Покрытие инвентаря ревизии: {databaseStatus(page.coverage.coverage.status)} · объектов{" "}
        {page.coverage.coverage.knownObjects}
        {page.coverage.coverage.denominator === null
          ? "; всего неизвестно"
          : ` / ${page.coverage.coverage.denominator}`}
      </Text>
      <details>
        <summary style={{ ...databaseWrap, cursor: "pointer", padding: "4px 0" }}>
          Подробности инвентаря ревизии
        </summary>
        <CoverageDetails data={page.coverage} />
      </details>
      {page.truncated && (
        <Alert color="yellow" aria-live="polite">
          Поиск ограничен: {page.truncationReasons.join(", ")}. Результат неполон; отсутствие записи
          не доказывает отсутствие доступа.
        </Alert>
      )}
      {page.limitations.map((text) => (
        <Text key={text} size="sm" style={databaseWrap}>
          {text}
        </Text>
      ))}
    </Stack>
  );
}

function FlowSteps({
  projectId,
  revisionId,
  target: explicitTarget,
  pins,
  flowId,
  onSelect,
  positions,
  collapsedGroupIds,
  onPositionsChange,
  onCollapsedGroupsChange,
  onPreview,
}: SavedLayoutProps & {
  onPreview?: (value: boolean) => void;
  projectId: string;
  revisionId?: string;
  target?: BackendReadTarget;
  pins?: BackendEffectiveGraphPins;
  flowId: string;
  onSelect: (selection: FlowSelection, trigger?: HTMLElement) => void;
}) {
  const target = projectionTarget({ revisionId, target: explicitTarget, pins });
  const [stepCursors, setStepCursors] = useState([""]);
  const [transitionCursors, setTransitionCursors] = useState([""]);
  const steps = useFlowPage(
    projectId,
    {
      ...target,
      view: "steps",
      flowId,
      limit: 500,
      cursor: stepCursors.at(-1) ?? "",
    },
    pins,
  );
  const transitions = useFlowPage(
    projectId,
    {
      ...target,
      view: "transitions",
      flowId,
      limit: 500,
      cursor: transitionCursors.at(-1) ?? "",
    },
    pins,
  );
  const stepPage = flowPageOf(steps.data, "steps");
  const transitionPage = flowPageOf(transitions.data, "transitions");
  const transactions = new Map<string, string[]>();
  for (const node of stepPage?.stepItems ?? []) {
    if (
      "transactionContext" in node.attributes &&
      node.attributes.transactionContext.status === "known"
    ) {
      const id = node.attributes.transactionContext.transactionId;
      transactions.set(id, [...(transactions.get(id) ?? []), node.id]);
    }
  }
  const groupKey = ["backend-flow-transactions", projectId, projectionKey(target, pins), flowId];
  useDatabaseCancellation(groupKey);
  const groupNames = useQuery({
    queryKey: groupKey,
    enabled: transactions.size > 0,
    retry: false,
    staleTime: Infinity,
    queryFn: ({ signal }) =>
      readDatabaseGraph(
        projectId,
        { ...target, recordType: "nodes", kind: "transaction", parentId: flowId },
        signal,
        pins,
      ),
  });
  return (
    <Stack gap="md">
      <Group>
        <Title order={3}>Шаги Flow</Title>
        <Button
          variant="subtle"
          h="auto"
          styles={databaseButtonStyles}
          onClick={(event) => onSelect({ type: "node", id: flowId }, event.currentTarget)}
        >
          Описание Flow {flowId}
        </Button>
      </Group>
      <LoadState query={steps} label="шагов" />
      {stepPage && (
        <>
          <FlowNotice page={stepPage} />
          <Text component="output" aria-live="polite">
            На странице: {stepPage.stepItems.length} шагов
            {stepPage.nextCursor ? "; есть следующие страницы" : "; последняя страница"}
          </Text>
          {transactions.size > 0 && (
            <Stack aria-label="Локальные группы транзакций" gap="xs">
              <Text size="sm">
                Локальные группы текущей страницы flow; границы соединения и завершения доступны в
                исходном объекте транзакции.
              </Text>
              {[...transactions].map(([id, steps], index) => (
                <Group key={id}>
                  <Button
                    variant="subtle"
                    h="auto"
                    styles={databaseButtonStyles}
                    onClick={(event) => onSelect({ type: "node", id }, event.currentTarget)}
                  >
                    {groupNames.data?.nodes.find((node) => node.id === id)?.name ??
                      `Локальная транзакция ${index + 1}`}
                    <Text component="span" size="xs" c="dimmed">
                      {" "}
                      · {id}
                    </Text>
                  </Button>
                  <Text size="sm">Шагов на странице: {steps.length}</Text>
                  <Button
                    variant="default"
                    h="auto"
                    py="sm"
                    styles={{
                      ...databaseButtonStyles,
                      inner: { minWidth: 0, maxWidth: "100%" },
                      label: { ...databaseWrap, whiteSpace: "normal" },
                    }}
                    aria-expanded={!collapsedGroupIds.includes(id)}
                    onClick={() =>
                      onCollapsedGroupsChange(
                        collapsedGroupIds.includes(id)
                          ? collapsedGroupIds.filter((value) => value !== id)
                          : [...collapsedGroupIds, id],
                      )
                    }
                  >
                    {collapsedGroupIds.includes(id) ? "Развернуть" : "Свернуть"} транзакцию{" "}
                    {groupNames.data?.nodes.find((node) => node.id === id)?.name ??
                      `Локальная транзакция ${index + 1}`}{" "}
                    · {id}
                  </Button>
                </Group>
              ))}
            </Stack>
          )}
          {stepPage.stepItems.length === 0 && (
            <Text>Шаги в этой области не найдены; анализ может быть неполным.</Text>
          )}
          <Stack aria-label="Шаги Flow" gap="xs">
            {stepPage.stepItems.map((node) => (
              <Paper key={node.id} withBorder p="sm">
                <Stack gap="xs">
                  <Button
                    variant="default"
                    h="auto"
                    py="sm"
                    styles={databaseButtonStyles}
                    aria-label={`Открыть шаг ${node.name}`}
                    onClick={(event) =>
                      onSelect({ type: "node", id: node.id }, event.currentTarget)
                    }
                  >
                    {node.name} ·{" "}
                    {"stepKind" in node.attributes ? node.attributes.stepKind : node.kind} ·{" "}
                    {"analysisStatus" in node.attributes
                      ? databaseStatus(String(node.attributes.analysisStatus))
                      : "Неизвестно"}
                  </Button>
                  {flowStepContext(node).map((text) => (
                    <Text key={text} size="sm" style={databaseWrap}>
                      {text}
                    </Text>
                  ))}
                  {"gaps" in node.attributes &&
                    node.attributes.gaps.map((gap) => (
                      <Text key={gap} size="sm" style={databaseWrap}>
                        {gap}
                      </Text>
                    ))}
                </Stack>
              </Paper>
            ))}
          </Stack>
          <Pages
            label="шаги"
            cursors={stepCursors}
            setCursors={setStepCursors}
            busy={steps.isFetching}
            next={stepPage.nextCursor}
          />
        </>
      )}
      <Title order={3}>Переходы Flow</Title>
      <LoadState query={transitions} label="переходов" />
      {transitionPage && (
        <>
          <FlowNotice page={transitionPage} />
          <Text component="output" aria-live="polite">
            На странице: {transitionPage.transitionItems.length} переходов
            {transitionPage.nextCursor ? "; есть следующие страницы" : "; последняя страница"}
          </Text>
          {transitionPage.transitionItems.length === 0 && (
            <Text>Переходы в этой области не найдены; анализ может быть неполным.</Text>
          )}
          <Stack aria-label="Переходы Flow" gap="xs">
            {transitionPage.transitionItems.map((edge) => (
              <Paper key={edge.id} withBorder p="sm">
                <Stack gap="xs">
                  <Button
                    variant="subtle"
                    h="auto"
                    styles={databaseButtonStyles}
                    onClick={(event) =>
                      onSelect({ type: "edge", id: edge.id }, event.currentTarget)
                    }
                  >
                    Переход {flowTransitionLabel(edge)} · {edge.id}
                  </Button>
                  <Group>
                    {[edge.from, edge.to].map((id) => (
                      <Button
                        key={id}
                        variant="subtle"
                        h="auto"
                        styles={databaseButtonStyles}
                        onClick={(event) => onSelect({ type: "node", id }, event.currentTarget)}
                      >
                        Шаг {id}
                        {!stepPage?.stepItems.some((node) => node.id === id)
                          ? " · вне текущей страницы"
                          : ""}
                      </Button>
                    ))}
                  </Group>
                </Stack>
              </Paper>
            ))}
          </Stack>
          <Pages
            label="переходы"
            cursors={transitionCursors}
            setCursors={setTransitionCursors}
            busy={transitions.isFetching}
            next={transitionPage.nextCursor}
          />
        </>
      )}
      {stepPage && transitionPage && (
        <BackendFlowGraph
          nodes={stepPage.stepItems}
          edges={transitionPage.transitionItems}
          onSelect={onSelect}
          positions={positions}
          collapsedGroupIds={collapsedGroupIds}
          onPositionsChange={onPositionsChange}
          onCollapsedGroupsChange={onCollapsedGroupsChange}
          onPreview={onPreview}
        />
      )}
    </Stack>
  );
}

function FlowAccesses({
  projectId,
  revisionId,
  target: explicitTarget,
  pins,
  selector,
  onSelect,
  onFlow,
  onDatabase,
  kind,
  onKindChange,
}: {
  kind: "" | "reads" | "writes" | "deletes";
  onKindChange: (kind: "" | "reads" | "writes" | "deletes") => void;
  projectId: string;
  revisionId?: string;
  target?: BackendReadTarget;
  pins?: BackendEffectiveGraphPins;
  selector: { entrypointId: string } | { dataNodeId: string };
  onSelect: (selection: FlowSelection, trigger?: HTMLElement) => void;
  onFlow: (entrypointId: string | undefined, flowId: string) => void;
  onDatabase: (dataNodeId: string, datastoreId: string, facetKey: string) => void;
}) {
  const target = projectionTarget({ revisionId, target: explicitTarget, pins });
  const [cursors, setCursors] = useState([""]);
  const input: QueryBackendFlowRequest = {
    ...target,
    view: "accesses",
    ...selector,
    ...(kind ? { accessKind: kind } : {}),
    limit: 100,
    cursor: cursors.at(-1) ?? "",
  };
  const query = useFlowPage(projectId, input, pins);
  const page = flowPageOf(query.data, "accesses");
  const reverse = "dataNodeId" in selector;
  return (
    <Stack
      gap="sm"
      aria-label={reverse ? "Обратные доступы к данным" : "Доступы endpoint к данным"}
    >
      <Title order={3}>
        {reverse ? `Чтения и записи ${selector.dataNodeId}` : "Доступы к данным endpoint"}
      </Title>
      <NativeSelect
        label={reverse ? "Тип обратного доступа" : "Тип доступа endpoint"}
        value={kind}
        data={[
          { value: "", label: "Все доступы" },
          { value: "reads", label: "Чтения" },
          { value: "writes", label: "Записи" },
          { value: "deletes", label: "Удаления" },
        ]}
        onChange={(event) => {
          onKindChange(event.currentTarget.value as typeof kind);
          setCursors([""]);
        }}
      />
      <LoadState query={query} label={reverse ? "обратных доступов" : "доступов endpoint"} />
      {page && (
        <>
          <FlowNotice page={page} />
          <Text component="output" aria-live="polite">
            На странице: {page.accessItems.length} доступов
            {page.nextCursor ? "; есть следующие страницы" : "; последняя страница"}
          </Text>
          {page.accessItems.length === 0 && (
            <Text>
              Доступы в этой области не найдены. Неполное покрытие и ограничения поиска сохраняют
              неизвестность.
            </Text>
          )}
          {page.accessItems.map((item) => (
            <Paper
              key={`${item.accessEdgeId}:${item.entrypointId ?? "unattached"}`}
              withBorder
              p="sm"
            >
              <Stack gap="xs">
                <Group>
                  <Badge color={item.relation === "possible" ? "yellow" : "blue"}>
                    {item.relation === "possible" ? "Возможная связь" : "Прямая связь"}
                  </Badge>
                  <Badge>{databaseStatus(item.status)}</Badge>
                  <Text size="sm">
                    {item.accessKind} · {item.accessMode}
                  </Text>
                </Group>
                {!item.entrypointId && <Text>Доступ не привязан к endpoint; путь неизвестен.</Text>}
                <Group>
                  <Button
                    variant="subtle"
                    h="auto"
                    styles={databaseButtonStyles}
                    onClick={(event) =>
                      onSelect({ type: "node", id: item.queryId }, event.currentTarget)
                    }
                  >
                    Исходный запрос {item.queryId}
                  </Button>
                  <Button
                    variant="subtle"
                    h="auto"
                    styles={databaseButtonStyles}
                    onClick={(event) =>
                      onSelect({ type: "edge", id: item.accessEdgeId }, event.currentTarget)
                    }
                  >
                    {item.status === "desired" ? "Основания базовых объектов" : "Основания доступа"}
                  </Button>
                  <Button
                    variant="default"
                    h="auto"
                    styles={databaseButtonStyles}
                    onClick={() => onDatabase(item.targetId, item.datastoreId, item.facetKey)}
                  >
                    Открыть объект базы данных {item.targetId}
                  </Button>
                  {item.flowId && (
                    <Button
                      variant="subtle"
                      h="auto"
                      styles={databaseButtonStyles}
                      onClick={() => onFlow(item.entrypointId ?? undefined, item.flowId!)}
                    >
                      Открыть связанный Flow {item.flowId}
                    </Button>
                  )}
                </Group>
                {item.pathNodeIds.length > 0 && (
                  <details>
                    <summary>Один подтверждающий путь · {item.pathEdgeIds.length} связей</summary>
                    <Stack gap="xs">
                      {item.pathNodeIds.map((id, index) => (
                        <Button
                          key={`${index}:${id}`}
                          variant="subtle"
                          h="auto"
                          styles={databaseButtonStyles}
                          onClick={(event) => onSelect({ type: "node", id }, event.currentTarget)}
                        >
                          Объект пути {id}
                        </Button>
                      ))}
                      {item.pathEdgeIds.map((id, index) => (
                        <Button
                          key={`${index}:${id}`}
                          variant="subtle"
                          h="auto"
                          styles={databaseButtonStyles}
                          onClick={(event) => onSelect({ type: "edge", id }, event.currentTarget)}
                        >
                          Связь пути {id}
                        </Button>
                      ))}
                    </Stack>
                  </details>
                )}
                {item.limitations.map((text) => (
                  <Text key={text} size="sm" style={databaseWrap}>
                    {text}
                  </Text>
                ))}
              </Stack>
            </Paper>
          ))}
          <Pages
            label={reverse ? "обратные доступы" : "доступы endpoint"}
            cursors={cursors}
            setCursors={setCursors}
            busy={query.isFetching}
            next={page.nextCursor}
          />
        </>
      )}
    </Stack>
  );
}
