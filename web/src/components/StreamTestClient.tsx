import type { ReactElement } from "react";
import { useEffect, useRef, useState } from "react";
import {
  Badge,
  Button,
  Card,
  Code,
  Group,
  ScrollArea,
  Stack,
  Text,
  TextInput,
} from "@mantine/core";
import { IconPlugConnected, IconPlugConnectedX, IconSend } from "@tabler/icons-react";
import dayjs from "dayjs";
import type { StreamKind } from "./StreamEditor";

// StreamTestClient is §30.14's "Try it": a BROWSER-side client, not a
// server-side probe, and the distinction is the whole point. internal/probe
// is deliberately the tree's only outgoing HTTP client, and teaching it
// WebSocket would mean an RFC 6455 client beside the server; more to the
// point it would answer the wrong question — a server dialling itself never
// meets the corporate proxy that §16 warns will cut an upgrade, and that
// proxy sits between THIS browser and the mock host. So the panel opens the
// connection from here (EventSource or WebSocket, the CSP naming ws:/wss:
// for it since P6d), says what happened in plain words, logs frames as
// they arrive, and can send one.
//
// What the browser does NOT tell us, and the wording reflects it: an
// EventSource `error` carries no status, so a refused handshake (503 over
// the cap, 501 unsupported, a 404 route, a proxy reset) is one event — the
// text says so rather than guessing; and EventSource reconnects on its own,
// so the client closes it on the first error to keep the log honest (one
// attempt, one verdict) instead of a silent retry loop the operator did not
// ask for.

export type LogEntry = {
  at: string;
  dir: "in" | "out" | "sys";
  event?: string;
  text: string;
};

type Status = "idle" | "connecting" | "open" | "closed" | "error";

const MAX_LOG = 200;

const STATUS_LABEL: Record<Status, string> = {
  idle: "не подключено",
  connecting: "подключаемся…",
  open: "соединение открыто",
  closed: "закрыто",
  error: "ошибка",
};

const STATUS_COLOR: Record<Status, string> = {
  idle: "gray",
  connecting: "yellow",
  open: "green",
  closed: "gray",
  error: "red",
};

/** wsURL turns the workspace's http(s) URL into the ws(s) one — the scheme
 * is the only difference, and the port and path stay exactly as the server
 * reported them (ConnectPanel's rule: never rebuild a URL the server sent). */
export function wsURL(httpURL: string): string {
  return httpURL.replace(/^http/, "ws");
}

export function StreamTestClient({
  url,
  kind,
  eventNames,
  testIdPrefix,
}: {
  /** The absolute mock-plane URL of the endpoint, e.g. http://alex.mock.local/events. */
  url: string;
  kind: StreamKind;
  /** SSE only: the named events the definition sends, so listeners exist
   * for them — EventSource fires `message` for unnamed frames alone. */
  eventNames: string[];
  testIdPrefix: string;
}): ReactElement {
  const t = (name: string) => `${testIdPrefix}-${name}`;
  const [status, setStatus] = useState<Status>("idle");
  const [log, setLog] = useState<LogEntry[]>([]);
  const [outgoing, setOutgoing] = useState("{}");
  const esRef = useRef<EventSource | null>(null);
  const wsRef = useRef<WebSocket | null>(null);

  function push(entry: Omit<LogEntry, "at">): void {
    setLog((prev) => [...prev, { at: dayjs().format("HH:mm:ss.SSS"), ...entry }].slice(-MAX_LOG));
  }

  function disconnect(reason: string): void {
    if (esRef.current) {
      esRef.current.close();
      esRef.current = null;
    }
    if (wsRef.current) {
      wsRef.current.close(1000, "closed by the panel");
      wsRef.current = null;
    }
    setStatus((s) => (s === "error" ? s : "closed"));
    push({ dir: "sys", text: reason });
  }

  // Never leave a socket open past the panel: a stream connection holds one
  // of MOCKER_STREAM_MAX_CONNS until its lifetime ends.
  useEffect(() => {
    return () => {
      esRef.current?.close();
      wsRef.current?.close(1000, "panel unmounted");
    };
  }, []);

  function connect(): void {
    setLog([]);
    setStatus("connecting");
    const target = kind === "ws" ? wsURL(url) : url;
    push({ dir: "sys", text: `Подключаемся к ${target}` });
    if (kind === "sse") {
      if (typeof EventSource === "undefined") {
        setStatus("error");
        push({ dir: "sys", text: "В этом браузере нет EventSource" });
        return;
      }
      const es = new EventSource(target);
      esRef.current = es;
      es.onopen = () => {
        setStatus("open");
        push({ dir: "sys", text: "Соединение открыто — сервер принял поток" });
      };
      const onFrame = (name: string) => (ev: MessageEvent<string>) =>
        push({ dir: "in", event: name === "message" ? undefined : name, text: ev.data });
      es.addEventListener("message", onFrame("message"));
      for (const name of new Set(eventNames.filter((n) => n !== "" && n !== "message"))) {
        es.addEventListener(name, onFrame(name));
      }
      es.onerror = () => {
        // One attempt, one verdict: EventSource would otherwise retry forever.
        es.close();
        esRef.current = null;
        setStatus((s) => (s === "open" ? "closed" : "error"));
        push({
          dir: "sys",
          text:
            "Соединение прервано. Браузер не сообщает причину: это может быть отказ сервера (лимит " +
            "соединений, неизвестный маршрут), обрыв по времени жизни или прокси между вами и моком.",
        });
      };
      return;
    }
    if (typeof WebSocket === "undefined") {
      setStatus("error");
      push({ dir: "sys", text: "В этом браузере нет WebSocket" });
      return;
    }
    const ws = new WebSocket(target);
    wsRef.current = ws;
    ws.onopen = () => {
      setStatus("open");
      push({ dir: "sys", text: "Соединение открыто — рукопожатие прошло" });
    };
    ws.onmessage = (ev: MessageEvent<unknown>) => {
      push({
        dir: "in",
        text: typeof ev.data === "string" ? ev.data : "[бинарный кадр]",
      });
    };
    ws.onerror = () => {
      push({
        dir: "sys",
        text:
          "Ошибка соединения. Браузер не сообщает причину: отказ сервера (Origin, лимит соединений, " +
          "не тот маршрут) или прокси, который не пропускает upgrade.",
      });
    };
    ws.onclose = (ev: CloseEvent) => {
      wsRef.current = null;
      setStatus((s) => (s === "open" || ev.wasClean ? "closed" : "error"));
      push({
        dir: "sys",
        text: `Закрыто: код ${ev.code}${ev.reason ? ` (${ev.reason})` : ""}`,
      });
    };
  }

  function send(): void {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      push({ dir: "sys", text: "Нет открытого соединения" });
      return;
    }
    ws.send(outgoing);
    push({ dir: "out", text: outgoing });
  }

  const live = status === "connecting" || status === "open";

  return (
    <Card withBorder p="sm" data-testid={t("client")}>
      <Stack gap="xs">
        <Group gap="sm">
          <Badge color={STATUS_COLOR[status]} data-testid={t("client-status")}>
            {STATUS_LABEL[status]}
          </Badge>
          <Text size="xs" c="dimmed">
            {kind === "ws" ? wsURL(url) : url}
          </Text>
          {live ? (
            <Button
              variant="default"
              size="xs"
              leftSection={<IconPlugConnectedX size={14} />}
              onClick={() => disconnect("Отключено с этой стороны")}
              data-testid={t("client-disconnect")}
            >
              Отключиться
            </Button>
          ) : (
            <Button
              size="xs"
              leftSection={<IconPlugConnected size={14} />}
              onClick={connect}
              data-testid={t("client-connect")}
            >
              Подключиться из браузера
            </Button>
          )}
        </Group>
        {kind === "ws" ? (
          <Group gap="xs" align="flex-end">
            <TextInput
              label="Отправить сообщение (текстовый кадр)"
              style={{ flex: 1 }}
              value={outgoing}
              onChange={(e) => setOutgoing(e.currentTarget.value)}
              data-testid={t("client-outgoing")}
            />
            <Button
              variant="default"
              size="xs"
              leftSection={<IconSend size={14} />}
              disabled={status !== "open"}
              onClick={send}
              data-testid={t("client-send")}
            >
              Отправить
            </Button>
          </Group>
        ) : null}
        <ScrollArea.Autosize mah={240}>
          <Stack gap={2} data-testid={t("client-log")}>
            {log.length === 0 ? (
              <Text size="xs" c="dimmed">
                Кадры появятся здесь по мере получения.
              </Text>
            ) : (
              log.map((entry, i) => (
                // eslint-disable-next-line react/no-array-index-key
                <Group key={i} gap="xs" wrap="nowrap" align="flex-start">
                  <Text size="xs" c="dimmed" style={{ whiteSpace: "nowrap" }}>
                    {entry.at}
                  </Text>
                  <Text size="xs" c={entry.dir === "sys" ? "dimmed" : undefined} fw={500} w={22}>
                    {entry.dir === "in" ? "←" : entry.dir === "out" ? "→" : "·"}
                  </Text>
                  {entry.event ? (
                    <Badge size="xs" variant="light">
                      {entry.event}
                    </Badge>
                  ) : null}
                  {entry.dir === "sys" ? (
                    <Text size="xs" c="dimmed">
                      {entry.text}
                    </Text>
                  ) : (
                    <Code block={false} fz="xs">
                      {entry.text}
                    </Code>
                  )}
                </Group>
              ))
            )}
          </Stack>
        </ScrollArea.Autosize>
      </Stack>
    </Card>
  );
}
