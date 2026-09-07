import type { Condition, StreamDefinition } from "@/api/generated/schemas";
import { jsonLocation } from "@/validation/json";

// streamDraft.ts is the stream editor's model: the shape the form edits
// (StreamDraft), the two directions between it and the wire document
// (draftFromDefinition / draftToDefinition), and the client-side checks that
// mirror the server's own by name. It carries no JSX and no React, which is
// why it is a `.ts` — StreamEditor.tsx was 891 lines with all of this inlined
// above the component, and its own test already had a `draftToDefinition`
// describe block that never rendered anything.
//
// The checks here mirror the ones a form can answer BEFORE a round trip — a
// non-integer, an interval under the floor, a frame whose data is not JSON —
// and never clamp, exactly as the server never clamps
// (internal/customep/stream.go validates the whole document by name).

export type StreamKind = "sse" | "ws";

// The server's own limits (internal/customep/stream.go). Spelled here so the
// caps strip and the client checks read one table; a change there without a
// change here shows up as a server refusal the strip did not predict.
export const STREAM_CAPS = {
  maxFrames: 500,
  maxFrameDelayMs: 30_000,
  minTickIntervalMs: 100,
  maxEventBytes: 64,
  maxRules: 100,
  maxCloseReasonBytes: 123,
} as const;

export interface FrameDraft {
  delayMs: string;
  event: string;
  dataText: string;
}

export interface RuleDraft {
  when: Condition[];
  dataText: string;
  closeOn: boolean;
  closeCode: string;
  closeReason: string;
}

export interface StreamDraft {
  scheduleOn: boolean;
  frames: FrameDraft[];
  loop: boolean;
  closeWhenDone: boolean;
  intervalOn: boolean;
  intervalMs: string;
  tickEvent: string;
  // A18 D10.1: a tick's body comes from exactly one of a JSON Schema
  // (generated, byte-identical per ordinal) or a Lua function; the draft
  // keeps BOTH texts so switching the source back does not lose what was
  // typed, and draftToDefinition sends only the selected one.
  tickSource: "schema" | "lua";
  schemaText: string;
  luaText: string;
  repliesOn: boolean;
  rules: RuleDraft[];
  echo: boolean;
  // A18 D10.2: a Lua hook over every inbound ws frame. It REPLACES the reply
  // rules and echo on the wire (400 on_frame_and_reactive / on_frame_and_echo),
  // which draftToDefinition mirrors by refusing the combination by name
  // before a round trip. Before this field existed (2026-09-05) an edit
  // from this screen of a stream the agent had given tick.lua or onFrame
  // silently dropped both: draftFromDefinition never read them and a
  // full-replacement PUT resent the definition without them.
  onFrameOn: boolean;
  onFrameText: string;
}
export const DEFAULT_CONDITION: Condition = { in: "body", name: "", op: "equals", value: "" };

export function emptyFrame(): FrameDraft {
  return { delayMs: "1000", event: "", dataText: "{}" };
}

export function emptyRule(): RuleDraft {
  return {
    when: [{ ...DEFAULT_CONDITION }],
    dataText: "{}",
    closeOn: false,
    closeCode: "1000",
    closeReason: "",
  };
}

export function emptyStreamDraft(): StreamDraft {
  return {
    scheduleOn: true,
    frames: [emptyFrame()],
    loop: false,
    closeWhenDone: true,
    intervalOn: false,
    intervalMs: "1000",
    tickEvent: "",
    tickSource: "schema",
    schemaText: '{\n  "type": "object",\n  "properties": {}\n}',
    luaText: "",
    repliesOn: false,
    rules: [],
    echo: false,
    onFrameOn: false,
    onFrameText: "",
  };
}

function pretty(value: unknown): string {
  return JSON.stringify(value === undefined ? null : value, null, 2);
}

/** draftFromDefinition seeds the editor from a stored row (edit) — every
 * stored field survives the round trip, including `closeWhenDone` when the
 * server omitted it (nil reads as true on the server; the form shows that). */
export function draftFromDefinition(def: StreamDefinition | undefined): StreamDraft {
  const draft = emptyStreamDraft();
  if (!def) {
    return draft;
  }
  draft.scheduleOn = def.timeline !== undefined;
  draft.frames = def.timeline
    ? def.timeline.frames.map((f) => ({
        delayMs: String(f.delayMs),
        event: f.event ?? "",
        dataText: pretty(f.data),
      }))
    : [emptyFrame()];
  draft.loop = def.timeline?.loop ?? false;
  draft.closeWhenDone = def.closeWhenDone ?? true;
  draft.intervalOn = def.tick !== undefined;
  if (def.tick) {
    draft.intervalMs = String(def.tick.intervalMs);
    draft.tickEvent = def.tick.event ?? "";
    // A Lua tick has no schema (the two are exclusive by name on the
    // server): keep the default schema text for the OTHER source rather
    // than "null", so switching back offers a valid starting document.
    if (def.tick.lua !== undefined) {
      draft.tickSource = "lua";
      draft.luaText = def.tick.lua;
    } else {
      draft.schemaText = pretty(def.tick.schema);
    }
  }
  if (def.onFrame !== undefined) {
    draft.onFrameOn = true;
    draft.onFrameText = def.onFrame;
  }
  draft.repliesOn = (def.reactive?.length ?? 0) > 0;
  draft.rules = (def.reactive ?? []).map((r) => ({
    when: r.when.map((c) => ({ ...c })),
    dataText: r.data === undefined ? "" : pretty(r.data),
    closeOn: r.close !== undefined,
    closeCode: String(r.close?.code ?? 1000),
    closeReason: r.close?.reason ?? "",
  }));
  draft.echo = def.echo ?? false;
  return draft;
}

function parseJSON(text: string, what: string): { value: unknown } | { error: string } {
  try {
    return { value: JSON.parse(text) as unknown };
  } catch (err) {
    return { error: `${what}: JSON невалиден (${jsonLocation(text, err)})` };
  }
}

function checkEvent(event: string, what: string): string | null {
  if (event === "") {
    return null;
  }
  if (/[\r\n\0]/.test(event)) {
    return `${what}: имя события не может содержать перевод строки`;
  }
  if (new TextEncoder().encode(event).length > STREAM_CAPS.maxEventBytes) {
    return `${what}: имя события длиннее ${STREAM_CAPS.maxEventBytes} байт`;
  }
  return null;
}

function integer(text: string, what: string, min: number, max: number): number | string {
  const trimmed = text.trim();
  const n = Number(trimmed);
  if (trimmed === "" || !Number.isInteger(n)) {
    return `${what}: целое число от ${min} до ${max}`;
  }
  if (n < min || n > max) {
    return `${what}: от ${min} до ${max}`;
  }
  return n;
}

/** draftToDefinition turns the form into the wire document, or names the
 * first thing that stops it. The order of checks follows the form top to
 * bottom so the message points at the section the operator is looking at. */
export function draftToDefinition(
  kind: StreamKind,
  draft: StreamDraft,
): { stream: StreamDefinition } | { error: string } {
  const stream: StreamDefinition = {};
  if (draft.scheduleOn) {
    if (draft.frames.length === 0) {
      return { error: "Расписание: добавьте хотя бы один кадр" };
    }
    if (draft.frames.length > STREAM_CAPS.maxFrames) {
      return { error: `Расписание: не больше ${STREAM_CAPS.maxFrames} кадров` };
    }
    const frames = [];
    for (const [i, f] of draft.frames.entries()) {
      const what = `Кадр ${i + 1}`;
      const delayMs = integer(f.delayMs, `${what}, пауза (мс)`, 0, STREAM_CAPS.maxFrameDelayMs);
      if (typeof delayMs === "string") {
        return { error: delayMs };
      }
      const eventError = checkEvent(f.event, what);
      if (eventError) {
        return { error: eventError };
      }
      const data = parseJSON(f.dataText, `${what}, данные`);
      if ("error" in data) {
        return { error: data.error };
      }
      frames.push({
        delayMs,
        event: f.event === "" ? undefined : f.event,
        data: data.value,
      });
    }
    stream.timeline = { frames, loop: draft.loop || undefined };
  }
  if (draft.intervalOn) {
    const intervalMs = integer(
      draft.intervalMs,
      "Интервал (мс)",
      STREAM_CAPS.minTickIntervalMs,
      Number.MAX_SAFE_INTEGER,
    );
    if (typeof intervalMs === "string") {
      return { error: intervalMs };
    }
    const eventError = checkEvent(draft.tickEvent, "Кадр по интервалу");
    if (eventError) {
      return { error: eventError };
    }
    if (draft.tickSource === "lua") {
      if (draft.luaText.trim() === "") {
        return { error: "Кадр по интервалу: функция пуста" };
      }
      stream.tick = {
        intervalMs,
        event: draft.tickEvent === "" ? undefined : draft.tickEvent,
        lua: draft.luaText,
      };
    } else {
      const schema = parseJSON(draft.schemaText, "Схема кадра");
      if ("error" in schema) {
        return { error: schema.error };
      }
      if (
        schema.value === null ||
        typeof schema.value !== "object" ||
        Array.isArray(schema.value)
      ) {
        return { error: "Схема кадра: нужен JSON-объект (JSON Schema)" };
      }
      stream.tick = {
        intervalMs,
        event: draft.tickEvent === "" ? undefined : draft.tickEvent,
        schema: schema.value as Record<string, unknown>,
      };
    }
  }
  if (kind === "ws" && draft.onFrameOn) {
    if (draft.onFrameText.trim() === "") {
      return { error: "Обработка входящих функцией: функция пуста" };
    }
    // The server's own exclusivity (on_frame_and_reactive, on_frame_and_echo)
    // said in the form's words, before a round trip.
    if (draft.repliesOn) {
      return { error: "Обработка входящих функцией исключает правила ответов: выключите одно" };
    }
    if (draft.echo) {
      return { error: "Обработка входящих функцией исключает эхо: выключите одно" };
    }
    stream.onFrame = draft.onFrameText;
  }
  if (kind === "ws" && draft.repliesOn) {
    if (draft.rules.length === 0) {
      return { error: "Ответы на входящие: добавьте хотя бы одно правило" };
    }
    if (draft.rules.length > STREAM_CAPS.maxRules) {
      return { error: `Ответы на входящие: не больше ${STREAM_CAPS.maxRules} правил` };
    }
    const reactive = [];
    for (const [i, r] of draft.rules.entries()) {
      const what = `Правило ${i + 1}`;
      if (r.when.length === 0) {
        return { error: `${what}: добавьте хотя бы одно условие` };
      }
      for (const c of r.when) {
        if (c.name.trim() === "") {
          return { error: `${what}: у условия не заполнено имя` };
        }
      }
      let data: unknown;
      const hasData = r.dataText.trim() !== "";
      if (hasData) {
        const parsed = parseJSON(r.dataText, `${what}, ответ`);
        if ("error" in parsed) {
          return { error: parsed.error };
        }
        data = parsed.value;
      }
      let close;
      if (r.closeOn) {
        const code = integer(r.closeCode, `${what}, код закрытия`, 1000, 4999);
        if (typeof code === "string") {
          return { error: code };
        }
        if (code !== 1000 && code < 4000) {
          return { error: `${what}: код закрытия — 1000 или 4000–4999` };
        }
        if (new TextEncoder().encode(r.closeReason).length > STREAM_CAPS.maxCloseReasonBytes) {
          return {
            error: `${what}: причина закрытия длиннее ${STREAM_CAPS.maxCloseReasonBytes} байт`,
          };
        }
        close = { code, reason: r.closeReason === "" ? undefined : r.closeReason };
      }
      if (!hasData && !close) {
        return { error: `${what}: нужен ответ или закрытие соединения` };
      }
      reactive.push({
        when: r.when.map((c) => ({
          ...c,
          value: c.op === "exists" ? undefined : c.value,
        })),
        data,
        close,
      });
    }
    stream.reactive = reactive;
  }
  if (kind === "ws" && draft.echo) {
    stream.echo = true;
  }
  if (!draft.closeWhenDone) {
    stream.closeWhenDone = false;
  }
  const hasBehaviour =
    stream.timeline !== undefined ||
    stream.tick !== undefined ||
    (stream.reactive?.length ?? 0) > 0 ||
    stream.echo === true ||
    stream.onFrame !== undefined;
  if (!hasBehaviour) {
    return {
      error:
        kind === "ws"
          ? "Включите хотя бы одно поведение: расписание, интервал, ответы, эхо или обработку входящих функцией"
          : "Включите хотя бы одно поведение: расписание или интервал",
    };
  }
  return { stream };
}
