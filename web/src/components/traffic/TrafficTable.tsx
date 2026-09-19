import type { ReactElement } from "react";
import {
  Alert,
  Anchor,
  Badge,
  Button,
  Code,
  Group,
  Stack,
  Table,
  Text,
  Tooltip,
  UnstyledButton,
  VisuallyHidden,
} from "@mantine/core";
import { IconChevronDown, IconChevronRight, IconCopy, IconRoute } from "@tabler/icons-react";
import { useNavigate } from "@tanstack/react-router";
import type { TrafficRow } from "@/api/generated/schemas";
import {
  droppedNoteCount,
  endpointDisabledReason,
  formatSource,
  formatTime,
  overrideDisabledReason,
} from "./model";
import classes from "./TrafficTable.module.css";

// TrafficTable is the rows and their two actions. It reads no query and owns
// no state: everything it shows is decided by TrafficPage (which row is
// expanded, which row's mutation failed) or derived by model.ts (why an
// action is refused). Split out of TrafficPage.tsx so the transport state
// machine and the markup stop sharing a 923-line file.

export function TrafficTable({
  workspaceId,
  rows,
  expanded,
  onToggle,
  rowErrors,
  onCreateOverride,
  onCreateEndpoint,
  overridePendingFor,
  endpointPendingFor,
}: {
  workspaceId: number;
  /** Already filtered and sorted newest-first by the page. */
  rows: TrafficRow[];
  expanded: number | null;
  onToggle: (rowId: number) => void;
  rowErrors: Record<number, string>;
  onCreateOverride: (rowId: number) => void;
  onCreateEndpoint: (rowId: number) => void;
  /** The row id each mutation is currently in flight for, or undefined. */
  overridePendingFor: number | undefined;
  endpointPendingFor: number | undefined;
}): ReactElement {
  return (
    <Table highlightOnHover stickyHeader className={classes.table} data-testid="traffic-table">
      <Table.Thead>
        <Table.Tr>
          <Table.Th>Время</Table.Th>
          <Table.Th>Метод</Table.Th>
          <Table.Th>Путь</Table.Th>
          <Table.Th>Статус</Table.Th>
          <Table.Th>Длительность</Table.Th>
          <Table.Th>Совпадение</Table.Th>
          <Table.Th>Источник</Table.Th>
          <Table.Th>Действия</Table.Th>
        </Table.Tr>
      </Table.Thead>
      <Table.Tbody>
        {rows.map((row) => (
          <TrafficRowGroup
            key={row.id}
            workspaceId={workspaceId}
            row={row}
            isExpanded={expanded === row.id}
            onToggle={() => onToggle(row.id)}
            overrideReason={overrideDisabledReason(row)}
            endpointReason={endpointDisabledReason(row)}
            droppedNote={droppedNoteCount(row.notes)}
            error={rowErrors[row.id]}
            onCreateOverride={() => onCreateOverride(row.id)}
            onCreateEndpoint={() => onCreateEndpoint(row.id)}
            overridePending={overridePendingFor === row.id}
            endpointPending={endpointPendingFor === row.id}
          />
        ))}
      </Table.Tbody>
    </Table>
  );
}

function TrafficRowGroup({
  workspaceId,
  row,
  isExpanded,
  onToggle,
  overrideReason,
  endpointReason,
  droppedNote,
  error,
  onCreateOverride,
  onCreateEndpoint,
  overridePending,
  endpointPending,
}: {
  workspaceId: number;
  row: TrafficRow;
  isExpanded: boolean;
  onToggle: () => void;
  overrideReason: string | null;
  endpointReason: string | null;
  droppedNote: number | null;
  error: string | undefined;
  onCreateOverride: () => void;
  onCreateEndpoint: () => void;
  overridePending: boolean;
  endpointPending: boolean;
}) {
  return (
    <>
      <Table.Tr
        data-testid="traffic-row"
        data-row-id={row.id}
        onClick={onToggle}
        style={{ cursor: "pointer" }}
      >
        <Table.Td className={classes.time}>{formatTime(row.ts)}</Table.Td>
        <Table.Td>
          <Badge
            size="sm"
            variant="light"
            color={
              { GET: "blue", POST: "teal", PUT: "orange", PATCH: "yellow", DELETE: "red" }[
                row.method
              ] ?? "gray"
            }
          >
            {row.method}
          </Badge>
        </Table.Td>
        <Table.Td>
          <UnstyledButton
            className={classes.path}
            aria-label={`Подробнее о запросе ${row.method} ${row.path}`}
            aria-expanded={isExpanded}
            aria-controls={isExpanded ? `traffic-details-${row.id}` : undefined}
            onClick={(event) => {
              event.stopPropagation();
              onToggle();
            }}
          >
            {isExpanded ? <IconChevronDown size={14} /> : <IconChevronRight size={14} />}
            <Code>{row.path}</Code>
          </UnstyledButton>
        </Table.Td>
        <Table.Td>
          <Text
            component="span"
            size="sm"
            fw={600}
            c={
              row.status >= 500
                ? "red.8"
                : row.status >= 400
                  ? "orange.9"
                  : row.status >= 300
                    ? "blue.8"
                    : "teal.9"
            }
          >
            {row.status}
          </Text>
          {row.truncated ? (
            <Badge
              size="xs"
              color="yellow"
              variant="light"
              ml={4}
              title="Тело обрезано при записи — показанное короче того, что реально ушло по сети"
              data-testid="traffic-truncated"
            >
              обрезано
            </Badge>
          ) : null}
        </Table.Td>
        <Table.Td className={classes.duration}>
          {new Intl.NumberFormat("ru-RU", { maximumFractionDigits: 2 }).format(row.durationMs)} мс
        </Table.Td>
        <Table.Td onClick={(e) => e.stopPropagation()}>
          <MatchCell workspaceId={workspaceId} row={row} />
        </Table.Td>
        <Table.Td>{formatSource(row)}</Table.Td>
        <Table.Td onClick={(e) => e.stopPropagation()}>
          <Group gap={4} wrap="nowrap">
            <Tooltip
              label={overrideReason ?? "Создать правку из этого ответа"}
              events={{ hover: true, focus: true, touch: true }}
            >
              <span
                aria-label={
                  overrideReason !== null ? `Правка недоступна: ${overrideReason}` : undefined
                }
              >
                <Button
                  size="xs"
                  variant="subtle"
                  leftSection={<IconCopy size={14} />}
                  aria-label="Создать правку из этого ответа"
                  disabled={overrideReason !== null}
                  title={overrideReason ?? undefined}
                  loading={overridePending}
                  onClick={onCreateOverride}
                  data-testid="traffic-to-override"
                >
                  Правка
                </Button>
              </span>
            </Tooltip>
            <Tooltip
              label={endpointReason ?? "Создать endpoint из этого запроса"}
              events={{ hover: true, focus: true, touch: true }}
            >
              <span
                aria-label={
                  endpointReason !== null ? `Endpoint недоступен: ${endpointReason}` : undefined
                }
              >
                <Button
                  size="xs"
                  variant="subtle"
                  leftSection={<IconRoute size={14} />}
                  aria-label="Создать endpoint из этого запроса"
                  disabled={endpointReason !== null}
                  title={endpointReason ?? undefined}
                  loading={endpointPending}
                  onClick={onCreateEndpoint}
                  data-testid="traffic-to-endpoint"
                >
                  Эндпоинт
                </Button>
              </span>
            </Tooltip>
          </Group>
          {overrideReason !== null ? (
            <VisuallyHidden data-testid="traffic-override-reason">
              Правка недоступна: {overrideReason}
            </VisuallyHidden>
          ) : null}
          {endpointReason !== null ? (
            <VisuallyHidden data-testid="traffic-endpoint-reason">
              Endpoint недоступен: {endpointReason}
            </VisuallyHidden>
          ) : null}
        </Table.Td>
      </Table.Tr>
      {droppedNote !== null || error !== undefined ? (
        <Table.Tr>
          <Table.Td colSpan={8} style={{ paddingTop: 0 }}>
            <Group gap="xs" wrap="wrap">
              {droppedNote !== null ? (
                <Badge color="yellow" variant="light">
                  Отброшено при записи этой строки: {droppedNote}
                </Badge>
              ) : null}
            </Group>
            {error !== undefined ? (
              <Alert color="red" role="alert" mt="xs">
                {error}
              </Alert>
            ) : null}
          </Table.Td>
        </Table.Tr>
      ) : null}
      {isExpanded ? (
        <Table.Tr
          id={`traffic-details-${row.id}`}
          data-testid="traffic-row-details"
          className={classes.details}
        >
          <Table.Td colSpan={8}>
            <Stack gap="xs">
              {overrideReason !== null ? (
                <Text size="sm" c="dimmed">
                  Правка недоступна: {overrideReason}
                </Text>
              ) : null}
              {endpointReason !== null ? (
                <Text size="sm" c="dimmed">
                  Endpoint недоступен: {endpointReason}
                </Text>
              ) : null}
              <Text size="sm" fw={500}>
                Заголовки запроса
              </Text>
              <Code block>{JSON.stringify(row.reqHeaders ?? {}, null, 2)}</Code>
              <Text size="sm" fw={500}>
                Тело запроса
              </Text>
              <Code block>{row.reqBody || "—"}</Code>
              <Text size="sm" fw={500}>
                Тело ответа
              </Text>
              <Code block>{row.respBody || "—"}</Code>
              <Text size="sm" fw={500}>
                Заметки
              </Text>
              <Text size="sm">{row.notes === undefined || row.notes === "" ? "—" : row.notes}</Text>
            </Stack>
          </Table.Td>
        </Table.Tr>
      ) : null}
    </>
  );
}

// MatchCell is the «Совпадение» column (A21, U4): what answered the request,
// as a LINK into its editor when there is one — the operations tab by the
// spec operation's id (`?opId=`, resolved there), the custom-endpoints tab
// by the row id (`?endpointId=`, the P7b deep link). Before this the cell
// printed `operation #12` as text and the most natural move on the screen —
// "this answer is wrong, open it" — was four clicks and a search.
function MatchCell({ workspaceId, row }: { workspaceId: number; row: TrafficRow }) {
  const navigate = useNavigate();
  if (row.matchedKind === "operation" && row.matchedId != null) {
    const opId = String(row.matchedId);
    return (
      <Anchor
        size="sm"
        href={`/workspaces/${workspaceId}/operations?opId=${opId}`}
        data-testid="traffic-match-link"
        onClick={(e) => {
          e.preventDefault();
          void navigate({
            to: "/workspaces/$id/operations",
            params: { id: workspaceId },
            search: { opId },
          });
        }}
      >
        операция #{row.matchedId}
      </Anchor>
    );
  }
  if (row.matchedKind === "endpoint" && row.matchedId != null) {
    const endpointId = String(row.matchedId);
    return (
      <Anchor
        size="sm"
        href={`/workspaces/${workspaceId}/endpoints?endpointId=${endpointId}`}
        data-testid="traffic-match-link"
        onClick={(e) => {
          e.preventDefault();
          void navigate({
            to: "/workspaces/$id/endpoints",
            params: { id: workspaceId },
            search: { endpointId },
          });
        }}
      >
        endpoint #{row.matchedId}
      </Anchor>
    );
  }
  return <>{row.matchedKind === "none" ? "—" : row.matchedKind}</>;
}
