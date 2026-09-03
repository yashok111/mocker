import type { ReactElement } from "react";
import { useState } from "react";
import {
  Alert,
  Badge,
  Button,
  Card,
  Group,
  Loader,
  Stack,
  Table,
  Text,
  Textarea,
  TextInput,
  Title,
} from "@mantine/core";
import { IconAlertTriangle, IconPlugConnectedX, IconSend } from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import dayjs from "dayjs";
import {
  getListStreamConnectionsQueryKey,
  useCloseStreamConnection,
  useListStreamConnections,
  usePushStreamFrame,
} from "@/api/generated/stream/stream.ts";
import type { StreamConnectionView } from "@/api/generated/schemas";
import { describeApiFailure, describeApiFailureDetailed } from "@/api/errors";

// StreamConnectionsPage is §30.14's connections panel (P6e) over the P6c
// surface: the workspace's live SSE/WebSocket connections on the mock
// plane, the cap beside them, a close per row and a push-one-frame form.
// It is the eighth tab of the workspace layout rather than a section of
// the custom-endpoints screen: what an operator needs "the moment the
// connection cap bites" is a list they can watch, not a form they are
// editing, and the two have different refresh rhythms — this one polls.
//
// Polling, not a stream: the registry has no feed of its own (§30.16), and
// a 2-second refetch on a list that is at most MOCKER_STREAM_MAX_CONNS rows
// costs less than the server keeping one more subscriber per open panel.
// Connection ids restart at 1 on a process restart (P6c), which is why a
// close or a push always follows a fresh list and never a remembered id —
// the table IS the fresh list.

const POLL_MS = 2000;

function formatOpened(iso: string): string {
  const d = dayjs(iso);
  return d.isValid() ? d.format("HH:mm:ss") : iso;
}

export function StreamConnectionsPage({ id }: { id: number }): ReactElement {
  const connections = useListStreamConnections(id, undefined, {
    query: { refetchInterval: POLL_MS },
  });

  return (
    <div data-testid="connections-page">
      <Stack gap="md">
        <Title order={1}>Соединения</Title>
        <Text size="sm" c="dimmed">
          Живые SSE- и WebSocket-соединения с потоковыми endpoint&apos;ами этого воркспейса. Список
          обновляется каждые {POLL_MS / 1000} с. Соединение можно закрыть или отправить в него один
          кадр — он уйдёт только в это соединение и нигде не сохраняется.
        </Text>
        {connections.isPending ? (
          <Group gap="xs">
            <Loader size="sm" />
            <Text size="sm" component="output">
              Загрузка…
            </Text>
          </Group>
        ) : connections.isError ? (
          <Stack gap="sm" data-testid="connections-error">
            <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
              {describeApiFailure(connections.error)}
            </Alert>
            <Button
              variant="default"
              w="fit-content"
              onClick={() => void connections.refetch()}
              data-testid="connections-retry"
            >
              Повторить
            </Button>
          </Stack>
        ) : connections.data.status !== 200 ? (
          <Alert
            color="red"
            icon={<IconAlertTriangle size={18} />}
            role="alert"
            data-testid="connections-error"
          >
            {describeApiFailure(null)}
          </Alert>
        ) : (
          <ConnectionsTable
            id={id}
            open={connections.data.data.open}
            cap={connections.data.data.cap}
            rows={connections.data.data.connections}
          />
        )}
      </Stack>
    </div>
  );
}

function ConnectionsTable({
  id,
  open,
  cap,
  rows,
}: {
  id: number;
  open: number;
  cap: number;
  rows: StreamConnectionView[];
}): ReactElement {
  const queryClient = useQueryClient();
  const [pushingId, setPushingId] = useState<number | null>(null);
  const [rowError, setRowError] = useState<{ cid: number; message: string } | null>(null);
  const [pushed, setPushed] = useState<{ cid: number; frameId: number } | null>(null);
  const [event, setEvent] = useState("");
  const [dataText, setDataText] = useState("{}");
  const [dataError, setDataError] = useState<string | null>(null);

  const invalidate = () =>
    void queryClient.invalidateQueries({ queryKey: getListStreamConnectionsQueryKey(id) });

  const close = useCloseStreamConnection({
    mutation: { onSuccess: invalidate },
  });
  const push = usePushStreamFrame({
    mutation: { onSuccess: invalidate },
  });

  function handleClose(row: StreamConnectionView): void {
    setRowError(null);
    close.mutate(
      { id, cid: row.id },
      // A 404 here is the ordinary race: the connection ended between the
      // list and the click. The list refetches either way.
      {
        onError: (err) => {
          setRowError({ cid: row.id, message: describeApiFailureDetailed(err) });
          invalidate();
        },
      },
    );
  }

  function handlePush(row: StreamConnectionView): void {
    let data: unknown;
    try {
      data = JSON.parse(dataText) as unknown;
    } catch (err) {
      setDataError(`JSON невалиден (${err instanceof Error ? err.message : String(err)})`);
      return;
    }
    setDataError(null);
    setRowError(null);
    setPushed(null);
    push.mutate(
      {
        id,
        cid: row.id,
        // A WebSocket frame has no event name; the server refuses one by
        // name, so it is simply not sent for kind "ws".
        data: { event: row.kind === "ws" || event === "" ? undefined : event, data },
      },
      {
        onSuccess: (res) => {
          if (res.status === 200) {
            setPushed({ cid: row.id, frameId: res.data.frameId });
          }
        },
        // 409 inbox_full / connection_closed and 504 push_timeout each carry
        // the server's own sentence; the 504 one says the frame is STILL
        // queued and must not be resent blindly — the words are the point.
        onError: (err) => setRowError({ cid: row.id, message: describeApiFailureDetailed(err) }),
      },
    );
  }

  return (
    <Stack gap="sm">
      <Group gap="xs">
        <Text size="sm" data-testid="connections-open">
          Открыто <strong>{open}</strong> из {cap}
        </Text>
        {cap > 0 && open >= cap ? (
          <Badge color="red" size="sm">
            лимит достигнут — новые рукопожатия получают 503
          </Badge>
        ) : null}
      </Group>
      {rows.length === 0 ? (
        <Text data-testid="connections-empty">
          Сейчас ни одного соединения. Откройте потоковый endpoint из браузера (кнопка «Проверить»
          на вкладке «Кастомные») или подключите клиент — строка появится здесь.
        </Text>
      ) : (
        <Card withBorder p={0} data-testid="connections-table">
          <Table fz="sm">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>id</Table.Th>
                <Table.Th>тип</Table.Th>
                <Table.Th>путь</Table.Th>
                <Table.Th>клиент</Table.Th>
                <Table.Th>открыто</Table.Th>
                <Table.Th>кадров →</Table.Th>
                <Table.Th>← кадров</Table.Th>
                <Table.Th />
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {rows.map((row) => (
                <Table.Tr key={row.id} data-testid="connection-row">
                  <Table.Td>{row.id}</Table.Td>
                  <Table.Td>
                    <Badge size="sm" variant="light">
                      {row.kind === "ws" ? "WebSocket" : "SSE"}
                    </Badge>
                  </Table.Td>
                  <Table.Td>{row.path}</Table.Td>
                  <Table.Td>{row.remoteAddr}</Table.Td>
                  <Table.Td>{formatOpened(row.openedAt)}</Table.Td>
                  <Table.Td>
                    {row.frames}
                    {row.pushed > 0 ? ` (из них отправлено вручную ${row.pushed})` : ""}
                    {row.skipped > 0 ? ` · пропущено ${row.skipped}` : ""}
                  </Table.Td>
                  <Table.Td>{row.kind === "ws" ? row.framesIn : "—"}</Table.Td>
                  <Table.Td>
                    <Group gap="xs" wrap="nowrap" justify="flex-end">
                      <Button
                        variant="default"
                        size="xs"
                        leftSection={<IconSend size={14} />}
                        onClick={() => setPushingId(pushingId === row.id ? null : row.id)}
                        data-testid="connection-push-toggle"
                      >
                        Отправить кадр
                      </Button>
                      <Button
                        variant="default"
                        size="xs"
                        color="red"
                        leftSection={<IconPlugConnectedX size={14} />}
                        loading={close.isPending}
                        onClick={() => handleClose(row)}
                        data-testid="connection-close"
                      >
                        Закрыть
                      </Button>
                    </Group>
                    {rowError?.cid === row.id ? (
                      <Alert color="red" mt="xs" role="alert" data-testid="connection-error">
                        {rowError.message}
                      </Alert>
                    ) : null}
                    {pushed?.cid === row.id ? (
                      <Text size="xs" mt="xs" data-testid="connection-pushed">
                        Отправлено, id кадра {pushed.frameId}
                      </Text>
                    ) : null}
                    {pushingId === row.id ? (
                      <Stack gap="xs" mt="xs" data-testid="connection-push-form">
                        {row.kind === "sse" ? (
                          <TextInput
                            label="Событие (необязательно)"
                            value={event}
                            onChange={(e) => setEvent(e.currentTarget.value)}
                            data-testid="connection-push-event"
                          />
                        ) : null}
                        <Textarea
                          label="Данные, JSON"
                          rows={3}
                          value={dataText}
                          error={dataError}
                          onChange={(e) => setDataText(e.currentTarget.value)}
                          data-testid="connection-push-data"
                        />
                        <Button
                          size="xs"
                          w="fit-content"
                          loading={push.isPending}
                          onClick={() => handlePush(row)}
                          data-testid="connection-push-submit"
                        >
                          Отправить в соединение {row.id}
                        </Button>
                      </Stack>
                    ) : null}
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Card>
      )}
    </Stack>
  );
}
