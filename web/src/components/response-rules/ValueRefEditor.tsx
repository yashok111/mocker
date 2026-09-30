import { NativeSelect, Stack, Text, Textarea, TextInput } from "@mantine/core";
import type { ResponseRuleValueRef } from "@/api/generated/schemas";
import type { ResponseRule } from "./model";

export default function ValueRefEditor({
  value,
  label,
  sourceLabel,
  rule,
  onChange,
}: {
  value: ResponseRuleValueRef;
  label: string;
  sourceLabel?: string;
  rule: ResponseRule;
  onChange: (value: ResponseRuleValueRef) => void;
}) {
  const producers = rule.nodes.filter((node) => node.type.startsWith("entity_"));
  const resultOptions = [
    { value: "", label: "Выберите узел" },
    ...producers.map((node) => ({ value: node.id, label: node.name || node.id })),
  ];
  if (
    value.source === "result" &&
    value.nodeId &&
    !resultOptions.some((option) => option.value === value.nodeId)
  )
    resultOptions.push({ value: value.nodeId, label: `${value.nodeId} · узел недоступен` });
  return (
    <Stack gap="xs">
      <NativeSelect
        label={sourceLabel ?? `Источник ${label}`}
        value={value.source}
        data={[
          { value: "literal", label: "Значение JSON" },
          { value: "path", label: "Параметр пути" },
          { value: "query", label: "Query-параметр" },
          { value: "header", label: "Заголовок запроса" },
          { value: "body", label: "JSON тела запроса" },
          { value: "result", label: "Результат узла сущности" },
        ]}
        onChange={(event) => {
          const source = event.currentTarget.value as ResponseRuleValueRef["source"];
          if (source === "literal") onChange({ source, valueJSON: "null" });
          else if (source === "result")
            onChange({ source, nodeId: producers[0]?.id ?? "", pointer: "" });
          else if (source === "body") onChange({ source, pointer: "" });
          else onChange({ source, name: "" });
        }}
      />
      {value.source === "literal" && (
        <Textarea
          label={`JSON: ${label}`}
          autosize
          minRows={2}
          maxRows={8}
          value={value.valueJSON ?? ""}
          onChange={(event) => onChange({ ...value, valueJSON: event.currentTarget.value })}
        />
      )}
      {(value.source === "path" || value.source === "query" || value.source === "header") && (
        <TextInput
          label={`Имя: ${label}`}
          value={value.name ?? ""}
          onChange={(event) => onChange({ ...value, name: event.currentTarget.value })}
        />
      )}
      {value.source === "result" && (
        <NativeSelect
          label={`Узел: ${label}`}
          value={value.nodeId ?? ""}
          data={resultOptions}
          onChange={(event) => onChange({ ...value, nodeId: event.currentTarget.value })}
        />
      )}
      {(value.source === "body" || value.source === "result") && (
        <>
          <TextInput
            label={`JSON Pointer: ${label}`}
            placeholder="/items/0/id"
            value={value.pointer ?? ""}
            onChange={(event) => onChange({ ...value, pointer: event.currentTarget.value })}
          />
          <Text size="xs" c="dimmed">
            Пустой путь выбирает весь JSON. Результат узла должен быть доступен на каждом пути к
            этому блоку.
          </Text>
        </>
      )}
    </Stack>
  );
}
