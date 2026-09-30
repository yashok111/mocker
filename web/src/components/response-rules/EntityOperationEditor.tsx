import { useId } from "react";
import { Button, Group, NativeSelect, Stack, Text, TextInput } from "@mantine/core";
import type { ResponseRuleEntityOperation } from "@/api/generated/schemas";
import { isRecord, type ApiDocument } from "../api-designer/documentModel";
import type { ResponseRule, ResponseRuleNode } from "./model";
import ValueRefEditor from "./ValueRefEditor";

type EntityNode = Extract<
  ResponseRuleNode,
  { type: "entity_read" | "entity_create" | "entity_update" }
>;

export default function EntityOperationEditor({
  node,
  rule,
  document,
  onChange,
}: {
  node: EntityNode;
  rule: ResponseRule;
  document: ApiDocument;
  onChange: (node: EntityNode) => void;
}) {
  const { entity, type } = node;
  function editEntity(next: ResponseRuleEntityOperation) {
    const base =
      next.scope === undefined
        ? { family: next.family }
        : { family: next.family, scope: next.scope };
    switch (node.type) {
      case "entity_read":
        onChange({
          ...node,
          entity:
            next.operation === "list"
              ? { ...base, operation: "list" }
              : {
                  ...base,
                  operation: "get",
                  key: next.key ?? { source: "path", name: "id" },
                },
        });
        break;
      case "entity_create":
        onChange({
          ...node,
          entity: { ...base, data: next.data ?? node.entity.data },
        });
        break;
      case "entity_update":
        onChange({
          ...node,
          entity: {
            ...base,
            key: next.key ?? node.entity.key,
            data: next.data ?? node.entity.data,
          },
        });
        break;
    }
  }
  const suggestionsId = useId();
  const families = new Set(
    rule.nodes.flatMap((node) =>
      (node.type === "entity_read" ||
        node.type === "entity_create" ||
        node.type === "entity_update") &&
      node.entity.family
        ? [node.entity.family]
        : [],
    ),
  );
  if (isRecord(document.paths))
    for (const path of Object.keys(document.paths))
      families.add(path.replace(/\/\{[^}]+\}$/, "").replace(/\{[^}]+\}/g, "{}"));
  return (
    <Stack gap="sm">
      <TextInput
        label="Семейство сущностей"
        placeholder="/orders"
        list={suggestionsId}
        value={entity.family}
        onChange={(event) => editEntity({ ...entity, family: event.currentTarget.value })}
      />
      <datalist id={suggestionsId}>
        {[...families].sort().map((family) => (
          <option key={family} value={family}>
            {family}
          </option>
        ))}
      </datalist>
      <Text size="xs" c="dimmed">
        Семейство ресурсов API, например /orders или /orders/{"{}"}/items. Хранилище подготовится
        при применении правила.
      </Text>
      {type === "entity_read" && (
        <NativeSelect
          label="Чтение сущностей"
          value={entity.operation ?? "get"}
          data={[
            { value: "get", label: "Одна сущность по ключу" },
            { value: "list", label: "Список сущностей" },
          ]}
          onChange={(event) => {
            const operation = event.currentTarget.value as "get" | "list";
            const { key: _, ...base } = entity;
            editEntity(
              operation === "list"
                ? { ...base, operation }
                : { ...base, operation, key: { source: "path", name: "id" } },
            );
          }}
        />
      )}
      <NativeSelect
        label="Область сущности"
        value={entity.scope === undefined ? "inherited" : "explicit"}
        data={[
          { value: "inherited", label: "Из пути текущего запроса" },
          { value: "explicit", label: "Задать явно" },
        ]}
        onChange={(event) => {
          if (event.currentTarget.value === "explicit") editEntity({ ...entity, scope: [] });
          else {
            const { scope: _, ...base } = entity;
            editEntity(base);
          }
        }}
      />
      {entity.scope !== undefined && (
        <>
          <Text size="xs" c="dimmed">
            Пустой список выбирает корневую область. Вложенное семейство требует значения каждого
            родителя по порядку.
          </Text>
          {entity.scope.map((value, index) => (
            <Stack key={index} gap="xs">
              <ValueRefEditor
                value={value}
                label={`области ${index + 1}`}
                rule={rule}
                onChange={(next) =>
                  editEntity({
                    ...entity,
                    scope: entity.scope!.map((item, i) => (i === index ? next : item)),
                  })
                }
              />
              <Group>
                <Button
                  size="xs"
                  variant="subtle"
                  color="red"
                  onClick={() =>
                    editEntity({ ...entity, scope: entity.scope!.filter((_, i) => i !== index) })
                  }
                >
                  Удалить область {index + 1}
                </Button>
              </Group>
            </Stack>
          ))}
          <Group>
            <Button
              size="xs"
              variant="default"
              disabled={entity.scope.length >= 3}
              onClick={() =>
                editEntity({
                  ...entity,
                  scope: [...entity.scope!, { source: "literal", valueJSON: '""' }],
                })
              }
            >
              Добавить значение области
            </Button>
          </Group>
        </>
      )}
      {(type === "entity_update" || (type === "entity_read" && entity.operation === "get")) && (
        <ValueRefEditor
          value={entity.key ?? { source: "path", name: "id" }}
          label="ключ сущности"
          sourceLabel="Источник ключа сущности"
          rule={rule}
          onChange={(key) => editEntity({ ...entity, key })}
        />
      )}
      {(type === "entity_create" || type === "entity_update") && (
        <>
          <ValueRefEditor
            value={entity.data ?? { source: "body", pointer: "" }}
            label="данные сущности"
            sourceLabel="Источник данных сущности"
            rule={rule}
            onChange={(data) => editEntity({ ...entity, data })}
          />
          <Text size="xs" c="dimmed">
            {type === "entity_create"
              ? "Данные должны быть объектом JSON. ID назначается при создании."
              : "Объект JSON изменяет поля верхнего уровня существующей сущности."}
          </Text>
        </>
      )}
    </Stack>
  );
}
