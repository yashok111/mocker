import type { ReactElement } from "react";
import { useState } from "react";
import {
  ActionIcon,
  Alert,
  Button,
  Card,
  Checkbox,
  Divider,
  Group,
  NativeSelect,
  Stack,
  Table,
  Text,
  Textarea,
  TextInput,
} from "@mantine/core";
import { IconAlertTriangle, IconCalculator, IconPlus, IconTrash } from "@tabler/icons-react";
import { usePreviewEndpoint } from "@/api/generated/endpoints/endpoints.ts";
import type {
  Condition,
  ServerConfigViewLimits,
  StreamDefinition,
  StreamPreviewView,
} from "@/api/generated/schemas";
import { describeApiFailureDetailed } from "@/api/errors";

// StreamEditor is DESIGN §30.14's authoring half, P6e: on the custom-endpoints
// screen a stream (kind "sse" or "ws") swaps the body editor for the four
// behaviours PHRASED AS TASKS. §14 forbids "recipe", "JSON patch" and
// "matcher" in the interface and §30.14 puts "timeline", "reactive" and
// "tick" under the same rule, so the sections below are named by what the
// operator wants to happen — send frames on a schedule, generate a frame
// every N ms, answer incoming messages, echo — and the wire words appear
// only in this comment and in the request the form sends.
//
// The reply rules reuse the operation editor's `when[]` vocabulary verbatim
// (the same IN/OP labels and the same four-field row): one condition language
// in the product, not two. The server validates the whole document by name
// (internal/customep/stream.go); the checks here mirror the ones a form can
// answer BEFORE a round trip — a non-integer, an interval under the floor,
// a frame whose data is not JSON — and never clamp, exactly as the server
// never clamps.

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

const DEFAULT_CONDITION: Condition = { in: "body", name: "", op: "equals", value: "" };

const IN_OPTIONS: { value: Condition["in"]; label: string }[] = [
  { value: "query", label: "query-параметр" },
  { value: "header", label: "заголовок" },
  { value: "body", label: "тело" },
];

const OP_OPTIONS: { value: Condition["op"]; label: string }[] = [
  { value: "equals", label: "равно" },
  { value: "contains", label: "содержит" },
  { value: "exists", label: "присутствует" },
];

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
    const message = err instanceof Error ? err.message : String(err);
    return { error: `${what}: JSON невалиден (${message})` };
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

export function StreamEditor({
  kind,
  draft,
  onChange,
  testIdPrefix,
}: {
  kind: StreamKind;
  draft: StreamDraft;
  onChange: (next: StreamDraft) => void;
  testIdPrefix: string;
}): ReactElement {
  const t = (name: string) => `${testIdPrefix}-${name}`;
  const patch = (p: Partial<StreamDraft>) => onChange({ ...draft, ...p });
  const patchFrame = (i: number, p: Partial<FrameDraft>) =>
    patch({ frames: draft.frames.map((f, j) => (j === i ? { ...f, ...p } : f)) });
  const patchRule = (i: number, p: Partial<RuleDraft>) =>
    patch({ rules: draft.rules.map((r, j) => (j === i ? { ...r, ...p } : r)) });
  const patchCondition = (ri: number, ci: number, p: Partial<Condition>) =>
    patchRule(ri, {
      when: (draft.rules[ri]?.when ?? []).map((c, j) => (j === ci ? { ...c, ...p } : c)),
    });

  return (
    <Stack gap="sm" data-testid={t("stream-editor")}>
      <Divider label="Отправлять кадры по расписанию" labelPosition="left" />
      <Checkbox
        label="Включить"
        checked={draft.scheduleOn}
        onChange={(e) => patch({ scheduleOn: e.currentTarget.checked })}
        data-testid={t("schedule-on")}
      />
      {draft.scheduleOn ? (
        <Stack gap="xs">
          {draft.frames.map((frame, index) => (
            // Index-keyed on purpose: frames carry no id and are edited in
            // place, never reordered.
            // eslint-disable-next-line react/no-array-index-key
            <Group key={index} gap="xs" wrap="nowrap" align="flex-start">
              <TextInput
                label="Пауза перед кадром, мс"
                w={170}
                value={frame.delayMs}
                onChange={(e) => patchFrame(index, { delayMs: e.currentTarget.value })}
                data-testid={t(`frame-delay-${index}`)}
              />
              <TextInput
                label="Событие (необязательно)"
                w={170}
                value={frame.event}
                onChange={(e) => patchFrame(index, { event: e.currentTarget.value })}
                data-testid={t(`frame-event-${index}`)}
              />
              <Textarea
                label="Данные, JSON"
                rows={2}
                style={{ flex: 1 }}
                value={frame.dataText}
                onChange={(e) => patchFrame(index, { dataText: e.currentTarget.value })}
                data-testid={t(`frame-data-${index}`)}
              />
              <ActionIcon
                variant="default"
                color="red"
                mt={28}
                onClick={() => patch({ frames: draft.frames.filter((_, j) => j !== index) })}
                aria-label="Удалить кадр"
                data-testid={t(`frame-remove-${index}`)}
              >
                <IconTrash size={16} />
              </ActionIcon>
            </Group>
          ))}
          <Group gap="md">
            <Button
              variant="default"
              size="xs"
              leftSection={<IconPlus size={14} />}
              onClick={() => patch({ frames: [...draft.frames, emptyFrame()] })}
              data-testid={t("frame-add")}
            >
              Добавить кадр
            </Button>
            <Checkbox
              label="Повторять по кругу"
              checked={draft.loop}
              onChange={(e) => patch({ loop: e.currentTarget.checked })}
              data-testid={t("loop")}
            />
            <Checkbox
              label="Закрыть соединение после последнего кадра"
              checked={draft.closeWhenDone}
              onChange={(e) => patch({ closeWhenDone: e.currentTarget.checked })}
              data-testid={t("close-when-done")}
            />
          </Group>
        </Stack>
      ) : null}

      <Divider label="Генерировать кадр по интервалу" labelPosition="left" />
      <Checkbox
        label="Включить"
        checked={draft.intervalOn}
        onChange={(e) => patch({ intervalOn: e.currentTarget.checked })}
        data-testid={t("interval-on")}
      />
      {draft.intervalOn ? (
        <Stack gap="xs">
          <Group grow align="flex-start">
            <TextInput
              label="Интервал, мс"
              value={draft.intervalMs}
              onChange={(e) => patch({ intervalMs: e.currentTarget.value })}
              data-testid={t("interval-ms")}
            />
            <TextInput
              label="Событие (необязательно)"
              value={draft.tickEvent}
              onChange={(e) => patch({ tickEvent: e.currentTarget.value })}
              data-testid={t("interval-event")}
            />
          </Group>
          <NativeSelect
            label="Откуда брать тело кадра"
            value={draft.tickSource}
            onChange={(e) =>
              patch({ tickSource: e.currentTarget.value === "lua" ? "lua" : "schema" })
            }
            data-testid={t("interval-source")}
          >
            <option value="schema">по схеме — генерируется, как обычный ответ</option>
            <option value="lua">функцией на Lua — раздел «Функция эндпоинта» в руководстве</option>
          </NativeSelect>
          {draft.tickSource === "lua" ? (
            <Textarea
              label="Функция (Lua): тело над аргументом ordinal, возвращает таблицу, строку или nil"
              rows={6}
              value={draft.luaText}
              onChange={(e) => patch({ luaText: e.currentTarget.value })}
              styles={{ input: { fontFamily: "var(--mantine-font-family-monospace)" } }}
              data-testid={t("interval-lua")}
            />
          ) : (
            <Textarea
              label="Схема кадра (JSON Schema)"
              rows={4}
              value={draft.schemaText}
              onChange={(e) => patch({ schemaText: e.currentTarget.value })}
              data-testid={t("interval-schema")}
            />
          )}
        </Stack>
      ) : null}

      {kind === "ws" ? (
        <>
          <Divider label="Отвечать на входящие сообщения" labelPosition="left" />
          <Checkbox
            label="Включить"
            checked={draft.repliesOn}
            onChange={(e) => patch({ repliesOn: e.currentTarget.checked })}
            data-testid={t("replies-on")}
          />
          {draft.repliesOn ? (
            <Stack gap="sm">
              <Text size="xs" c="dimmed">
                Входящее сообщение — JSON-объект; «тело» ниже — его ключи верхнего уровня. Правила
                проверяются по порядку, срабатывает первое, у которого совпали все условия.
              </Text>
              {draft.rules.map((rule, ri) => (
                // eslint-disable-next-line react/no-array-index-key
                <Card key={ri} withBorder p="sm" data-testid={t(`rule-${ri}`)}>
                  <Stack gap="xs">
                    <Group justify="space-between">
                      <Text size="sm" fw={500}>
                        Правило {ri + 1}
                      </Text>
                      <ActionIcon
                        variant="default"
                        color="red"
                        onClick={() => patch({ rules: draft.rules.filter((_, j) => j !== ri) })}
                        aria-label="Удалить правило"
                        data-testid={t(`rule-remove-${ri}`)}
                      >
                        <IconTrash size={16} />
                      </ActionIcon>
                    </Group>
                    {rule.when.map((cond, ci) => (
                      // eslint-disable-next-line react/no-array-index-key
                      <Group key={ci} gap="xs" wrap="nowrap" align="flex-end">
                        <NativeSelect
                          label="Где"
                          value={cond.in}
                          onChange={(e) =>
                            patchCondition(ri, ci, { in: e.currentTarget.value as Condition["in"] })
                          }
                          data-testid={t(`rule-when-in-${ri}-${ci}`)}
                        >
                          {IN_OPTIONS.map((o) => (
                            <option key={o.value} value={o.value}>
                              {o.label}
                            </option>
                          ))}
                        </NativeSelect>
                        <TextInput
                          label="Имя"
                          value={cond.name}
                          onChange={(e) => patchCondition(ri, ci, { name: e.currentTarget.value })}
                          data-testid={t(`rule-when-name-${ri}-${ci}`)}
                        />
                        <NativeSelect
                          label="Условие"
                          value={cond.op}
                          onChange={(e) =>
                            patchCondition(ri, ci, { op: e.currentTarget.value as Condition["op"] })
                          }
                          data-testid={t(`rule-when-op-${ri}-${ci}`)}
                        >
                          {OP_OPTIONS.map((o) => (
                            <option key={o.value} value={o.value}>
                              {o.label}
                            </option>
                          ))}
                        </NativeSelect>
                        <TextInput
                          label="Значение"
                          disabled={cond.op === "exists"}
                          value={cond.value ?? ""}
                          onChange={(e) => patchCondition(ri, ci, { value: e.currentTarget.value })}
                          data-testid={t(`rule-when-value-${ri}-${ci}`)}
                        />
                        <ActionIcon
                          variant="default"
                          color="red"
                          onClick={() =>
                            patchRule(ri, { when: rule.when.filter((_, j) => j !== ci) })
                          }
                          aria-label="Удалить условие"
                          data-testid={t(`rule-when-remove-${ri}-${ci}`)}
                        >
                          <IconTrash size={16} />
                        </ActionIcon>
                      </Group>
                    ))}
                    <Button
                      variant="default"
                      size="xs"
                      w="fit-content"
                      leftSection={<IconPlus size={14} />}
                      onClick={() =>
                        patchRule(ri, { when: [...rule.when, { ...DEFAULT_CONDITION }] })
                      }
                      data-testid={t(`rule-when-add-${ri}`)}
                    >
                      Добавить условие
                    </Button>
                    <Textarea
                      label="Ответ, JSON (пусто — не отвечать)"
                      rows={2}
                      value={rule.dataText}
                      onChange={(e) => patchRule(ri, { dataText: e.currentTarget.value })}
                      data-testid={t(`rule-data-${ri}`)}
                    />
                    <Group gap="md" align="flex-end">
                      <Checkbox
                        label="Закрыть соединение после ответа"
                        checked={rule.closeOn}
                        onChange={(e) => patchRule(ri, { closeOn: e.currentTarget.checked })}
                        data-testid={t(`rule-close-on-${ri}`)}
                      />
                      {rule.closeOn ? (
                        <>
                          <TextInput
                            label="Код закрытия (1000 или 4000–4999)"
                            w={220}
                            value={rule.closeCode}
                            onChange={(e) => patchRule(ri, { closeCode: e.currentTarget.value })}
                            data-testid={t(`rule-close-code-${ri}`)}
                          />
                          <TextInput
                            label="Причина (необязательно)"
                            value={rule.closeReason}
                            onChange={(e) => patchRule(ri, { closeReason: e.currentTarget.value })}
                            data-testid={t(`rule-close-reason-${ri}`)}
                          />
                        </>
                      ) : null}
                    </Group>
                  </Stack>
                </Card>
              ))}
              <Button
                variant="default"
                size="xs"
                w="fit-content"
                leftSection={<IconPlus size={14} />}
                onClick={() => patch({ rules: [...draft.rules, emptyRule()] })}
                data-testid={t("rule-add")}
              >
                Добавить правило
              </Button>
            </Stack>
          ) : null}

          <Divider label="Возвращать входящие сообщения как есть" labelPosition="left" />
          <Checkbox
            label="Включить (если ни одно правило не совпало)"
            checked={draft.echo}
            onChange={(e) => patch({ echo: e.currentTarget.checked })}
            data-testid={t("echo")}
          />

          <Divider label="Обрабатывать входящие сообщения функцией" labelPosition="left" />
          <Checkbox
            label="Включить (вместо правил и эха — вместе они не сохраняются)"
            checked={draft.onFrameOn}
            onChange={(e) => patch({ onFrameOn: e.currentTarget.checked })}
            data-testid={t("on-frame-on")}
          />
          {draft.onFrameOn ? (
            <Textarea
              label="Функция (Lua): тело над аргументом frame; вернуть nil, («reply», данные) или («close», код, причина)"
              rows={6}
              value={draft.onFrameText}
              onChange={(e) => patch({ onFrameText: e.currentTarget.value })}
              styles={{ input: { fontFamily: "var(--mantine-font-family-monospace)" } }}
              data-testid={t("on-frame-lua")}
            />
          ) : null}
        </>
      ) : null}
    </Stack>
  );
}

function formatBytesPerSec(n: number): string {
  if (n >= 1024 * 1024) {
    return `${(n / (1024 * 1024)).toFixed(1)} МБ/с`;
  }
  if (n >= 1024) {
    return `${(n / 1024).toFixed(1)} КБ/с`;
  }
  return `${n} Б/с`;
}

/** StreamCapsStrip is §30.14's read-only strip: the server's effective caps
 * and, on request, the draft's own first frames and the maximum output one
 * connection would produce — POST .../endpoints/preview writes nothing, so
 * pressing the button is free. The number is the amplifier §30.12 wants
 * shown BEFORE a loop is saved: a 4 MiB frame every 100 ms is 40 MiB/s per
 * connection, and the cap on connections multiplies it. */
function formatBytes(n: number): string {
  if (n >= 1024 * 1024) {
    return `${(n / (1024 * 1024)).toFixed(n % (1024 * 1024) === 0 ? 0 : 1)} МБ`;
  }
  if (n >= 1024) {
    return `${(n / 1024).toFixed(n % 1024 === 0 ? 0 : 1)} КБ`;
  }
  return `${n} Б`;
}

export function StreamCapsStrip({
  workspaceId,
  path,
  kind,
  draft,
  limits,
  testIdPrefix,
}: {
  workspaceId: number;
  path: string;
  kind: StreamKind;
  draft: StreamDraft;
  /** The server's effective limits (A9, from the session's config). Absent
   * — a component mounted without a session — the strip names the
   * variables instead of inventing numbers. */
  limits?: ServerConfigViewLimits;
  testIdPrefix: string;
}): ReactElement {
  const t = (name: string) => `${testIdPrefix}-${name}`;
  const [result, setResult] = useState<StreamPreviewView | null>(null);
  const [draftError, setDraftError] = useState<string | null>(null);
  const preview = usePreviewEndpoint();

  function run(): void {
    const def = draftToDefinition(kind, draft);
    if ("error" in def) {
      setDraftError(def.error);
      setResult(null);
      return;
    }
    setDraftError(null);
    preview.mutate(
      {
        id: workspaceId,
        data: { method: "GET", path: path.trim() || "/", kind, stream: def.stream },
      },
      { onSuccess: (res) => setResult(res.status === 200 ? res.data : null) },
    );
  }

  return (
    <Card withBorder p="sm" data-testid={t("caps")}>
      <Stack gap="xs">
        <Text size="xs" c="dimmed" data-testid={t("caps-text")}>
          Лимиты сервера: до {STREAM_CAPS.maxFrames} кадров в расписании, пауза до{" "}
          {STREAM_CAPS.maxFrameDelayMs.toLocaleString("ru-RU")} мс, интервал от{" "}
          {STREAM_CAPS.minTickIntervalMs} мс, до {STREAM_CAPS.maxRules} правил
          {limits
            ? `, кадр не больше ${formatBytes(limits.maxResponseBytes)}. Соединений на воркспейс — ${limits.streamMaxConns}, время жизни — ${limits.streamMaxLifetimeSec} с` +
              (kind === "ws"
                ? `, входящий кадр до ${formatBytes(limits.streamMaxFrameBytes)}, очередь ответов ${formatBytes(limits.streamSendBudgetBytes)}`
                : "")
            : ", кадр не больше MOCKER_MAX_RESPONSE. Соединений на воркспейс — MOCKER_STREAM_MAX_CONNS, время жизни — MOCKER_STREAM_MAX_LIFETIME"}
          .
        </Text>
        <Group gap="sm">
          <Button
            variant="default"
            size="xs"
            leftSection={<IconCalculator size={14} />}
            loading={preview.isPending}
            onClick={run}
            data-testid={t("preview-run")}
          >
            Рассчитать кадры
          </Button>
          {result ? (
            <Text size="sm" data-testid={t("preview-rate")}>
              {result.nominalRate
                ? "Оценка по одному запуску функции, не потолок: "
                : "Максимум на одно соединение: "}
              <strong>{formatBytesPerSec(result.maxBytesPerSec)}</strong>
              {result.truncated ? " · показаны первые кадры, поток длиннее" : ""}
              {kind === "ws" ? ` · правил: ${result.rules}${result.echo ? ", эхо" : ""}` : ""}
            </Text>
          ) : null}
        </Group>
        {draftError ? (
          <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
            {draftError}
          </Alert>
        ) : null}
        {preview.isError ? (
          <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
            {describeApiFailureDetailed(preview.error)}
          </Alert>
        ) : null}
        {result && result.frames.length > 0 ? (
          <Table.ScrollContainer minWidth={400} mah={240}>
            <Table fz="xs" data-testid={t("preview-frames")}>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>мс</Table.Th>
                  <Table.Th>событие</Table.Th>
                  <Table.Th>данные</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {result.frames.map((f, i) => (
                  // eslint-disable-next-line react/no-array-index-key
                  <Table.Tr key={i}>
                    <Table.Td>{f.atMs}</Table.Td>
                    <Table.Td>{f.event ?? "—"}</Table.Td>
                    <Table.Td>
                      {f.notRun ? (
                        <Text size="xs" c="dimmed" component="span">
                          тело не вычислялось
                        </Text>
                      ) : (
                        <code>{JSON.stringify(f.data)}</code>
                      )}
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        ) : null}
      </Stack>
    </Card>
  );
}
