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
import { listOperations, type ApiDocument } from "../api-designer/documentModel";
import {
  removeState,
  type Selection,
  type StateDiagram,
  type StateDiagramTransition,
} from "./model";

export default function StateInspector({
  diagram,
  document,
  selection,
  onChange,
  onSelect,
}: {
  diagram: StateDiagram;
  document: ApiDocument;
  selection: Selection;
  onChange: (d: StateDiagram) => void;
  onSelect: (s: Selection) => void;
}) {
  const state =
    selection?.kind === "state" ? diagram.states.find((s) => s.id === selection.id) : undefined;
  const transition =
    selection?.kind === "transition"
      ? diagram.transitions.find((t) => t.id === selection.id)
      : undefined;
  const stateOptions = diagram.states.map((s) => ({ value: s.id, label: s.name || s.id }));
  if (state) {
    const update = (patch: Partial<typeof state>) =>
      onChange({
        ...diagram,
        states: diagram.states.map((s) => (s.id === state.id ? { ...s, ...patch } : s)),
      });
    return (
      <Stack gap="sm">
        <Text fw={650}>Состояние</Text>
        <TextInput
          label="Название состояния"
          value={state.name}
          maxLength={80}
          onChange={(e) => update({ name: e.currentTarget.value })}
        />
        <TextInput
          label="Значение состояния в данных"
          description={`Пустое поле использует ID: ${state.id}`}
          value={state.value ?? ""}
          maxLength={256}
          onChange={(e) => {
            const value = e.currentTarget.value;
            if (new TextEncoder().encode(value).length <= 256)
              update({ value: value || undefined });
          }}
        />
        <Checkbox
          label="Начальное состояние"
          checked={diagram.initialStateId === state.id}
          onChange={(e) =>
            onChange({ ...diagram, initialStateId: e.currentTarget.checked ? state.id : "" })
          }
        />
        <Checkbox
          label="Конечное состояние"
          checked={state.terminal}
          onChange={(e) => update({ terminal: e.currentTarget.checked })}
        />
        {state.terminal && diagram.transitions.some((t) => t.from === state.id) ? (
          <Alert color="yellow">Удалите исходящие переходы из конечного состояния.</Alert>
        ) : null}
        <Group grow>
          <NumberInput
            label="Позиция X"
            value={state.x}
            min={-100000}
            max={100000}
            onChange={(v) => {
              if (typeof v === "number") update({ x: v });
            }}
          />
          <NumberInput
            label="Позиция Y"
            value={state.y}
            min={-100000}
            max={100000}
            onChange={(v) => {
              if (typeof v === "number") update({ y: v });
            }}
          />
        </Group>
        <Button
          variant="light"
          color="red"
          onClick={() => {
            if (window.confirm("Удалить состояние и все связанные переходы?")) {
              onChange(removeState(diagram, state.id));
              onSelect(null);
            }
          }}
        >
          Удалить состояние
        </Button>
      </Stack>
    );
  }
  if (transition) {
    const update = (patch: Partial<StateDiagramTransition>) =>
      onChange({
        ...diagram,
        transitions: diagram.transitions.map((t) =>
          t.id === transition.id ? { ...t, ...patch } : t,
        ),
      });
    const operations = listOperations(document);
    const bindingValue = transition.binding
      ? `${transition.binding.method} ${transition.binding.path}`
      : "";
    const operationOptions = [
      { value: "", label: "Без привязки" },
      ...operations.map((o) => ({
        value: `${o.method} ${o.path}`,
        label: `${o.method.toUpperCase()} ${o.path}`,
      })),
    ];
    if (bindingValue && !operationOptions.some((o) => o.value === bindingValue))
      operationOptions.push({ value: bindingValue, label: `Не найдена: ${bindingValue}` });
    const binding = transition.binding;
    return (
      <Stack gap="sm">
        <Text fw={650}>Переход</Text>
        <TextInput
          label="Название перехода"
          value={transition.name}
          maxLength={80}
          onChange={(e) => update({ name: e.currentTarget.value })}
        />
        <NativeSelect
          label="Из состояния"
          value={transition.from}
          data={
            stateOptions.some((s) => s.value === transition.from)
              ? stateOptions
              : [{ value: transition.from, label: "Состояние удалено" }, ...stateOptions]
          }
          onChange={(e) => update({ from: e.currentTarget.value })}
        />
        <NativeSelect
          label="В состояние"
          value={transition.to}
          data={
            stateOptions.some((s) => s.value === transition.to)
              ? stateOptions
              : [{ value: transition.to, label: "Состояние удалено" }, ...stateOptions]
          }
          onChange={(e) => update({ to: e.currentTarget.value })}
        />
        <NativeSelect
          label="Операция API"
          value={bindingValue}
          data={operationOptions}
          onChange={(e) => {
            const operation = operations.find(
              (o) => `${o.method} ${o.path}` === e.currentTarget.value,
            );
            update({
              binding: operation
                ? {
                    method: operation.method as NonNullable<
                      StateDiagramTransition["binding"]
                    >["method"],
                    path: operation.path,
                  }
                : undefined,
            });
          }}
        />
        {binding ? (
          <Text size="xs" c="dimmed">
            Привязка описывает действие. Прогон вычисляет переход по модели.
          </Text>
        ) : null}
        <NumberInput
          label="Статус ответа в симуляции"
          value={transition.responseStatus}
          min={100}
          max={599}
          allowDecimal={false}
          onChange={(v) => {
            if (typeof v === "number") update({ responseStatus: v });
          }}
        />
        <Checkbox
          label="Условие перехода"
          checked={transition.guard !== undefined}
          onChange={(e) =>
            update({
              guard: e.currentTarget.checked
                ? { pointer: "/paymentAllowed", equalsJSON: "true" }
                : undefined,
            })
          }
        />
        {transition.guard ? (
          <>
            <TextInput
              label="JSON Pointer к полю данных"
              description="Например /balance; пустой путь — весь объект"
              value={transition.guard.pointer}
              onChange={(e) =>
                update({ guard: { ...transition.guard!, pointer: e.currentTarget.value } })
              }
            />
            <Textarea
              label="Ожидаемое значение JSON"
              value={transition.guard.equalsJSON}
              autosize
              minRows={2}
              onChange={(e) =>
                update({ guard: { ...transition.guard!, equalsJSON: e.currentTarget.value } })
              }
            />
          </>
        ) : null}
        <Textarea
          label="Изменение данных JSON"
          description="Поля объекта заменяются после перехода. {} — без изменений."
          value={transition.patchJSON}
          autosize
          minRows={3}
          maxRows={10}
          onChange={(e) => update({ patchJSON: e.currentTarget.value })}
        />
        <Button
          color="red"
          variant="light"
          onClick={() => {
            onChange({
              ...diagram,
              transitions: diagram.transitions.filter((t) => t.id !== transition.id),
            });
            onSelect(null);
          }}
        >
          Удалить переход
        </Button>
      </Stack>
    );
  }
  return (
    <Stack gap="sm">
      <Text fw={650}>Диаграмма</Text>
      <TextInput
        label="Название диаграммы"
        value={diagram.name}
        maxLength={80}
        onChange={(e) => onChange({ ...diagram, name: e.currentTarget.value })}
      />
      <NativeSelect
        label="Начальное состояние"
        value={diagram.initialStateId}
        data={[{ value: "", label: "Выберите состояние" }, ...stateOptions]}
        onChange={(e) => onChange({ ...diagram, initialStateId: e.currentTarget.value })}
      />
      <Checkbox
        label="Исполнять переходы для сущности"
        checked={diagram.entity !== undefined}
        onChange={(e) =>
          onChange({
            ...diagram,
            entity: e.currentTarget.checked
              ? { family: "", keyParam: "", stateField: "" }
              : undefined,
          })
        }
      />
      {diagram.entity && (
        <>
          <TextInput
            label="Семейство сущностей"
            description="Канонический путь, например /orders или /teams/{}/orders"
            value={diagram.entity.family}
            maxLength={2048}
            onChange={(e) =>
              onChange({
                ...diagram,
                entity: { ...diagram.entity!, family: e.currentTarget.value },
              })
            }
          />
          <TextInput
            label="Параметр ключа сущности"
            description="Параметр пути сразу после семейства, например orderId"
            value={diagram.entity.keyParam}
            maxLength={256}
            onChange={(e) =>
              onChange({
                ...diagram,
                entity: { ...diagram.entity!, keyParam: e.currentTarget.value },
              })
            }
          />
          <TextInput
            label="Поле состояния"
            description="Поле верхнего уровня в JSON сущности, например status"
            value={diagram.entity.stateField}
            maxLength={256}
            onChange={(e) =>
              onChange({
                ...diagram,
                entity: { ...diagram.entity!, stateField: e.currentTarget.value },
              })
            }
          />
          <Text size="xs" c="dimmed">
            Отсутствующее поле состояния означает начальное состояние. null, неверный тип и
            неизвестное значение приводят к конфликту. После сохранения примените диаграмму к моку.
          </Text>
        </>
      )}
      <Text size="sm" c="dimmed">
        Выберите состояние или стрелку, чтобы настроить переходы и операции API.
      </Text>
    </Stack>
  );
}
