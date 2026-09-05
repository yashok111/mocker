import { afterEach, describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import {
  StreamCapsStrip,
  StreamEditor,
  draftFromDefinition,
  draftToDefinition,
  emptyRule,
  emptyStreamDraft,
  type StreamDraft,
} from "./StreamEditor";
import { renderWithProviders } from "@/test/render";
import { json, route } from "@/test/http";
import { serverConfigFixture } from "@/test/fixtures";
import type { StreamDefinition } from "@/api/generated/schemas";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

// The pure half first: the draft ↔ definition round trip is what the create
// and edit forms both stand on, and every check here mirrors a refusal the
// server would otherwise make one round trip later.
describe("draftToDefinition", () => {
  it("turns the default draft into a one-frame schedule that closes when done", () => {
    const out = draftToDefinition("sse", emptyStreamDraft());
    expect(out).toEqual({
      stream: {
        timeline: { frames: [{ delayMs: 1000, event: undefined, data: {} }], loop: undefined },
      },
    });
  });

  it("refuses an interval under the server's floor by name, without clamping", () => {
    const draft: StreamDraft = {
      ...emptyStreamDraft(),
      scheduleOn: false,
      intervalOn: true,
      intervalMs: "50",
    };
    expect(draftToDefinition("sse", draft)).toEqual({
      error: "Интервал (мс): от 100 до 9007199254740991",
    });
  });

  it("refuses a frame whose data is not JSON and says which frame", () => {
    const draft = emptyStreamDraft();
    draft.frames = [
      { delayMs: "0", event: "", dataText: "{}" },
      { delayMs: "10", event: "", dataText: "{oops" },
    ];
    const out = draftToDefinition("sse", draft);
    expect("error" in out && out.error.startsWith("Кадр 2, данные: JSON невалиден")).toBe(true);
  });

  it("refuses a stream with every behaviour switched off", () => {
    const draft: StreamDraft = { ...emptyStreamDraft(), scheduleOn: false };
    expect(draftToDefinition("sse", draft)).toEqual({
      error: "Включите хотя бы одно поведение: расписание или интервал",
    });
    expect(draftToDefinition("ws", draft)).toEqual({
      error:
        "Включите хотя бы одно поведение: расписание, интервал, ответы, эхо или обработку входящих функцией",
    });
  });

  it("builds ws reply rules with the when[] vocabulary and a 4xxx close code", () => {
    const draft: StreamDraft = {
      ...emptyStreamDraft(),
      scheduleOn: false,
      repliesOn: true,
      echo: true,
    };
    const rule = emptyRule();
    rule.when = [
      { in: "body", name: "cmd", op: "equals", value: "bye" },
      { in: "body", name: "id", op: "exists", value: "ignored" },
    ];
    rule.dataText = '{"bye": true}';
    rule.closeOn = true;
    rule.closeCode = "4001";
    rule.closeReason = "done";
    draft.rules = [rule];
    expect(draftToDefinition("ws", draft)).toEqual({
      stream: {
        reactive: [
          {
            when: [
              { in: "body", name: "cmd", op: "equals", value: "bye" },
              { in: "body", name: "id", op: "exists", value: undefined },
            ],
            data: { bye: true },
            close: { code: 4001, reason: "done" },
          },
        ],
        echo: true,
      },
    });
  });

  it("refuses a close code outside 1000 and 4000–4999", () => {
    const draft: StreamDraft = { ...emptyStreamDraft(), scheduleOn: false, repliesOn: true };
    const rule = emptyRule();
    rule.when = [{ in: "body", name: "x", op: "exists" }];
    rule.closeOn = true;
    rule.closeCode = "1001";
    draft.rules = [rule];
    expect(draftToDefinition("ws", draft)).toEqual({
      error: "Правило 1: код закрытия — 1000 или 4000–4999",
    });
  });

  it("round-trips a stored definition through the draft unchanged", () => {
    const def: StreamDefinition = {
      timeline: { frames: [{ delayMs: 250, event: "tick", data: { n: 1 } }], loop: true },
      tick: { intervalMs: 1000, event: "update", schema: { type: "object" } },
      closeWhenDone: false,
      reactive: [{ when: [{ in: "body", name: "cmd", op: "exists" }], data: { pong: true } }],
      echo: true,
    };
    const out = draftToDefinition("ws", draftFromDefinition(def));
    expect(out).toEqual({
      stream: {
        timeline: { frames: [{ delayMs: 250, event: "tick", data: { n: 1 } }], loop: true },
        tick: { intervalMs: 1000, event: "update", schema: { type: "object" } },
        reactive: [
          {
            when: [{ in: "body", name: "cmd", op: "exists", value: undefined }],
            data: { pong: true },
            close: undefined,
          },
        ],
        echo: true,
        closeWhenDone: false,
      },
    });
  });

  // A18 D10: a stream the agent authored with a Lua tick and an inbound hook
  // used to lose BOTH on the first edit from this screen — the draft never
  // read them and the PUT is a full replacement. The round trip is the
  // regression test; the source select is what a person sees.
  it("round-trips a Lua tick and an onFrame hook, and sends no schema beside the Lua", () => {
    const def: StreamDefinition = {
      tick: { intervalMs: 500, lua: "return { n = ordinal }" },
      onFrame: 'return "reply", frame',
    };
    const draft = draftFromDefinition(def);
    expect(draft.tickSource).toBe("lua");
    expect(draft.onFrameOn).toBe(true);
    expect(draftToDefinition("ws", draft)).toEqual({
      stream: {
        tick: { intervalMs: 500, event: undefined, lua: "return { n = ordinal }" },
        onFrame: 'return "reply", frame',
      },
    });
  });

  it("refuses an inbound hook beside reply rules or echo by name, and an empty function", () => {
    const draft: StreamDraft = {
      ...emptyStreamDraft(),
      scheduleOn: false,
      onFrameOn: true,
      onFrameText: "return nil",
      repliesOn: true,
      rules: [emptyRule()],
    };
    expect(draftToDefinition("ws", draft)).toEqual({
      error: "Обработка входящих функцией исключает правила ответов: выключите одно",
    });
    expect(draftToDefinition("ws", { ...draft, repliesOn: false, rules: [], echo: true })).toEqual({
      error: "Обработка входящих функцией исключает эхо: выключите одно",
    });
    expect(
      draftToDefinition("ws", { ...draft, repliesOn: false, rules: [], onFrameText: "  " }),
    ).toEqual({ error: "Обработка входящих функцией: функция пуста" });
    // sse never sends the hook, whatever the draft says (the form hides it).
    expect(draftToDefinition("sse", { ...draft, repliesOn: false, rules: [] })).toEqual({
      error: "Включите хотя бы одно поведение: расписание или интервал",
    });
  });
});

function Harness({ kind }: { kind: "sse" | "ws" }) {
  const [draft, setDraft] = useState(emptyStreamDraft());
  return <StreamEditor kind={kind} draft={draft} onChange={setDraft} testIdPrefix="t" />;
}

describe("StreamEditor", () => {
  it("shows the two server-driven behaviours for sse and never the inbound ones", () => {
    renderWithProviders(<Harness kind="sse" />);
    expect(screen.getByTestId("t-schedule-on")).toBeInTheDocument();
    expect(screen.getByTestId("t-interval-on")).toBeInTheDocument();
    expect(screen.queryByTestId("t-replies-on")).toBeNull();
    expect(screen.queryByTestId("t-echo")).toBeNull();
    expect(screen.queryByTestId("t-on-frame-on")).toBeNull();
  });

  it("names no wire word in the interface", () => {
    renderWithProviders(<Harness kind="ws" />);
    const text = document.body.textContent ?? "";
    for (const banned of ["timeline", "reactive", "tick", "recipe", "matcher", "JSON patch"]) {
      expect(text.toLowerCase(), banned).not.toContain(banned.toLowerCase());
    }
  });

  it("adds and removes a frame, and adds a reply rule with a condition row for ws", async () => {
    renderWithProviders(<Harness kind="ws" />);
    await userEvent.click(screen.getByTestId("t-frame-add"));
    expect(screen.getByTestId("t-frame-delay-1")).toBeInTheDocument();
    await userEvent.click(screen.getByTestId("t-frame-remove-0"));
    expect(screen.queryByTestId("t-frame-delay-1")).toBeNull();

    await userEvent.click(screen.getByTestId("t-replies-on"));
    await userEvent.click(screen.getByTestId("t-rule-add"));
    expect(screen.getByTestId("t-rule-when-name-0-0")).toBeInTheDocument();
    await userEvent.click(screen.getByTestId("t-rule-when-add-0"));
    expect(screen.getByTestId("t-rule-when-name-0-1")).toBeInTheDocument();
  });

  it("swaps the schema box for a Lua box when the tick's source is the function, ws only for the hook", async () => {
    renderWithProviders(<Harness kind="ws" />);
    await userEvent.click(screen.getByTestId("t-interval-on"));
    expect(screen.getByTestId("t-interval-schema")).toBeInTheDocument();
    await userEvent.selectOptions(screen.getByTestId("t-interval-source"), "lua");
    expect(screen.queryByTestId("t-interval-schema")).toBeNull();
    expect(screen.getByTestId("t-interval-lua")).toBeInTheDocument();
    await userEvent.click(screen.getByTestId("t-on-frame-on"));
    expect(screen.getByTestId("t-on-frame-lua")).toBeInTheDocument();
  });
});

describe("StreamCapsStrip", () => {
  it("previews the draft through POST .../endpoints/preview and shows the rate", async () => {
    const fetchMock = route({
      "POST /api/workspaces/7/endpoints/preview": () =>
        json(200, {
          kind: "sse",
          frames: [{ atMs: 1000, data: {} }],
          truncated: false,
          maxBytesPerSec: 2048,
          rules: 0,
          echo: false,
        }),
    });
    renderWithProviders(
      <StreamCapsStrip
        workspaceId={7}
        path="/events"
        kind="sse"
        draft={emptyStreamDraft()}
        testIdPrefix="t"
      />,
    );
    await userEvent.click(screen.getByTestId("t-preview-run"));
    expect(await screen.findByTestId("t-preview-rate")).toHaveTextContent("2.0 КБ/с");
    expect(screen.getByTestId("t-preview-frames")).toHaveTextContent("1000");
    const sent: unknown = JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body));
    expect(sent).toEqual({
      method: "GET",
      path: "/events",
      kind: "sse",
      stream: { timeline: { frames: [{ delayMs: 1000, data: {} }] } },
    });
  });

  it("shows the server's effective limits when the session config is given (A9)", () => {
    route({});
    renderWithProviders(
      <StreamCapsStrip
        workspaceId={7}
        path="/chat"
        kind="ws"
        draft={emptyStreamDraft()}
        limits={serverConfigFixture().limits}
        testIdPrefix="t"
      />,
    );
    const text = screen.getByTestId("t-caps-text");
    expect(text).toHaveTextContent("кадр не больше 3 МБ");
    expect(text).toHaveTextContent("Соединений на воркспейс — 25");
    expect(text).toHaveTextContent("входящий кадр до 32 КБ");
    expect(text).not.toHaveTextContent("MOCKER_");
  });

  it("refuses to preview an invalid draft without a request", async () => {
    const fetchMock = route({});
    const draft: StreamDraft = { ...emptyStreamDraft(), scheduleOn: false };
    renderWithProviders(
      <StreamCapsStrip workspaceId={7} path="/events" kind="sse" draft={draft} testIdPrefix="t" />,
    );
    await userEvent.click(screen.getByTestId("t-preview-run"));
    expect(await screen.findByRole("alert")).toHaveTextContent("Включите хотя бы одно поведение");
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
