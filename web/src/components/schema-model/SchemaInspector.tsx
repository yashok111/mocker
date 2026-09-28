import {
  Alert,
  Button,
  Checkbox,
  Group,
  NativeSelect,
  Stack,
  Text,
  Textarea,
  TextInput,
} from "@mantine/core";
import type { SchemaModel } from "@/api/generated/schemas";
import { parseSchema, record, type Form } from "./form";
import styles from "./SchemaModel.module.css";

type Props = {
  form: Form;
  model: SchemaModel;
  busy: boolean;
  pending: boolean;
  onChange: (form: Form) => void;
  onApply: () => void;
  onDiscard: () => void;
  onDelete: () => void;
};
export default function SchemaInspector({
  form,
  model,
  busy,
  pending,
  onChange,
  onApply,
  onDiscard,
  onDelete,
}: Props) {
  let parsed: unknown;
  try {
    parsed = parseSchema(form.json);
  } catch {
    parsed = undefined;
  }
  const schema = record(parsed) ? parsed : {};
  const advanced =
    !record(parsed) || ["allOf", "oneOf", "anyOf", "$ref"].some((key) => key in schema);
  const field = form.selection.property !== undefined;
  const change = (key: string, value: string) =>
    onChange({ ...form, edits: { ...form.edits, [key]: value } });
  const value = (key: string) =>
    form.edits[key] ??
    (schema[key] === undefined
      ? ""
      : typeof schema[key] === "string" && !["example", "default"].includes(key)
        ? (schema[key] as string)
        : JSON.stringify(schema[key]));
  return (
    <Stack gap="sm" className={styles.inspector} component="section" aria-label="Инспектор схемы">
      <Text fw={650}>
        {form.selection.create
          ? field
            ? "Новое поле"
            : "Новая схема"
          : field
            ? "Свойства поля"
            : "Свойства схемы"}
      </Text>
      <TextInput
        label={field ? "Имя поля" : "Имя схемы"}
        value={form.name}
        onChange={(e) => onChange({ ...form, name: e.currentTarget.value })}
      />
      {advanced && (
        <Alert color="blue">
          Составная схема, ссылка или boolean: измените JSON ниже. Остальные ключи сохраняются.
        </Alert>
      )}
      <NativeSelect
        label="Тип"
        value={value("type")}
        disabled={advanced}
        data={[
          { value: "", label: "Не задан" },
          "object",
          "array",
          "string",
          "integer",
          "number",
          "boolean",
          "null",
        ]}
        onChange={(e) => change("type", e.currentTarget.value)}
      />
      <TextInput
        label="Формат"
        disabled={advanced}
        value={value("format")}
        onChange={(e) => change("format", e.currentTarget.value)}
        placeholder="uuid, date-time, email…"
      />
      <Textarea
        label="Описание"
        disabled={advanced}
        value={value("description")}
        onChange={(e) => change("description", e.currentTarget.value)}
        autosize
        minRows={2}
      />
      {field && (
        <Checkbox
          label="Обязательное поле"
          checked={form.required}
          onChange={(e) => onChange({ ...form, required: e.currentTarget.checked })}
        />
      )}
      <details>
        <summary>Ограничения и примеры</summary>
        <Stack gap="xs" mt="sm">
          {[
            ["enum", "Допустимые значения (JSON-массив)"],
            ["minimum", "Минимум"],
            ["maximum", "Максимум"],
            ["exclusiveMinimum", "Исключающий минимум"],
            ["exclusiveMaximum", "Исключающий максимум"],
            ["multipleOf", "Кратность"],
            ["minLength", "Минимальная длина"],
            ["maxLength", "Максимальная длина"],
            ["pattern", "Регулярное выражение"],
            ["minItems", "Минимум элементов"],
            ["maxItems", "Максимум элементов"],
            ["example", "Пример (JSON)"],
            ["examples", "Примеры (JSON-массив)"],
            ["default", "По умолчанию (JSON)"],
          ].map(([key, label]) => (
            <TextInput
              key={key}
              label={label}
              disabled={advanced}
              value={value(key!)}
              onChange={(e) => change(key!, e.currentTarget.value)}
            />
          ))}
        </Stack>
      </details>
      {field && (
        <>
          <NativeSelect
            label="Связать со схемой"
            value={form.target}
            onChange={(e) =>
              onChange({ ...form, target: e.currentTarget.value, referenceChanged: true })
            }
            data={[
              { value: "", label: "Без изменения ссылки" },
              ...model.schemas.map((s) => ({ value: s.name, label: s.name })),
            ]}
          />
          <Checkbox
            label="Массив ссылок (items)"
            disabled={!form.target}
            checked={form.array}
            onChange={(e) =>
              onChange({ ...form, array: e.currentTarget.checked, referenceChanged: true })
            }
          />
        </>
      )}
      {!field && (
        <details>
          <summary>Положение карточки</summary>
          <Group grow mt="sm">
            <TextInput
              label="Координата X"
              value={form.x}
              onChange={(e) => onChange({ ...form, x: e.currentTarget.value })}
            />
            <TextInput
              label="Координата Y"
              value={form.y}
              onChange={(e) => onChange({ ...form, y: e.currentTarget.value })}
            />
          </Group>
        </details>
      )}
      <details>
        <summary>Расширенный JSON схемы</summary>
        <Text size="xs" c="dimmed" mt="sm">
          Ввод JSON заменяет настройки этой формы. Неизвестные ключи сохраняются. Для удаления
          ссылки удалите $ref здесь.
        </Text>
        <Textarea
          label="JSON схемы"
          value={form.json}
          onChange={(e) =>
            onChange({
              ...form,
              json: e.currentTarget.value,
              edits: {},
              target: "",
              referenceChanged: false,
            })
          }
          minRows={10}
          autosize
          maxRows={24}
          spellCheck={false}
          styles={{ input: { fontFamily: "var(--mantine-font-family-monospace)", fontSize: 12 } }}
        />
      </details>
      <Group gap="xs">
        <Button onClick={onApply} loading={busy} disabled={!pending}>
          Применить
        </Button>
        <Button variant="default" onClick={onDiscard} disabled={!pending || busy}>
          Сбросить ввод
        </Button>
        {!form.selection.create && (
          <Button color="red" variant="subtle" disabled={pending || busy} onClick={onDelete}>
            {field ? "Удалить поле" : "Удалить схему"}
          </Button>
        )}
      </Group>
    </Stack>
  );
}
