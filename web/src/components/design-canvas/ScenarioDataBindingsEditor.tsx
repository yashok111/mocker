import { Alert, Button, Group, NativeSelect, Stack, Text, TextInput } from "@mantine/core";
import { IconPlus, IconTrash } from "@tabler/icons-react";
import { createCanvasId } from "./canvasId";
import { validateDataBindings } from "./canvasStorage";
import type {
  CanvasDocument,
  DataBinding,
  DataBindingTarget,
  DataFlowAnalysis,
  DataFlowField,
} from "./types";
import styles from "./ScenarioExecutionPanel.module.css";

export function bindingTargetLabel(target: DataBindingTarget): string {
  return `${target.kind}: ${target.kind === "body" ? target.pointer || "Всё тело" : target.name || "…"}`;
}

export function targetKey(target: DataBindingTarget): string {
  return JSON.stringify(
    target.kind === "body"
      ? { kind: target.kind, pointer: target.pointer }
      : {
          kind: target.kind,
          name: target.kind === "header" ? target.name.toLowerCase() : target.name,
        },
  );
}

export function bindingFieldName(target: DataBindingTarget): string {
  return target.kind === "body"
    ? target.pointer
      ? pointerLabel(target.pointer)
      : "Всё тело запроса"
    : target.name || "Новое поле";
}

function pointerLabel(pointer: string): string {
  return pointer
    ? pointer
        .slice(1)
        .split("/")
        .map((part) => part.replace(/~1/g, "/").replace(/~0/g, "~"))
        .join(" → ")
    : "Весь ответ";
}

function fieldLabel(field: DataFlowField): string {
  const name =
    field.kind === "response" || field.kind === "body"
      ? pointerLabel(field.pointer ?? "")
      : field.name;
  const types: Record<string, string> = {
    integer: "число",
    number: "число",
    string: "текст",
    boolean: "да / нет",
    object: "объект",
    array: "список",
    null: "пустое значение",
    unknown: "тип неизвестен",
  };
  return `${name} · ${types[field.type] ?? field.type}`;
}

export type BindingExample = { sourceMessageId: string; sourcePointer: string; valueJson: string };

function targetOf(field: DataFlowField): DataBindingTarget {
  return field.kind === "body"
    ? { kind: "body", pointer: field.pointer ?? "" }
    : { kind: field.kind as "path" | "query" | "header", name: field.name ?? "" };
}

export function ScenarioDataBindingsEditor({
  document,
  messageId,
  bindings,
  analysis,
  onChange,
  target,
  kind,
  examples = [],
}: {
  document: CanvasDocument;
  messageId: string;
  bindings: DataBinding[];
  analysis: DataFlowAnalysis | null;
  onChange: (bindings: DataBinding[]) => void;
  target?: DataBindingTarget;
  kind?: "body";
  examples?: BindingExample[];
}) {
  const messageIndex = document.messages.findIndex((message) => message.id === messageId);
  const sources = document.messages
    .slice(0, messageIndex)
    .filter((message) => message.kind === "request" && message.operation);
  const targets =
    analysis?.messages.find((message) => message.messageId === messageId)?.requestFields ?? [];
  const patch = (index: number, update: Partial<DataBinding>) =>
    onChange(bindings.map((binding, i) => (i === index ? { ...binding, ...update } : binding)));
  const add = (destination: DataBindingTarget) =>
    onChange([
      ...bindings,
      {
        id: createCanvasId(),
        sourceMessageId: "",
        sourcePointer: "",
        target: destination,
      },
    ]);
  const visibleBindings = bindings
    .map((binding, index) => ({ binding, index }))
    .filter(({ binding }) =>
      target
        ? targetKey(binding.target) === targetKey(target)
        : !kind || binding.target.kind === kind,
    );
  const availableTargets = (
    target ? [target] : targets.filter((field) => !kind || field.kind === kind).map(targetOf)
  ).filter(
    (destination) =>
      !bindings.some((binding) => targetKey(binding.target) === targetKey(destination)),
  );
  return (
    <div>
      <Stack gap="sm">
        {visibleBindings.map(({ binding, index }) => {
          const sourceFields =
            analysis?.messages.find((message) => message.messageId === binding.sourceMessageId)
              ?.responseFields ?? [];
          const sourceOptions = sourceFields.map((field) => ({
            value: `pointer:${field.pointer ?? ""}`,
            label: fieldLabel(field),
          }));
          const targetOptions = targets.map((field) => ({
            value: targetKey(targetOf(field)),
            label: fieldLabel(field),
          }));
          const sourceValue = `pointer:${binding.sourcePointer}`;
          const targetValue = targetKey(binding.target);
          const sourceKnown = sourceOptions.some((option) => option.value === sourceValue);
          const targetKnown = targetOptions.some((option) => option.value === targetValue);
          const selectedTarget = targets.find(
            (field) => targetKey(targetOf(field)) === targetValue,
          );
          const example = examples.find(
            (item) =>
              item.sourceMessageId === binding.sourceMessageId &&
              item.sourcePointer === binding.sourcePointer,
          );
          let structureError: string | null = null;
          try {
            validateDataBindings([binding]);
          } catch (cause) {
            structureError = cause instanceof Error ? cause.message : "Проверьте поля связи";
          }
          const diagnostics =
            analysis?.diagnostics.filter(
              (diagnostic) =>
                diagnostic.pointer === `/messages/${messageIndex}/execution/bindings/${index}` ||
                diagnostic.pointer.startsWith(
                  `/messages/${messageIndex}/execution/bindings/${index}/`,
                ),
            ) ?? [];
          return (
            <fieldset key={binding.id} className={styles.binding}>
              <legend>{bindingFieldName(binding.target)}</legend>
              <Group justify="space-between" wrap="wrap" mb="xs">
                <Text size="xs" className={styles.wrap} style={{ flex: 1, minWidth: 0 }}>
                  {binding.sourceMessageId
                    ? `${bindingFieldName(binding.target)} берётся из шага «${document.messages.find((message) => message.id === binding.sourceMessageId)?.label || binding.sourceMessageId}»`
                    : `Выберите, откуда взять ${bindingFieldName(binding.target)}`}
                </Text>
                <Button
                  variant="subtle"
                  color="red"
                  size="compact-sm"
                  leftSection={<IconTrash size={14} />}
                  aria-label={`Удалить связь ${index + 1}`}
                  onClick={() => onChange(bindings.filter((_, i) => i !== index))}
                >
                  Удалить
                </Button>
              </Group>
              <Stack gap="xs">
                <div className={styles.bindingSource}>
                  <NativeSelect
                    label="Из какого шага"
                    aria-label={`Шаг-источник связи ${index + 1}`}
                    value={binding.sourceMessageId}
                    data={[
                      { value: "", label: "Выберите предыдущий HTTP-шаг" },
                      ...sources.map((message) => ({
                        value: message.id,
                        label: `${message.label || message.id}${message.execution?.enabled === false ? " · выключен" : ""}`,
                      })),
                      ...(binding.sourceMessageId &&
                      !sources.some((message) => message.id === binding.sourceMessageId)
                        ? [
                            {
                              value: binding.sourceMessageId,
                              label: `${binding.sourceMessageId} · источник недоступен`,
                            },
                          ]
                        : []),
                    ]}
                    onChange={(event) =>
                      patch(index, { sourceMessageId: event.currentTarget.value })
                    }
                  />
                  <NativeSelect
                    label="Какое поле ответа"
                    aria-label={`Поле ответа связи ${index + 1}`}
                    disabled={!binding.sourceMessageId}
                    value={sourceKnown ? sourceValue : "manual"}
                    data={[
                      {
                        value: "manual",
                        label: binding.sourcePointer
                          ? `${pointerLabel(binding.sourcePointer)} · вручную`
                          : binding.sourceMessageId
                            ? "Весь ответ · вручную"
                            : "Сначала выберите шаг",
                      },
                      ...sourceOptions,
                    ]}
                    onChange={(event) => {
                      if (event.currentTarget.value !== "manual")
                        patch(index, { sourcePointer: event.currentTarget.value.slice(8) });
                    }}
                  />
                </div>
                {example ? (
                  <Text size="xs" c="dimmed" className={styles.wrap}>
                    В прошлом запуске:{" "}
                    {example.valueJson.length > 160
                      ? `${example.valueJson.slice(0, 160)}…`
                      : example.valueJson}
                  </Text>
                ) : null}
                {binding.prefix ? (
                  <Text size="xs" c="dimmed" className={styles.wrap}>
                    Перед значением добавится «{binding.prefix}»
                  </Text>
                ) : null}
                <details className={styles.disclosure}>
                  <summary>Дополнительно</summary>
                  <Stack gap="xs" mt="xs">
                    <TextInput
                      data-source-pointer
                      label={`JSON Pointer источника связи ${index + 1}`}
                      description="Пустое поле — весь ответ. Для массива: /items/0/id; экранирование: ~0 и ~1."
                      value={binding.sourcePointer}
                      maxLength={2000}
                      onChange={(event) =>
                        patch(index, { sourcePointer: event.currentTarget.value })
                      }
                    />
                    <NativeSelect
                      label={`Назначение связи ${index + 1}`}
                      value={targetKnown ? targetValue : "manual"}
                      data={[
                        { value: "manual", label: "Указать назначение вручную" },
                        ...targetOptions,
                      ]}
                      onChange={(event) => {
                        if (event.currentTarget.value === "manual")
                          event.currentTarget
                            .closest("fieldset")
                            ?.querySelector<HTMLInputElement>("[data-target-input]")
                            ?.focus();
                        const field = targets.find(
                          (field) => targetKey(targetOf(field)) === event.currentTarget.value,
                        );
                        if (field)
                          patch(index, {
                            target: targetOf(field),
                            ...(field.kind === "body" ||
                            (field.type !== "string" && field.type !== "unknown")
                              ? { prefix: undefined }
                              : {}),
                          });
                      }}
                    />
                    <div className={styles.bindingTarget}>
                      <NativeSelect
                        label={`Тип назначения связи ${index + 1}`}
                        value={binding.target.kind}
                        data={[
                          { value: "path", label: "Path" },
                          { value: "query", label: "Query" },
                          { value: "header", label: "Header" },
                          { value: "body", label: "JSON body" },
                        ]}
                        onChange={(event) => {
                          const kind = event.currentTarget.value as DataBindingTarget["kind"];
                          patch(index, {
                            target: kind === "body" ? { kind, pointer: "" } : { kind, name: "" },
                            ...(kind === "body" ? { prefix: undefined } : {}),
                          });
                        }}
                      />
                      {binding.target.kind === "body" ? (
                        <TextInput
                          data-target-input
                          label={`JSON Pointer назначения связи ${index + 1}`}
                          description="Пустое поле заменяет всё тело."
                          value={binding.target.pointer}
                          maxLength={2000}
                          onChange={(event) =>
                            patch(index, {
                              target: { kind: "body", pointer: event.currentTarget.value },
                            })
                          }
                        />
                      ) : (
                        <TextInput
                          data-target-input
                          label={`Имя назначения связи ${index + 1}`}
                          value={binding.target.name}
                          maxLength={256}
                          onChange={(event) =>
                            patch(index, {
                              target: {
                                kind: binding.target.kind as "path" | "query" | "header",
                                name: event.currentTarget.value,
                              },
                            })
                          }
                        />
                      )}
                    </div>
                    <TextInput
                      label={`Префикс связи ${index + 1}`}
                      description="Буквальный текст перед строкой, например Bearer с пробелом."
                      value={binding.prefix ?? ""}
                      disabled={
                        binding.target.kind === "body" ||
                        (selectedTarget !== undefined &&
                          !["string", "unknown"].includes(selectedTarget.type))
                      }
                      onChange={(event) =>
                        patch(index, { prefix: event.currentTarget.value || undefined })
                      }
                    />
                  </Stack>
                </details>
                {structureError ? (
                  <Alert color="red" role="alert">
                    {!binding.sourceMessageId
                      ? "Выберите шаг, из ответа которого взять значение."
                      : structureError}
                  </Alert>
                ) : null}
                {diagnostics.map((diagnostic, i) => (
                  <Alert key={i} color={diagnostic.severity === "error" ? "red" : "yellow"}>
                    {diagnostic.message}
                  </Alert>
                ))}
              </Stack>
            </fieldset>
          );
        })}
      </Stack>
      {kind === "body" && availableTargets.length ? (
        <NativeSelect
          label="Какое поле тела заполнить"
          value=""
          mt="xs"
          disabled={bindings.length >= 100 || sources.length === 0}
          data={[
            { value: "", label: "Выберите поле…" },
            ...availableTargets.map((destination) => ({
              value: targetKey(destination),
              label: bindingFieldName(destination),
            })),
          ]}
          onChange={(event) => {
            const destination = availableTargets.find(
              (item) => targetKey(item) === event.currentTarget.value,
            );
            if (destination) add(destination);
          }}
        />
      ) : (
        availableTargets.map((destination) => (
          <Button
            key={targetKey(destination)}
            variant="subtle"
            size="xs"
            mt={4}
            aria-label={`Взять из ответа: ${bindingFieldName(destination)}`}
            disabled={bindings.length >= 100 || sources.length === 0}
            onClick={() => add(destination)}
          >
            {target ? "Взять из ответа…" : `${bindingFieldName(destination)} · Взять из ответа…`}
          </Button>
        ))
      )}
      {!target ? (
        <details className={styles.disclosure}>
          <summary>Другое поле запроса</summary>
          <Button
            variant="subtle"
            size="xs"
            leftSection={<IconPlus size={14} />}
            mt="xs"
            disabled={bindings.length >= 100}
            onClick={() =>
              add(kind === "body" ? { kind: "body", pointer: "" } : { kind: "path", name: "" })
            }
          >
            Добавить связь
          </Button>
        </details>
      ) : null}
    </div>
  );
}
