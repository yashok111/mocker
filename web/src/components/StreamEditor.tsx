import type { ReactElement } from "react";
import {
  ActionIcon,
  Button,
  Card,
  Checkbox,
  Divider,
  Group,
  NativeSelect,
  Stack,
  Text,
  Textarea,
  TextInput,
} from "@mantine/core";
import { IconPlus, IconTrash } from "@tabler/icons-react";
import type { Condition } from "@/api/generated/schemas";
import {
  DEFAULT_CONDITION,
  emptyFrame,
  emptyRule,
  type FrameDraft,
  type RuleDraft,
  type StreamDraft,
  type StreamKind,
} from "./stream/streamDraft";

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
