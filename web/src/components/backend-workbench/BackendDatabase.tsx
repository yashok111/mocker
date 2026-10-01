import { useRef, useState } from "react";
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
import {
  databaseButtonStyles,
  databaseKey,
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

export function BackendDatabase(props: { projectId: string; revisionId: string }) {
  return <DatabaseDiscovery key={`${props.projectId}:${props.revisionId}`} {...props} />;
}

function DatabaseDiscovery({ projectId, revisionId }: { projectId: string; revisionId: string }) {
  const key = ["backend-database-discovery", projectId, revisionId];
  useDatabaseCancellation(key);
  const query = useQuery({
    queryKey: key,
    queryFn: ({ signal }) =>
      readDatabaseGraph(projectId, { revisionId, recordType: "nodes" }, signal),
    staleTime: Infinity,
    retry: false,
  });
  const [datastore, setDatastore] = useState("");
  const datastores =
    query.data?.nodes.filter(
      (node) => node.kind === "datastore" && "relational" in node.attributes,
    ) ?? [];
  const selected = datastores.find((node) => node.id === datastore) ?? datastores[0];
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
        {selected && (
          <>
            <NativeSelect
              label="Хранилище базы данных"
              data={datastores.map((node) => ({ value: node.id, label: node.name }))}
              value={selected.id}
              onChange={(event) => setDatastore(event.currentTarget.value)}
            />
            <DatabaseFacets
              key={selected.id}
              projectId={projectId}
              revisionId={revisionId}
              datastoreId={selected.id}
              nodes={datastoreScope(query.data!.nodes, selected.id)}
            />
          </>
        )}
      </Stack>
    </Paper>
  );
}

function DatabaseFacets({
  nodes,
  ...context
}: Omit<DatabaseContext, "facetKey"> & { nodes: BackendNode[] }) {
  const facets = [...new Set(nodes.flatMap((node) => Object.keys(relationalFacets(node))))].sort();
  const [selected, setSelected] = useState(
    facets.find((key) => nodes.some((node) => relationalFacets(node)[key]?.sourceKind === "sql")) ??
      facets[0] ??
      "",
  );
  return (
    <>
      <NativeSelect
        label="Источник схемы"
        value={selected}
        onChange={(event) => setSelected(event.currentTarget.value)}
        data={facets}
      />
      {selected && (
        <DatabaseLists key={selected} context={{ ...context, facetKey: selected }} nodes={nodes} />
      )}
    </>
  );
}

function DatabaseLists({ context, nodes }: { context: DatabaseContext; nodes: BackendNode[] }) {
  const key = databaseKey(context);
  useDatabaseCancellation(key);
  const [draft, setDraft] = useState("");
  const [search, setSearch] = useState("");
  const [tableCursors, setTableCursors] = useState([""]);
  const [relationshipCursors, setRelationshipCursors] = useState([""]);
  const [relationshipTable, setRelationshipTable] = useState<{
    value: string;
    label: string;
  } | null>(null);
  const tableId = relationshipTable?.value ?? "";
  const [selection, setSelection] = useState<DatabaseSelection | null>(null);
  const origin = useRef<HTMLElement | null>(null);
  const fallback = useRef<HTMLHeadingElement>(null);
  const tablesInput = {
    revisionId: context.revisionId,
    datastoreId: context.datastoreId,
    facetKey: context.facetKey,
    recordType: "tables" as const,
    limit: 500,
    cursor: tableCursors.at(-1) ?? "",
    ...(search ? { search } : {}),
  };
  const relationshipsInput = {
    revisionId: context.revisionId,
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
    origin.current = trigger ?? origin.current;
    setSelection(value);
  }
  function close() {
    setSelection(null);
    (origin.current?.isConnected ? origin.current : fallback.current)?.focus();
  }
  return (
    <Stack gap="md" style={{ minWidth: 0 }}>
      {tablePage && (
        <>
          <Text component="output">
            Состояние источника: {databaseStatus(tablePage.facetStatus)}
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
          setSearch(draft.trim());
          setTableCursors([""]);
          setSelection(null);
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
          setSelection(null);
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
        />
      )}
      {selection && (
        <BackendDatabaseInspector
          key={JSON.stringify(selection)}
          context={context}
          selection={selection}
          onSelect={select}
          onClose={close}
        />
      )}
      <Badge variant="light">Схема исходников; данные и исполнение базы не проверялись</Badge>
    </Stack>
  );
}
