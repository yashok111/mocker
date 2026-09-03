import { afterEach, describe, expect, it, vi } from "vitest";
import { act, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { StreamTestClient, wsURL } from "./StreamTestClient";
import { renderWithProviders } from "@/test/render";

// jsdom has neither EventSource nor a WebSocket that reaches a network, so
// both are stood in for by fakes exposing exactly the surface the client
// consumes: open, message (named or not), error, close, send. What the tests
// assert is the wording — the client's whole job is saying in plain words
// what the browser only signals.

class FakeEventSource {
  static instances: FakeEventSource[] = [];
  readonly url: string;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  closed = false;
  listeners: Record<string, Array<(ev: MessageEvent<string>) => void>> = {};
  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }
  addEventListener(type: string, l: (ev: MessageEvent<string>) => void): void {
    (this.listeners[type] ??= []).push(l);
  }
  close(): void {
    this.closed = true;
  }
  emit(type: string, data: string): void {
    for (const l of this.listeners[type] ?? []) {
      l(new MessageEvent<string>(type, { data }));
    }
  }
}

class FakeWebSocket {
  static OPEN = 1;
  static instances: FakeWebSocket[] = [];
  readonly url: string;
  readyState = 0;
  sent: string[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((ev: MessageEvent<unknown>) => void) | null = null;
  onerror: (() => void) | null = null;
  onclose: ((ev: CloseEvent) => void) | null = null;
  constructor(url: string) {
    this.url = url;
    FakeWebSocket.instances.push(this);
  }
  send(data: string): void {
    this.sent.push(data);
  }
  close(): void {
    this.readyState = 3;
  }
}

afterEach(() => {
  vi.unstubAllGlobals();
  FakeEventSource.instances = [];
  FakeWebSocket.instances = [];
});

describe("wsURL", () => {
  it("swaps only the scheme", () => {
    expect(wsURL("http://alex.mock.local:8080/events")).toBe("ws://alex.mock.local:8080/events");
    expect(wsURL("https://mocker.local/w/alex/events")).toBe("wss://mocker.local/w/alex/events");
  });
});

describe("StreamTestClient (sse)", () => {
  it("opens an EventSource from the browser, logs named and unnamed frames, and reports an error in plain words", async () => {
    vi.stubGlobal("EventSource", FakeEventSource);
    renderWithProviders(
      <StreamTestClient
        url="http://alex.mock.local/events"
        kind="sse"
        eventNames={["tick"]}
        testIdPrefix="t"
      />,
    );
    expect(screen.getByTestId("t-client-status")).toHaveTextContent("не подключено");

    await userEvent.click(screen.getByTestId("t-client-connect"));
    const es = FakeEventSource.instances[0];
    expect(es?.url).toBe("http://alex.mock.local/events");
    expect(screen.getByTestId("t-client-status")).toHaveTextContent("подключаемся…");

    act(() => es?.onopen?.());
    expect(screen.getByTestId("t-client-status")).toHaveTextContent("соединение открыто");

    act(() => {
      es?.emit("tick", '{"n":1}');
      es?.emit("message", '{"plain":true}');
    });
    const log = screen.getByTestId("t-client-log");
    expect(log).toHaveTextContent("tick");
    expect(log).toHaveTextContent('{"n":1}');
    expect(log).toHaveTextContent('{"plain":true}');

    act(() => es?.onerror?.());
    // One attempt, one verdict: the source is closed rather than left to
    // EventSource's own silent retry loop.
    expect(es?.closed).toBe(true);
    expect(screen.getByTestId("t-client-status")).toHaveTextContent("закрыто");
    expect(log).toHaveTextContent("Браузер не сообщает причину");
  });

  it("reports a refused handshake as an error when nothing was ever open", async () => {
    vi.stubGlobal("EventSource", FakeEventSource);
    renderWithProviders(
      <StreamTestClient
        url="http://alex.mock.local/events"
        kind="sse"
        eventNames={[]}
        testIdPrefix="t"
      />,
    );
    await userEvent.click(screen.getByTestId("t-client-connect"));
    act(() => FakeEventSource.instances[0]?.onerror?.());
    expect(screen.getByTestId("t-client-status")).toHaveTextContent("ошибка");
  });
});

describe("StreamTestClient (ws)", () => {
  it("dials the ws:// twin of the workspace URL, sends a text frame and reports the close code", async () => {
    vi.stubGlobal("WebSocket", FakeWebSocket);
    renderWithProviders(
      <StreamTestClient
        url="http://alex.mock.local/chat"
        kind="ws"
        eventNames={[]}
        testIdPrefix="t"
      />,
    );
    await userEvent.click(screen.getByTestId("t-client-connect"));
    const ws = FakeWebSocket.instances[0];
    expect(ws?.url).toBe("ws://alex.mock.local/chat");
    expect(screen.getByTestId("t-client-send")).toBeDisabled();

    act(() => {
      if (ws) {
        ws.readyState = FakeWebSocket.OPEN;
        ws.onopen?.();
      }
    });
    expect(screen.getByTestId("t-client-status")).toHaveTextContent("соединение открыто");

    await userEvent.clear(screen.getByTestId("t-client-outgoing"));
    await userEvent.type(screen.getByTestId("t-client-outgoing"), '{{"cmd":"ping"}');
    await userEvent.click(screen.getByTestId("t-client-send"));
    expect(ws?.sent).toEqual(['{"cmd":"ping"}']);

    act(() => ws?.onmessage?.(new MessageEvent("message", { data: '{"pong":true}' })));
    expect(screen.getByTestId("t-client-log")).toHaveTextContent('{"pong":true}');

    act(() =>
      ws?.onclose?.(new CloseEvent("close", { code: 4001, reason: "done", wasClean: true })),
    );
    expect(screen.getByTestId("t-client-status")).toHaveTextContent("закрыто");
    expect(screen.getByTestId("t-client-log")).toHaveTextContent("Закрыто: код 4001 (done)");
  });
});
