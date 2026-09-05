import { useEffect, useRef, useState } from "react";
import type { ReactElement } from "react";
import {
  ActionIcon,
  Badge,
  Button,
  Card,
  Divider,
  Group,
  NativeSelect,
  Stack,
  Text,
  Textarea,
  TextInput,
} from "@mantine/core";
import { IconPlus, IconTrash } from "@tabler/icons-react";
import { useListAssets } from "@/api/generated/assets/assets.ts";
import type { Condition, Variant } from "@/api/generated/schemas";
import { TabLink } from "./TabLink";

// VariantEditor is the ONE editor of a response variant — the object
// `{mode, body, bodyEncoding, bodyRef, function, schema, schemaPatch,
// recipes, headers, when[], mediaType}` that is the product's unit of
// authoring. Before A21 step 5 (2026-09-05) it was rendered by two divergent
// partial forms, OperationEditor's per-status panel and the custom-endpoint
// edit form, each showing four or five of the eleven fields: that is why
// A18's `function` reached one form and not the other, why `headers`
// reached neither, and why a `bodyRef` variant showed an empty body box
// whose first keystroke was a 400. All three Opus readers of the UI review
// named this refactor independently. Both screens mount this component now;
// the test-id builders keep each screen's existing ids.
//
// The producer is ONE select over four mutually exclusive sources —
// generated (by the schema), a pinned body, a file, a function — and
// switching it clears what the server refuses beside the new source (A18
// D5: function ⊻ body ⊻ bodyRef; a file takes no mediaType; a function takes
// no recipes/schemaPatch either), so the server's exclusivity rules are
// unreachable states here rather than 400s naming fields the operator
// cannot see. `when[]` and `headers` survive every switch: selection and
// headers belong to the variant, not to its producer. What the form still
// cannot author — recipes, schemaPatch, schema — is shown, never dropped
// (the spread), with its own line saying where it comes from.
//
// Validity travels up through onErrorChange, because the owning screen owns
// the save button: a JSON body that does not parse, an empty Lua box (the
// wire reads "" as no function) and a file producer with no file chosen all
// block the save.

export type ProducerMode = "generated" | "pinned" | "file" | "function";

export function producerOf(variant: Variant | undefined): ProducerMode {
  if (variant?.function !== undefined) {
    return "function";
  }
  if (variant?.bodyRef !== undefined && variant.bodyRef !== "") {
    return "file";
  }
  return variant?.mode === "pinned" ? "pinned" : "generated";
}

const DEFAULT_CONDITION: Condition = { in: "query", name: "", op: "equals", value: "" };

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

function jsonLocation(text: string, err: unknown): string {
  const message = err instanceof Error ? err.message : String(err);
  const match = /position (\d+)/.exec(message);
  if (!match) {
    return message;
  }
  const pos = Number(match[1]);
  const before = text.slice(0, pos);
  const line = before.split("\n").length;
  const column = pos - before.lastIndexOf("\n");
  return `строка ${line}, столбец ${column}`;
}

export function VariantEditor({
  workspaceId,
  variant,
  updateVariant,
  onErrorChange,
  testId,
  whenTestId,
  hasSchema,
}: {
  workspaceId: number;
  variant: Variant | undefined;
  /** Every caller passes an updater that spreads its argument first, so
   * whatever this editor is not showing survives untouched. */
  updateVariant: (updater: (v: Variant) => Variant) => void;
  onErrorChange: (hasError: boolean) => void;
  /** `testId("body")` → the screen's own id for the body box, etc. */
  testId: (name: string) => string;
  /** `whenTestId("name", 0)` → the id of the first condition's name field. */
  whenTestId: (name: string, index: number) => string;
  /** Whether "generated" has a schema to generate from: a spec operation
   * always does; a custom endpoint only when the agent gave it one (P7a). */
  hasSchema: boolean;
}): ReactElement {
  const producer = producerOf(variant);
  const functionText = variant?.function ?? "";
  const functionEmpty = producer === "function" && functionText.trim() === "";
  const fileEmpty = producer === "file" && (variant?.bodyRef ?? "") === "asset:";
  const when = variant?.when ?? [];
  const headers = Object.entries(variant?.headers ?? {});
  const recipeEntries = Object.entries(variant?.recipes ?? {});
  const schemaPatchCount = Array.isArray(variant?.schemaPatch) ? variant.schemaPatch.length : 0;

  // The assets list feeds the file producer's select; fetched only while
  // that producer is chosen, so the other three cost no request.
  const assets = useListAssets(workspaceId, { query: { enabled: producer === "file" } });
  const assetNames = assets.data?.status === 200 ? assets.data.data.assets.map((a) => a.name) : [];

  // The body textarea keeps its own draft text so a JSON parse error mid-edit
  // never overwrites the last VALID body sitting in the document — only a
  // successful parse ever calls updateVariant.
  const [bodyDraft, setBodyDraft] = useState<string | null>(null);
  const [bodyError, setBodyError] = useState<string | null>(null);
  const serverBodyText = JSON.stringify(variant?.body ?? {}, null, 2);
  const bodyText = bodyDraft ?? serverBodyText;

  // lastCommittedBodyRef holds the serverBodyText we expect to see NEXT as a
  // result of our OWN most recent successful edit. Whenever serverBodyText
  // disagrees with it, the body changed from OUTSIDE this editor («Сбросить
  // к спеке» sets it back to undefined without unmounting this component,
  // since the owning screen keeps every status keyed for the life of its
  // tab list) — the stale draft is dropped, or a save would submit no body
  // while the screen shows the old one.
  const lastCommittedBodyRef = useRef<string>(serverBodyText);
  useEffect(() => {
    if (serverBodyText !== lastCommittedBodyRef.current) {
      lastCommittedBodyRef.current = serverBodyText;
      setBodyDraft(null);
      setBodyError(null);
    }
  }, [serverBodyText]);

  // Read the callback through a ref rather than depending on it directly:
  // the parent passes a fresh closure every render, and depending on it
  // would re-fire this effect on every keystroke elsewhere in the document.
  const onErrorChangeRef = useRef(onErrorChange);
  useEffect(() => {
    onErrorChangeRef.current = onErrorChange;
  });
  useEffect(() => {
    onErrorChangeRef.current(bodyError !== null || functionEmpty || fileEmpty);
  }, [bodyError, functionEmpty, fileEmpty]);
  useEffect(() => {
    return () => onErrorChangeRef.current(false);
  }, []);

  function handleProducerChange(next: string): void {
    switch (next) {
      case "function":
        updateVariant((v) => ({
          ...v,
          mode: "generated",
          body: undefined,
          bodyEncoding: undefined,
          bodyRef: undefined,
          mediaType: undefined,
          recipes: undefined,
          schemaPatch: undefined,
          function: v.function ?? "",
        }));
        return;
      case "file":
        // "asset:" with no name is the "chosen, not picked yet" state; fileEmpty
        // blocks the save until a file is picked. A file takes no mediaType
        // (the asset's own is served) and no body.
        updateVariant((v) => ({
          ...v,
          mode: "pinned",
          body: undefined,
          bodyEncoding: undefined,
          mediaType: undefined,
          function: undefined,
          bodyRef: v.bodyRef ?? "asset:",
        }));
        return;
      case "pinned":
        updateVariant((v) => ({ ...v, mode: "pinned", bodyRef: undefined, function: undefined }));
        return;
      default:
        updateVariant((v) => ({
          ...v,
          mode: "generated",
          bodyRef: undefined,
          function: undefined,
        }));
    }
  }

  function handleBodyChange(text: string): void {
    setBodyDraft(text);
    try {
      const parsed: unknown = JSON.parse(text);
      setBodyError(null);
      lastCommittedBodyRef.current = JSON.stringify(parsed, null, 2);
      updateVariant((v) => ({ ...v, body: parsed }));
    } catch (err) {
      setBodyError(`JSON невалиден (${jsonLocation(text, err)})`);
    }
  }

  function setHeader(index: number, key: string, value: string): void {
    updateVariant((v) => {
      const entries = Object.entries(v.headers ?? {});
      entries[index] = [key, value];
      return { ...v, headers: Object.fromEntries(entries) };
    });
  }

  function removeHeader(index: number): void {
    updateVariant((v) => {
      const entries = Object.entries(v.headers ?? {}).filter((_, i) => i !== index);
      return { ...v, headers: entries.length === 0 ? undefined : Object.fromEntries(entries) };
    });
  }

  function patchCondition(index: number, patch: Partial<Condition>): void {
    updateVariant((v) => ({
      ...v,
      when: (v.when ?? []).map((c, i) => (i === index ? { ...c, ...patch } : c)),
    }));
  }

  return (
    <Stack gap="sm">
      <NativeSelect
        label="Откуда берётся ответ"
        data-testid={testId("mode")}
        value={producer}
        onChange={(e) => handleProducerChange(e.currentTarget.value)}
      >
        <option value="generated">
          {hasSchema ? "сгенерированный по схеме" : "сгенерированный (схемы нет — пустое тело)"}
        </option>
        <option value="pinned">закреплённое тело</option>
        <option value="file">файл с вкладки «Файлы»</option>
        <option value="function">функция (Lua)</option>
      </NativeSelect>

      {producer === "function" ? (
        <Textarea
          label="Функция (Lua) — над аргументом req, возвращает status, body, headers"
          description="Раздел «Функции» в руководстве. Компилируется при сохранении: синтаксическая ошибка — отказ со словами парсера."
          rows={6}
          styles={{ input: { fontFamily: "var(--mantine-font-family-monospace)" } }}
          data-testid={testId("function")}
          error={functionEmpty ? "Функция пуста" : undefined}
          value={functionText}
          onChange={(e) => {
            // Read the event BEFORE the updater: the owning screen runs
            // updaters lazily inside setState, when currentTarget is gone.
            const text = e.currentTarget.value;
            updateVariant((v) => ({ ...v, function: text }));
          }}
        />
      ) : producer === "file" ? (
        <Stack gap={4} data-testid={testId("body-ref")}>
          <NativeSelect
            label="Файл — отдаётся как есть, со своим media type"
            data-testid={testId("file")}
            value={(variant?.bodyRef ?? "asset:").replace(/^asset:/, "")}
            error={fileEmpty ? "Выберите файл" : undefined}
            onChange={(e) => {
              const name = e.currentTarget.value;
              updateVariant((v) => ({ ...v, bodyRef: `asset:${name}` }));
            }}
          >
            <option value="">— выберите файл —</option>
            {assetNames.map((name) => (
              <option key={name} value={name}>
                {name}
              </option>
            ))}
            {/* A stored name the list does not (yet) carry stays selectable
                rather than snapping to the placeholder. */}
            {variant?.bodyRef !== undefined &&
            variant.bodyRef !== "asset:" &&
            !assetNames.includes(variant.bodyRef.replace(/^asset:/, "")) ? (
              <option value={variant.bodyRef.replace(/^asset:/, "")}>
                {variant.bodyRef.replace(/^asset:/, "")}
              </option>
            ) : null}
          </NativeSelect>
          <Text size="xs" c="dimmed">
            Загрузить или заменить файл —{" "}
            <TabLink id={workspaceId} tab="assets">
              вкладка «Файлы»
            </TabLink>
          </Text>
        </Stack>
      ) : producer === "pinned" ? (
        <>
          <TextInput
            label="Media type"
            placeholder="application/json"
            data-testid={testId("media-type")}
            value={variant?.mediaType ?? ""}
            onChange={(e) => {
              const mediaType = e.currentTarget.value;
              updateVariant((v) => ({
                ...v,
                mediaType: mediaType === "" ? undefined : mediaType,
              }));
            }}
          />
          <Textarea
            label="Тело ответа, JSON"
            rows={6}
            data-testid={testId("body")}
            error={bodyError}
            value={bodyText}
            onChange={(e) => handleBodyChange(e.currentTarget.value)}
          />
        </>
      ) : (
        <Text size="sm" c="dimmed" data-testid={testId("generated-note")}>
          {hasSchema
            ? "Тело строится по схеме — переключите на «закреплённое тело», чтобы задать его вручную"
            : "Схемы у этого статуса нет: сгенерировать нечего, ответ будет пустым — задайте тело, файл или функцию"}
        </Text>
      )}

      {recipeEntries.length > 0 || schemaPatchCount > 0 ? (
        <Card withBorder p="xs" data-testid={testId("recipes")}>
          {recipeEntries.length > 0 ? (
            <>
              <Text size="xs" fw={600} c="dimmed">
                Автоматические значения на этом статусе ({recipeEntries.length}) — редактирование
                появится позже, здесь только показ, чтобы было видно, откуда взялось тело
              </Text>
              <Group gap={4} mt={4}>
                {recipeEntries.map(([path, recipe]) => (
                  <Badge key={path} size="sm" variant="light">
                    {path}: {recipe.kind}
                  </Badge>
                ))}
              </Group>
            </>
          ) : null}
          {schemaPatchCount > 0 ? (
            <Text size="xs" c="dimmed" mt={recipeEntries.length > 0 ? 4 : 0}>
              Правок схемы на этом статусе: {schemaPatchCount} — записаны агентом, сохраняются как
              есть
            </Text>
          ) : null}
        </Card>
      ) : null}

      <Divider label="Заголовки ответа" labelPosition="left" />
      <Stack gap="xs" data-testid={testId("headers")}>
        {headers.map(([key, value], index) => (
          // Index-keyed: entries are edited in place, never reordered.
          // eslint-disable-next-line react/no-array-index-key
          <Group key={index} gap="xs" wrap="nowrap" align="flex-end">
            <TextInput
              label="Заголовок"
              placeholder="X-Request-Id"
              data-testid={testId(`header-name-${index}`)}
              value={key}
              onChange={(e) => setHeader(index, e.currentTarget.value, value)}
            />
            <TextInput
              label="Значение"
              data-testid={testId(`header-value-${index}`)}
              value={value}
              onChange={(e) => setHeader(index, key, e.currentTarget.value)}
            />
            <ActionIcon
              variant="default"
              color="red"
              onClick={() => removeHeader(index)}
              data-testid={testId(`header-remove-${index}`)}
              aria-label="Удалить заголовок"
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
            updateVariant((v) => ({ ...v, headers: { ...(v.headers ?? {}), "": "" } }))
          }
          data-testid={testId("header-add")}
        >
          Добавить заголовок
        </Button>
      </Stack>

      <Divider label="Когда отвечать так" labelPosition="left" />
      <Text size="xs" c="dimmed">
        Все условия ниже должны совпасть, иначе используется обычная генерация
      </Text>
      <Stack gap="xs" data-testid={testId("when")}>
        {when.map((cond, index) => (
          // Index-keyed on purpose: conditions carry no id of their own and
          // this list is edited in place, never reordered.
          // eslint-disable-next-line react/no-array-index-key
          <Group key={index} gap="xs" wrap="nowrap" align="flex-end">
            <NativeSelect
              label="Где"
              data-testid={whenTestId("in", index)}
              value={cond.in}
              onChange={(e) =>
                patchCondition(index, { in: e.currentTarget.value as Condition["in"] })
              }
            >
              {IN_OPTIONS.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </NativeSelect>
            <TextInput
              label="Имя"
              data-testid={whenTestId("name", index)}
              value={cond.name}
              onChange={(e) => patchCondition(index, { name: e.currentTarget.value })}
            />
            <NativeSelect
              label="Условие"
              data-testid={whenTestId("op", index)}
              value={cond.op}
              onChange={(e) =>
                patchCondition(index, { op: e.currentTarget.value as Condition["op"] })
              }
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
              data-testid={whenTestId("value", index)}
              value={cond.value ?? ""}
              onChange={(e) => patchCondition(index, { value: e.currentTarget.value })}
            />
            <ActionIcon
              variant="default"
              color="red"
              onClick={() =>
                updateVariant((v) => ({
                  ...v,
                  when: (v.when ?? []).filter((_, i) => i !== index),
                }))
              }
              data-testid={whenTestId("remove", index)}
              aria-label="Удалить условие"
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
            updateVariant((v) => ({ ...v, when: [...(v.when ?? []), DEFAULT_CONDITION] }))
          }
          data-testid={testId("when-add")}
        >
          Добавить условие
        </Button>
      </Stack>
    </Stack>
  );
}
