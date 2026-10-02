import { useContext, useMemo, useRef, useState } from "react";
import {
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
import { queryBackendDatabase } from "@/api/generated/backend-projects/backend-projects";
import type { BackendNode } from "@/api/generated/schemas";
import { CoverageDetails, LoadState, Pages } from "./BackendGraphInventory";
import { BackendDatabaseInspector } from "./BackendDatabaseInspector";
import { BackendDatabaseGraph } from "./BackendDatabaseGraph";
import { BackendDatabaseProposal, type ProposalView } from "./BackendDatabaseProposal";
import {
  databaseButtonStyles,
  databaseKey,
  databaseTarget,
  databaseStatus,
  databaseWrap,
  datastoreScope,
  readDatabaseGraph,
  relationalFacets,
  useDatabaseCancellation,
  type DatabaseContext,
  type DatabaseSelection,
} from "./backendDatabaseReads";
import { cardinalityText } from "./backendDatabaseLayout";
import { useBackendAPIDeparture } from "./useBackendAPIDeparture";
import { usePinnedValue, type BackendSourcePin } from "./backendFlowReads";
import {
  BackendSavedViewContext,
  useWorkspaceSavedState,
  type SavedViewState,
} from "./backendSavedViewState";

type SourceNavigation = {
  pin?: BackendSourcePin;
  onFlowNavigate?: (pin: BackendSourcePin) => void;
};

export function BackendDatabase(
  props: SourceNavigation & {
    projectId: string;
    revisionId: string;
    repositoryId?: string;
    onDirty?: (dirty: boolean) => void;
  },
) {
  const session = useContext(BackendSavedViewContext);
  return (
    <DatabaseDiscovery
      key={`${props.projectId}:${props.revisionId}:${session?.restoreGeneration ?? 0}`}
      {...props}
    />
  );
}

function DatabaseDiscovery({
  projectId,
  revisionId,
  repositoryId,
  onDirty,
  pin,
  onFlowNavigate,
}: SourceNavigation & {
  projectId: string;
  revisionId: string;
  repositoryId?: string;
  onDirty?: (dirty: boolean) => void;
}) {
  const key = ["backend-database-discovery", projectId, revisionId];
  const depart = useBackendAPIDeparture();
  useDatabaseCancellation(key);
  const query = useQuery({
    queryKey: key,
    queryFn: ({ signal }) =>
      readDatabaseGraph(projectId, { revisionId, recordType: "nodes" }, signal),
    staleTime: Infinity,
    retry: false,
  });
  const [datastore, setDatastore] = usePinnedValue(pin?.datastoreId, pin?.datastoreId ?? "");
  const [dirty, setDirty] = useState(false);
  const savedViewSession = useContext(BackendSavedViewContext);
  const datastores =
    query.data?.nodes.filter(
      (node) => node.kind === "datastore" && "relational" in node.attributes,
    ) ?? [];
  const selected = datastore ? datastores.find((node) => node.id === datastore) : datastores[0];
  return (
    <Paper withBorder p="md" style={{ minWidth: 0 }}>
      <Stack aria-label="База данных">
        <Title order={2}>База данных</Title>
        <Text size="sm" c="dimmed">
          Объявленная схема исходников · только чтение · ревизия {revisionId}
        </Text>
        <LoadState query={query} label="схемы" />
        {query.data && datastores.length === 0 && (
          <Text>Реляционная схема в этой ревизии отсутствует</Text>
        )}
        {query.data && datastore && !selected && (
          <Text component="output">
            Выбранное хранилище {datastore} отсутствует в этой ревизии; другая схема не
            подставляется.
          </Text>
        )}
        {selected && (
          <>
            <NativeSelect
              label="Хранилище базы данных"
              data={datastores.map((node) => ({ value: node.id, label: node.name }))}
              value={selected.id}
              onChange={(event) => {
                const value = event.currentTarget.value;
                depart(
                  () => {
                    setDirty(false);
                    setDatastore(value);
                  },
                  dirty || savedViewSession?.dirty || savedViewSession?.pending
                    ? () =>
                        window.confirm(
                          "Есть несохранённые команды, изменения вида или неизвестный результат применения API. Перейти к другому хранилищу?",
                        )
                    : undefined,
                );
              }}
            />
            <DatabaseFacets
              key={selected.id}
              projectId={projectId}
              revisionId={revisionId}
              repositoryId={repositoryId}
              onDirty={(value) => {
                setDirty(value);
                onDirty?.(value);
              }}
              datastoreId={selected.id}
              nodes={datastoreScope(query.data!.nodes, selected.id)}
              pin={pin}
              onFlowNavigate={onFlowNavigate}
            />
          </>
        )}
      </Stack>
    </Paper>
  );
}

function DatabaseFacets({
  nodes,
  repositoryId,
  onDirty,
  pin,
  onFlowNavigate,
  ...context
}: SourceNavigation &
  Omit<DatabaseContext, "facetKey"> & {
    nodes: BackendNode[];
    repositoryId?: string;
    onDirty: (dirty: boolean) => void;
  }) {
  const depart = useBackendAPIDeparture();
  const facets = useMemo(
    () => [...new Set(nodes.flatMap((node) => Object.keys(relationalFacets(node))))].sort(),
    [nodes],
  );
  const [selected, setSelected] = usePinnedValue(
    pin?.facetKey,
    pin?.facetKey ??
      facets.find((key) =>
        nodes.some((node) => relationalFacets(node)[key]?.sourceKind === "sql"),
      ) ??
      facets[0] ??
      "",
  );
  const [dirty, setDirty] = useState(false);
  const savedViewSession = useContext(BackendSavedViewContext);
  return (
    <>
      <NativeSelect
        label="Источник схемы"
        value={selected}
        onChange={(event) => {
          const value = event.currentTarget.value;
          depart(
            () => {
              setDirty(false);
              onDirty(false);
              setSelected(value);
            },
            dirty || savedViewSession?.dirty || savedViewSession?.pending
              ? () =>
                  window.confirm(
                    "Есть несохранённые команды, изменения вида или неизвестный результат применения API. Перейти к другому источнику?",
                  )
              : undefined,
          );
        }}
        data={
          facets.includes(selected) || !selected
            ? facets
            : [{ value: selected, label: `${selected} · источник отсутствует` }, ...facets]
        }
      />
      {selected && !facets.includes(selected) && (
        <Text component="output">Выбранный источник схемы отсутствует в этой ревизии.</Text>
      )}
      {selected && facets.includes(selected) && (
        <DatabaseFacetWorkspace
          key={selected}
          context={{ ...context, facetKey: selected }}
          nodes={nodes}
          repositoryId={repositoryId}
          pin={pin}
          onFlowNavigate={onFlowNavigate}
          onDirty={(value) => {
            setDirty(value);
            onDirty(value);
          }}
        />
      )}
    </>
  );
}

function DatabaseFacetWorkspace({
  context,
  nodes,
  repositoryId,
  onDirty,
  pin,
  onFlowNavigate,
}: SourceNavigation & {
  context: DatabaseContext;
  nodes: BackendNode[];
  repositoryId?: string;
  onDirty: (dirty: boolean) => void;
}) {
  const savedSession = useContext(BackendSavedViewContext);
  const savedProposal =
    savedSession?.saved?.state.kind === "database" && "proposal" in savedSession.saved.target
      ? {
          ...savedSession.saved.target.proposal,
          baseRevisionId: savedSession.saved.pins.revisionId,
        }
      : null;
  const [view, setView] = usePinnedValue<ProposalView | null>(
    JSON.stringify([pin?.dataNodeId, pin?.revisionId, context.revisionId]),
    savedProposal,
  );
  const [initialColumnId, setInitialColumnId] = useState<string>();
  const effective: DatabaseContext = view
    ? {
        ...context,
        revisionId: view.baseRevisionId,
        proposal: { proposalId: view.proposalId, proposalRevisionId: view.proposalRevisionId },
      }
    : context;
  const needsBaseline = effective.revisionId !== context.revisionId;
  const baselineKey = [...databaseKey(effective), "baseline-objects"];
  useDatabaseCancellation(baselineKey);
  const baseline = useQuery({
    queryKey: baselineKey,
    queryFn: ({ signal }) =>
      readDatabaseGraph(
        effective.projectId,
        { revisionId: effective.revisionId, recordType: "nodes" },
        signal,
      ),
    enabled: needsBaseline,
    staleTime: Infinity,
    retry: false,
  });
  const selectedNodes = needsBaseline
    ? baseline.data && datastoreScope(baseline.data.nodes, effective.datastoreId)
    : nodes;
  return (
    <>
      {repositoryId && (
        <BackendDatabaseProposal
          context={context}
          repositoryId={repositoryId}
          onView={setView}
          onDirty={onDirty}
          initialColumnId={initialColumnId}
          initialView={savedProposal ?? undefined}
        />
      )}
      {needsBaseline && <LoadState query={baseline} label="объекты основания предложения" />}
      {selectedNodes && (
        <DatabaseLists
          key={view ? `${view.proposalId}:${view.proposalRevisionId}` : "source"}
          context={effective}
          nodes={selectedNodes}
          onRequireColumn={repositoryId ? setInitialColumnId : undefined}
          pin={pin}
          onFlowNavigate={onFlowNavigate}
        />
      )}
    </>
  );
}

function DatabaseLists({
  context,
  nodes,
  onRequireColumn,
  pin,
  onFlowNavigate,
}: SourceNavigation & {
  context: DatabaseContext;
  nodes: BackendNode[];
  onRequireColumn?: (columnId: string) => void;
}) {
  const depart = useBackendAPIDeparture();
  const key = databaseKey(context);
  useDatabaseCancellation(key);
  const workspace = useWorkspaceSavedState<Extract<SavedViewState, { kind: "database" }>>(
    databaseTarget(context),
    {
      kind: "database",
      scope: { datastoreId: context.datastoreId, facetKey: context.facetKey },
      filters: { search: "" },
      selection: pin?.dataNodeId ? { recordType: "node", id: pin.dataNodeId } : null,
      positions: [],
      collapsedGroupIds: [],
    },
    JSON.stringify([pin?.dataNodeId, pin?.recordId, pin?.recordType]),
  );
  const state = workspace.state;
  const [draft, setDraft] = useState(state.filters.search);
  const search = state.filters.search;
  const [tableCursors, setTableCursors] = useState([""]);
  const [relationshipCursors, setRelationshipCursors] = useState([""]);
  const tableId = state.filters.relationshipTableId ?? "";
  const [relationshipLabel, setRelationshipLabel] = useState(tableId);
  const relationshipTable = tableId ? { value: tableId, label: relationshipLabel } : null;
  const setRelationshipTable = (value: { value: string; label: string } | null) => {
    depart(() => {
      setRelationshipLabel(value?.label ?? "");
      const { relationshipTableId: _old, ...filters } = state.filters;
      workspace.onStateChange({
        ...state,
        selection: null,
        filters: { ...filters, ...(value ? { relationshipTableId: value.value } : {}) },
      });
    });
  };
  const [historicalSelection, setHistoricalSelection] = useState<DatabaseSelection | null>(null);
  const selection: DatabaseSelection | null =
    historicalSelection ??
    (state.selection ? { type: state.selection.recordType, id: state.selection.id } : null);
  const setSelection = (selection: DatabaseSelection | null) => {
    if (selection?.revisionId && selection.revisionId !== context.revisionId) {
      setHistoricalSelection(selection);
      return;
    }
    setHistoricalSelection(null);
    workspace.onStateChange({
      ...state,
      selection: selection ? { recordType: selection.type, id: selection.id } : null,
    });
  };
  const origin = useRef<HTMLElement | null>(null);
  const fallback = useRef<HTMLHeadingElement>(null);
  const tablesInput = {
    ...databaseTarget(context),
    datastoreId: context.datastoreId,
    facetKey: context.facetKey,
    recordType: "tables" as const,
    limit: 500,
    cursor: tableCursors.at(-1) ?? "",
    ...(search ? { search } : {}),
  };
  const relationshipsInput = {
    ...databaseTarget(context),
    datastoreId: context.datastoreId,
    facetKey: context.facetKey,
    recordType: "relationships" as const,
    limit: 500,
    cursor: relationshipCursors.at(-1) ?? "",
    ...(tableId ? { tableId } : {}),
  };
  const tables = useQuery({
    queryKey: [...key, "tables", tablesInput],
    queryFn: ({ signal }) => queryBackendDatabase(context.projectId, tablesInput, { signal }),
    staleTime: Infinity,
    retry: false,
  });
  const relationships = useQuery({
    queryKey: [...key, "relationships", relationshipsInput],
    queryFn: ({ signal }) =>
      queryBackendDatabase(context.projectId, relationshipsInput, { signal }),
    staleTime: Infinity,
    retry: false,
  });
  const tablePage = tables.data?.status === 200 ? tables.data.data : undefined;
  const relationshipPage = relationships.data?.status === 200 ? relationships.data.data : undefined;
  const tableOptions =
    tablePage?.tableItems.map((table) => ({ value: table.tableId, label: table.qualifiedName })) ??
    [];
  if (relationshipTable && !tableOptions.some((option) => option.value === relationshipTable.value))
    tableOptions.unshift(relationshipTable);
  const nativeObjects = nodes.filter(
    (node) =>
      ["datastore", "db_schema", "view", "migration"].includes(node.kind) ||
      (node.kind === "symbol" && "databaseRoutine" in node.attributes),
  );
  function select(value: DatabaseSelection, trigger?: HTMLElement) {
    depart(() => {
      origin.current = trigger ?? origin.current;
      setSelection(value);
    });
  }
  function close() {
    depart(() => {
      setSelection(null);
      (origin.current?.isConnected ? origin.current : fallback.current)?.focus();
    });
  }
  return (
    <Stack gap="md" style={{ minWidth: 0 }}>
      {workspace.canCapture && (
        <Button
          variant="default"
          disabled={!!historicalSelection || workspace.captureDisabled}
          onClick={workspace.capture}
        >
          Сохранить эту базу данных
        </Button>
      )}
      {historicalSelection && (
        <Text size="sm">
          Инспектор показывает историческую ссылку другой ревизии. Закройте его, чтобы сохранить вид
          выбранного источника.
        </Text>
      )}
      <Stack gap="xs" aria-label="Группы схем базы данных">
        {nodes
          .filter((node) => node.kind === "db_schema" && relationalFacets(node)[context.facetKey])
          .map((node) => (
            <Button
              key={node.id}
              variant="default"
              aria-expanded={!state.collapsedGroupIds.includes(node.id)}
              onClick={() =>
                workspace.onStateChange({
                  ...state,
                  collapsedGroupIds: state.collapsedGroupIds.includes(node.id)
                    ? state.collapsedGroupIds.filter((id) => id !== node.id)
                    : [...state.collapsedGroupIds, node.id],
                })
              }
            >
              {state.collapsedGroupIds.includes(node.id) ? "Развернуть" : "Свернуть"} схему{" "}
              {node.name}
            </Button>
          ))}
      </Stack>
      {context.proposal && <Text fw={600}>Предложенная схема · проверки не выполнены</Text>}
      {tablePage && (
        <>
          <Text component="output">
            {context.proposal ? "Состояние основания" : "Состояние источника"}:{" "}
            {databaseStatus(tablePage.facetStatus)}
          </Text>
          {tablePage.limitations.map((text) => (
            <Text key={text} style={databaseWrap}>
              {text}
            </Text>
          ))}
          <CoverageDetails data={tablePage.coverage} />
        </>
      )}
      <Stack gap="xs" aria-label="Хранилище и другие объекты схемы">
        <Title order={3}>Хранилище и другие объекты схемы</Title>
        {nativeObjects.map((node) => (
          <Button
            key={node.id}
            variant="default"
            aria-pressed={selection?.type === "node" && selection.id === node.id}
            h="auto"
            py="sm"
            justify="flex-start"
            styles={{ ...databaseButtonStyles, label: { ...databaseWrap, textAlign: "left" } }}
            onClick={(event) => select({ type: "node", id: node.id }, event.currentTarget)}
          >
            Открыть{" "}
            {
              (
                {
                  datastore: "хранилище",
                  db_schema: "схему",
                  view: "представление",
                  migration: "миграцию",
                  symbol: "тело базы данных",
                } as Record<string, string>
              )[node.kind]
            }{" "}
            {node.name}
          </Button>
        ))}
      </Stack>
      <form
        onSubmit={(event) => {
          event.preventDefault();
          depart(() => {
            workspace.onStateChange({
              ...state,
              filters: { ...state.filters, search: draft.trim() },
              selection: null,
            });
            setTableCursors([""]);
          });
        }}
      >
        <Group align="flex-end">
          <TextInput
            label="Название таблицы"
            value={draft}
            onChange={(event) => setDraft(event.currentTarget.value)}
            style={{ flex: "1 1 200px" }}
          />
          <Button type="submit">Найти таблицы</Button>
        </Group>
      </form>
      <Title order={3} ref={fallback} tabIndex={-1}>
        Таблицы
      </Title>
      <LoadState query={tables} label="таблиц" />
      {tablePage && (
        <Stack gap="xs" aria-label="Таблицы базы данных">
          <Text size="sm" component="output">
            На странице: {tablePage.tableItems.length} таблиц
            {tablePage.nextCursor ? "; есть следующие страницы" : "; последняя страница"}
          </Text>
          {tablePage.tableItems.length === 0 && (
            <Text>В выбранном источнике таблицы не найдены</Text>
          )}
          {tablePage.tableItems.map((table) => (
            <Button
              key={table.tableId}
              variant="default"
              aria-label={`Открыть таблицу ${table.qualifiedName}`}
              aria-pressed={selection?.id === table.tableId}
              h="auto"
              py="sm"
              justify="flex-start"
              styles={{ ...databaseButtonStyles, label: { ...databaseWrap, textAlign: "left" } }}
              onClick={(event) => select({ type: "node", id: table.tableId }, event.currentTarget)}
            >
              {table.qualifiedName} · колонок {table.columnCount} ·{" "}
              {databaseStatus(table.driftStatus)}
            </Button>
          ))}
          <Pages
            label="таблицы"
            cursors={tableCursors}
            setCursors={setTableCursors}
            busy={tables.isFetching}
            next={tablePage.nextCursor}
          />
        </Stack>
      )}
      <Title order={3}>Внешние ключи</Title>
      <NativeSelect
        label="Связи таблицы"
        value={tableId}
        data={[{ value: "", label: "Все таблицы" }, ...tableOptions]}
        onChange={(event) => {
          const value = event.currentTarget.value;
          setRelationshipTable(
            value
              ? {
                  value,
                  label: tableOptions.find((option) => option.value === value)?.label ?? value,
                }
              : null,
          );
          setRelationshipCursors([""]);
        }}
      />
      <LoadState query={relationships} label="внешних ключей" />
      {relationshipPage && (
        <Stack aria-label="Внешние ключи базы данных" gap="sm">
          <Text component="output">
            Состояние связей: {databaseStatus(relationshipPage.facetStatus)} · на странице{" "}
            {relationshipPage.relationshipItems.length}
            {relationshipPage.nextCursor ? "; есть следующие страницы" : "; последняя страница"}
          </Text>
          {relationshipPage.limitations.map((text) => (
            <Text key={text} style={databaseWrap}>
              {text}
            </Text>
          ))}
          {relationshipPage.relationshipItems.length === 0 && (
            <Text>В выбранном источнике внешние ключи не найдены</Text>
          )}
          {relationshipPage.relationshipItems.map((edge) => (
            <Paper key={edge.edgeId} withBorder p="sm">
              <Stack gap="xs">
                <Button
                  variant="subtle"
                  aria-label={`Открыть FK ${edge.edgeId}`}
                  aria-pressed={selection?.id === edge.edgeId}
                  styles={databaseButtonStyles}
                  h="auto"
                  onClick={(event) =>
                    select({ type: "edge", id: edge.edgeId }, event.currentTarget)
                  }
                >
                  FK {edge.edgeId} · {databaseStatus(edge.status)}
                </Button>
                {edge.runtimeStatus && <Badge>{databaseStatus(edge.runtimeStatus)}</Badge>}
                <Button
                  variant="subtle"
                  styles={databaseButtonStyles}
                  h="auto"
                  onClick={(event) =>
                    select({ type: "node", id: edge.constraintId }, event.currentTarget)
                  }
                >
                  Ограничение {edge.constraintId}
                </Button>
                {edge.columnPairs.map((pair, index) => (
                  <Text key={index} size="sm" style={databaseWrap}>
                    {index + 1}. {pair.fromColumnId} → {pair.toColumnId}
                  </Text>
                ))}
                <Group>
                  <Button
                    variant="subtle"
                    styles={databaseButtonStyles}
                    h="auto"
                    onClick={(event) =>
                      select({ type: "node", id: edge.sourceTableId }, event.currentTarget)
                    }
                  >
                    Исходная таблица {edge.sourceTableId}
                  </Button>
                  {edge.targetTableId ? (
                    <Button
                      variant="subtle"
                      styles={databaseButtonStyles}
                      h="auto"
                      onClick={(event) =>
                        select({ type: "node", id: edge.targetTableId! }, event.currentTarget)
                      }
                    >
                      Целевая таблица {edge.targetTableId}
                    </Button>
                  ) : (
                    <Text style={databaseWrap}>Цель неизвестна: {edge.targetReason}</Text>
                  )}
                </Group>
                {tablePage &&
                  !tablePage.tableItems.some((table) => table.tableId === edge.sourceTableId) && (
                    <Text size="sm">
                      Исходная таблица вне текущей страницы; доступна в инспекторе
                    </Text>
                  )}
                {tablePage &&
                  edge.targetTableId &&
                  !tablePage.tableItems.some((table) => table.tableId === edge.targetTableId) && (
                    <Text size="sm">
                      Целевая таблица вне текущей страницы; доступна в инспекторе
                    </Text>
                  )}
                <Text size="sm">
                  Строк цели на строку источника:{" "}
                  {cardinalityText(edge.targetCardinality.min, edge.targetCardinality.max)}
                </Text>
                <Text size="sm">
                  Строк источника на строку цели:{" "}
                  {cardinalityText(edge.sourceCardinality.min, edge.sourceCardinality.max)}
                </Text>
                {[
                  ...new Set([...edge.sourceCardinality.basis, ...edge.targetCardinality.basis]),
                ].map((basis) => (
                  <Text key={basis} size="xs" style={databaseWrap}>
                    {basis}
                  </Text>
                ))}
              </Stack>
            </Paper>
          ))}
          <Pages
            label="связи"
            cursors={relationshipCursors}
            setCursors={setRelationshipCursors}
            busy={relationships.isFetching}
            next={relationshipPage.nextCursor}
          />
        </Stack>
      )}
      {tablePage && relationshipPage && (
        <BackendDatabaseGraph
          key={JSON.stringify([context, tablesInput, relationshipsInput])}
          tables={tablePage.tableItems}
          relationships={relationshipPage.relationshipItems}
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
      {selection && (
        <BackendDatabaseInspector
          key={JSON.stringify(selection)}
          context={context}
          selection={selection}
          onSelect={select}
          onClose={close}
          onRequireColumn={onRequireColumn}
          onFlowNavigate={onFlowNavigate}
        />
      )}
      <Badge variant="light">Схема исходников; данные и исполнение базы не проверялись</Badge>
    </Stack>
  );
}
