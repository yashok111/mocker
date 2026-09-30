import { useState, useSyncExternalStore } from "react";
import {
  Alert,
  Button,
  Checkbox,
  Group,
  NativeSelect,
  NumberInput,
  Stack,
  Text,
  Textarea,
  TextInput,
} from "@mantine/core";
import type { ResponseRuleCondition } from "@/api/generated/schemas";
import { isRecord, type ApiDocument } from "../api-designer/documentModel";
import type { FormDraftStore } from "../api-designer/forms/formDraftStore";
import {
  checkRule,
  EXTENSION,
  nodeNames,
  ports,
  portNames,
  type ResponseRule,
  type ResponseRuleNode,
  type GraphSelection,
} from "./model";
import { FieldRows } from "./RequestFixtureEditor";
import EntityOperationEditor from "./EntityOperationEditor";
import ValueRefEditor from "./ValueRefEditor";

export const inspectorPointer = (id: string) => `/${EXTENSION}/$form/${id}`;
type Props = {
  rule: ResponseRule;
  document: ApiDocument;
  selection: GraphSelection;
  formStore: FormDraftStore;
  blocked: boolean;
  onChange: (rule: ResponseRule) => boolean;
  onRemove: () => void;
};
export default function RuleInspector({
  rule,
  document,
  selection,
  formStore,
  blocked,
  onChange,
  onRemove,
}: Props) {
  useSyncExternalStore(formStore.subscribe, formStore.getSnapshot, formStore.getSnapshot);
  const pointer = inspectorPointer(rule.id);
  const draft = formStore.get(pointer);
  const [error, setError] = useState<string | null>(null);
  let candidate = rule;
  try {
    if (draft) candidate = JSON.parse(draft.source) as ResponseRule;
  } catch {
    /* Kept visible for cancellation below. */
  }
  const node =
    selection?.kind === "node"
      ? candidate.nodes.find((item) => item.id === selection.id)
      : undefined;
  const edge =
    selection?.kind === "edge"
      ? candidate.edges.find((item) => item.id === selection.id)
      : undefined;
  function edit(next: ResponseRule) {
    let failure: string | undefined;
    try {
      checkRule(next);
    } catch {
      failure = "Проверьте обязательные поля, целые числа и ограничения размера.";
    }
    formStore.set(pointer, {
      source: JSON.stringify(next),
      propertySource: draft?.propertySource ?? JSON.stringify(rule),
      error: failure,
    });
    setError(null);
  }
  function editNode(next: ResponseRuleNode) {
    edit({
      ...candidate,
      nodes: candidate.nodes.map((item) => (item.id === next.id ? next : item)),
    });
  }
  const operationOptions: { value: string; label: string }[] = [
    { value: "", label: "Без привязки" },
  ];
  if (isRecord(document.paths))
    for (const [path, item] of Object.entries(document.paths)) {
      if (!isRecord(item)) continue;
      for (const method of ["get", "post", "put", "patch", "delete", "head", "options", "trace"]) {
        if (!isRecord(item[method])) continue;
        operationOptions.push({
          value: `${method.toUpperCase()} ${path}`,
          label: `${method.toUpperCase()} ${path}${item.$ref !== undefined ? " · $ref не поддерживается" : ""}`,
        });
      }
    }
  const binding = candidate.binding ? `${candidate.binding.method} ${candidate.binding.path}` : "";
  if (binding && !operationOptions.some((option) => option.value === binding))
    operationOptions.push({ value: binding, label: `${binding} · операция недоступна` });
  const sourceNode = edge && candidate.nodes.find((item) => item.id === edge.from);
  return (
    <Stack gap="sm">
      <Text fw={600}>{node ? nodeNames[node.type] : edge ? "Связь" : "Свойства правила"}</Text>
      <Text size="xs" c="dimmed">
        ID: {node?.id ?? edge?.id ?? rule.id}
      </Text>
      <fieldset disabled={blocked} style={{ border: 0, margin: 0, padding: 0, minWidth: 0 }}>
        <Stack gap="sm">
          {!node && !edge && (
            <>
              <TextInput
                label="Название правила"
                value={candidate.name}
                onChange={(event) => edit({ ...candidate, name: event.currentTarget.value })}
              />
              <NativeSelect
                label="Операция правила"
                value={binding}
                data={operationOptions}
                onChange={(event) => {
                  const value = event.currentTarget.value;
                  if (!value) {
                    const { binding: _, ...withoutBinding } = candidate;
                    edit(withoutBinding);
                  } else {
                    const separator = value.indexOf(" ");
                    edit({
                      ...candidate,
                      binding: {
                        method: value.slice(0, separator),
                        path: value.slice(separator + 1),
                      },
                    });
                  }
                }}
              />
              <Text size="xs" c="dimmed">
                Привязка использует точные метод и путь. Path Item с $ref пока не поддерживается.
              </Text>
            </>
          )}
          {node && (
            <>
              <TextInput
                label="Название узла"
                value={node.name}
                onChange={(event) => editNode({ ...node, name: event.currentTarget.value })}
              />
              {node.type === "condition" && (
                <>
                  <NativeSelect
                    label="Источник условия"
                    value={node.condition.in}
                    data={[
                      { value: "query", label: "Query-параметр" },
                      { value: "header", label: "Заголовок" },
                      { value: "body", label: "Ключ JSON тела" },
                    ]}
                    onChange={(event) =>
                      editNode({
                        ...node,
                        condition: {
                          ...node.condition,
                          in: event.currentTarget.value as ResponseRuleCondition["in"],
                        },
                      })
                    }
                  />
                  <TextInput
                    label="Имя поля условия"
                    value={node.condition.name}
                    onChange={(event) =>
                      editNode({
                        ...node,
                        condition: { ...node.condition, name: event.currentTarget.value },
                      })
                    }
                  />
                  <NativeSelect
                    label="Сравнение"
                    value={node.condition.op}
                    data={[
                      { value: "exists", label: "Существует" },
                      { value: "equals", label: "Равно" },
                      { value: "contains", label: "Содержит" },
                    ]}
                    onChange={(event) => {
                      const op = event.currentTarget.value as ResponseRuleCondition["op"];
                      const { value: _, ...base } = node.condition;
                      editNode({
                        ...node,
                        condition:
                          op === "exists"
                            ? { ...base, op }
                            : { ...base, op, value: node.condition.value ?? "" },
                      });
                    }}
                  />
                  {node.condition.op !== "exists" && (
                    <TextInput
                      label="Значение условия"
                      value={node.condition.value ?? ""}
                      onChange={(event) =>
                        editNode({
                          ...node,
                          condition: { ...node.condition, value: event.currentTarget.value },
                        })
                      }
                    />
                  )}
                  {node.condition.in === "body" && (
                    <Text size="xs" c="dimmed">
                      Только ключ верхнего уровня. Числа сравниваются как исходный текст: 1, 1.0 и
                      1e0 различаются; большие целые сохраняются точно.
                    </Text>
                  )}
                </>
              )}
              {node.type === "delay" && (
                <NumberInput
                  label="Задержка, мс"
                  min={0}
                  max={30000}
                  allowDecimal={false}
                  value={node.delayMs ?? ""}
                  onChange={(value) =>
                    editNode({ ...node, delayMs: value === "" ? NaN : Number(value) })
                  }
                />
              )}
              {(node.type === "entity_read" ||
                node.type === "entity_create" ||
                node.type === "entity_update") && (
                <EntityOperationEditor
                  node={node}
                  rule={candidate}
                  document={document}
                  onChange={editNode}
                />
              )}
              {node.type === "response" && (
                <>
                  <NumberInput
                    label="HTTP-статус"
                    min={200}
                    max={599}
                    allowDecimal={false}
                    value={node.response.status ?? ""}
                    onChange={(value) =>
                      editNode({
                        ...node,
                        response: { ...node.response, status: value === "" ? NaN : Number(value) },
                      })
                    }
                  />
                  <TextInput
                    label="Тип содержимого"
                    value={node.response.mediaType}
                    onChange={(event) =>
                      editNode({
                        ...node,
                        response: { ...node.response, mediaType: event.currentTarget.value },
                      })
                    }
                  />
                  <Text size="xs" c="dimmed">
                    application/json или application/*+json. Управляемые заголовки и cookies
                    недоступны.
                  </Text>
                  <FieldRows
                    rows={node.response.headers}
                    label="Заголовок ответа"
                    addLabel="Добавить заголовок ответа"
                    onChange={(headers) =>
                      editNode({ ...node, response: { ...node.response, headers } })
                    }
                  />
                  <Checkbox
                    label="Тело ответа JSON"
                    checked={
                      node.response.bodyJSON !== undefined || node.response.bodyFrom !== undefined
                    }
                    onChange={(event) => {
                      if (event.currentTarget.checked)
                        editNode({ ...node, response: { ...node.response, bodyJSON: "null" } });
                      else {
                        const { bodyJSON: _, bodyFrom: _from, ...response } = node.response;
                        editNode({ ...node, response });
                      }
                    }}
                  />
                  {(node.response.bodyJSON !== undefined ||
                    node.response.bodyFrom !== undefined) && (
                    <NativeSelect
                      label="Источник тела ответа"
                      value={node.response.bodyFrom !== undefined ? "reference" : "literal"}
                      data={[
                        { value: "literal", label: "JSON ответа" },
                        { value: "reference", label: "Значение из запроса или сущности" },
                      ]}
                      onChange={(event) => {
                        const { bodyJSON: _, bodyFrom: _from, ...response } = node.response;
                        editNode({
                          ...node,
                          response:
                            event.currentTarget.value === "reference"
                              ? { ...response, bodyFrom: { source: "body", pointer: "" } }
                              : { ...response, bodyJSON: "null" },
                        });
                      }}
                    />
                  )}
                  {node.response.bodyFrom !== undefined && (
                    <ValueRefEditor
                      value={node.response.bodyFrom}
                      label="значение тела ответа"
                      sourceLabel="Источник значения тела ответа"
                      rule={candidate}
                      onChange={(bodyFrom) =>
                        editNode({ ...node, response: { ...node.response, bodyFrom } })
                      }
                    />
                  )}
                  {node.response.bodyJSON !== undefined && (
                    <Textarea
                      label="JSON ответа"
                      autosize
                      minRows={4}
                      maxRows={12}
                      value={node.response.bodyJSON}
                      onChange={(event) =>
                        editNode({
                          ...node,
                          response: { ...node.response, bodyJSON: event.currentTarget.value },
                        })
                      }
                    />
                  )}
                  <Text size="xs" c="dimmed">
                    Для HEAD и статусов 204, 205, 304 тело должно отсутствовать.
                  </Text>
                </>
              )}
              {node.type === "fallback" && (
                <Text size="sm">
                  Передать стандартной обработке. Конкретный ответ в этой симуляции не
                  рассчитывается.
                </Text>
              )}
            </>
          )}
          {edge && (
            <>
              <NativeSelect
                label="Начало связи"
                value={edge.from}
                data={candidate.nodes.map((item) => ({
                  value: item.id,
                  label: item.name || item.id,
                }))}
                onChange={(event) =>
                  edit({
                    ...candidate,
                    edges: candidate.edges.map((item) =>
                      item.id === edge.id
                        ? {
                            ...item,
                            from: event.currentTarget.value,
                            port:
                              ports(
                                candidate.nodes.find(
                                  (node) => node.id === event.currentTarget.value,
                                )!,
                              )[0] ?? "next",
                          }
                        : item,
                    ),
                  })
                }
              />
              <NativeSelect
                label="Выход связи"
                value={edge.port}
                data={(sourceNode ? ports(sourceNode) : [edge.port]).map((port) => ({
                  value: port,
                  label: portNames[port] ?? port,
                }))}
                onChange={(event) =>
                  edit({
                    ...candidate,
                    edges: candidate.edges.map((item) =>
                      item.id === edge.id
                        ? { ...item, port: event.currentTarget.value as typeof edge.port }
                        : item,
                    ),
                  })
                }
              />
              <NativeSelect
                label="Конец связи"
                value={edge.to}
                data={candidate.nodes.map((item) => ({
                  value: item.id,
                  label: item.name || item.id,
                }))}
                onChange={(event) =>
                  edit({
                    ...candidate,
                    edges: candidate.edges.map((item) =>
                      item.id === edge.id ? { ...item, to: event.currentTarget.value } : item,
                    ),
                  })
                }
              />
            </>
          )}
        </Stack>
      </fieldset>
      {(error || draft?.error) && (
        <Alert color="red" role="alert">
          {error ?? draft?.error}
        </Alert>
      )}
      {draft && (
        <>
          <Text size="xs">Есть неприменённые свойства. Они сохранятся при смене вкладки.</Text>
          <Group gap="xs">
            <Button
              size="xs"
              disabled={blocked || !!draft.error}
              onClick={() => {
                if (draft.propertySource !== JSON.stringify(rule)) {
                  setError("Правило изменилось. Отмените свойства и повторите редактирование.");
                  return;
                }
                try {
                  checkRule(candidate);
                  if (onChange(candidate)) {
                    formStore.remove(pointer);
                    setError(null);
                  }
                } catch (cause) {
                  setError(cause instanceof Error ? cause.message : "Неверные свойства");
                }
              }}
            >
              Применить свойства
            </Button>
            <Button
              size="xs"
              variant="default"
              onClick={() => {
                formStore.remove(pointer);
                setError(null);
              }}
            >
              Отменить свойства
            </Button>
          </Group>
        </>
      )}
      {(node || edge) && (
        <Button
          color="red"
          size="xs"
          variant="subtle"
          disabled={blocked || !!draft}
          onClick={onRemove}
        >
          {node ? "Удалить узел" : "Удалить связь"}
        </Button>
      )}
    </Stack>
  );
}
