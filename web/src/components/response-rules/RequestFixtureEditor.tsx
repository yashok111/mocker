import { Button, Checkbox, Group, Stack, Text, Textarea, TextInput } from "@mantine/core";
import type { ResponseRuleField, ResponseRuleRequest } from "@/api/generated/schemas";
import styles from "./ResponseRules.module.css";

export function FieldRows({
  rows,
  label,
  addLabel,
  onChange,
}: {
  rows: ResponseRuleField[];
  label: string;
  addLabel: string;
  onChange: (rows: ResponseRuleField[]) => void;
}) {
  return (
    <Stack gap="xs">
      {rows.map((row, index) => (
        <div className={styles.fieldRow} key={index}>
          <TextInput
            label={`${label} ${index + 1}`}
            value={row.name}
            onChange={(event) =>
              onChange(
                rows.map((item, i) =>
                  i === index ? { ...item, name: event.currentTarget.value } : item,
                ),
              )
            }
          />
          <TextInput
            label={`Значение: ${label.toLowerCase()} ${index + 1}`}
            value={row.value}
            onChange={(event) =>
              onChange(
                rows.map((item, i) =>
                  i === index ? { ...item, value: event.currentTarget.value } : item,
                ),
              )
            }
          />
          <Button
            size="compact-xs"
            variant="subtle"
            color="red"
            aria-label={`Удалить: ${label.toLowerCase()} ${index + 1}`}
            onClick={() => onChange(rows.filter((_, i) => i !== index))}
          >
            Удалить
          </Button>
        </div>
      ))}
      <Group>
        <Button
          size="xs"
          variant="default"
          disabled={rows.length >= 100}
          onClick={() => onChange([...rows, { name: "", value: "" }])}
        >
          {addLabel}
        </Button>
      </Group>
    </Stack>
  );
}

export default function RequestFixtureEditor({
  value,
  onChange,
}: {
  value: ResponseRuleRequest;
  onChange: (request: ResponseRuleRequest) => void;
}) {
  return (
    <Stack gap="sm" aria-label="Пример запроса">
      <Text fw={600}>Пример запроса</Text>
      <Text size="xs" c="dimmed">
        Порядок и повторения сохраняются. Заголовки сравниваются без учёта регистра; используется
        первое значение. Пример хранится только до закрытия вкладки.
      </Text>
      <FieldRows
        rows={value.query}
        label="Query-параметр"
        addLabel="Добавить query-параметр"
        onChange={(query) => onChange({ ...value, query })}
      />
      <FieldRows
        rows={value.headers}
        label="Заголовок запроса"
        addLabel="Добавить заголовок запроса"
        onChange={(headers) => onChange({ ...value, headers })}
      />
      <Checkbox
        label="Тело запроса JSON"
        checked={value.bodyJSON !== undefined}
        onChange={(event) => {
          if (event.currentTarget.checked) onChange({ ...value, bodyJSON: "null" });
          else {
            const { bodyJSON: _, ...withoutBody } = value;
            onChange(withoutBody);
          }
        }}
      />
      {value.bodyJSON !== undefined && (
        <Textarea
          label="JSON запроса"
          autosize
          minRows={3}
          maxRows={12}
          value={value.bodyJSON}
          onChange={(event) => onChange({ ...value, bodyJSON: event.currentTarget.value })}
        />
      )}
    </Stack>
  );
}
