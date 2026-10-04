import { createContext, useContext, useState } from "react";
import {
  Button,
  Checkbox,
  Group,
  NativeSelect,
  Stack,
  Text,
  TextInput,
  Textarea,
} from "@mantine/core";
import {
  changeEditableSchema,
  completeChangeObject,
  changeSchemaForProfile,
  changeSchemaDefault,
  changeVariantIndex,
  isChangeObject,
  resolveChangeSchema,
} from "./backendChangeFormModel";
import type { ChangeJSON, ChangeSchema } from "./backendChangeSchemaTypes";

export const ChangeSchemaProfileContext = createContext("6");

const labels: Record<string, string> = {
  accessMode: "Режим доступа",
  address: "Адрес",
  bodyStatus: "Состояние тела",
  boundaryStatus: "Границы транзакции",
  changes: "Изменения миграции",
  channelId: "UUID канала",
  columnScope: "Область колонок",
  columnsStatus: "Состояние колонок",
  condition: "Условие",
  connectionScope: "Область соединения",
  constraintsStatus: "Состояние ограничений",
  contractId: "ID контракта",
  databaseName: "Имя базы данных",
  databaseRoutine: "Процедура базы данных",
  datastoreId: "UUID хранилища",
  definitionReason: "Причина неизвестного определения",
  delay: "Задержка",
  deliveryEdgeId: "UUID связи доставки",
  deliveryReason: "Причина состояния доставки",
  deliveryStatus: "Состояние доставки",
  dependenciesStatus: "Состояние зависимостей",
  dependencyIds: "UUID зависимостей",
  derivationStatus: "Состояние происхождения",
  diagramId: "ID диаграммы",
  dispatchReason: "Причина состояния вызова",
  dispatchStatus: "Состояние вызова",
  duration: "Длительность",
  embeddedContractId: "ID вложенного контракта",
  emitsEdgeId: "UUID связи отправки",
  endpointId: "UUID конечной точки",
  exitStatus: "Состояние выходов",
  expectedKind: "Ожидаемый вид",
  fieldInventory: "Инвентаризация полей",
  generatedExpression: "Вычисляемое выражение",
  identity: "Идентификация значения",
  isolationLevel: "Уровень изоляции",
  items: "Элементы",
  materialized: "Материализовано",
  maxAttempts: "Максимум попыток",
  mediaType: "Медиатип",
  messageId: "UUID сообщения",
  nativeReason: "Причина отсутствия исходного текста",
  nulls: "Порядок null",
  objectId: "ID объекта артефакта",
  objectKey: "Ключ объекта",
  operation: "Операция",
  operationId: "ID операции",
  order: "Порядок миграции",
  ordinal: "Позиция",
  outcome: "Исход",
  parentIds: "UUID родителей",
  participantId: "ID участника",
  portKey: "Ключ порта",
  protocol: "Протокол",
  relational: "Реляционные свойства",
  responseStatus: "Статус ответа",
  routeId: "ID маршрута",
  routineKind: "Вид процедуры",
  ruleId: "ID правила",
  scope: "Область",
  scopeReason: "Причина неизвестной области",
  searchScope: "Область поиска",
  section: "Раздел",
  sourceNodeId: "UUID исходного узла",
  sourceNodeIds: "UUID исходных узлов",
  targetReason: "Причина неизвестной цели",
  technology: "Технология",
  timezone: "Часовой пояс",
  transactionId: "UUID транзакции",
  transitionId: "ID перехода",
  trigger: "Триггер",
  reason: "Причина изменения",
  id: "UUID объекта",
  kind: "Вид",
  name: "Название",
  parentId: "Родитель",
  attributes: "Полные атрибуты",
  recordType: "Объект",
  from: "Начальный узел",
  to: "Конечный узел",
  update: "Изменение узла",
  group: "Группа свойств",
  columnId: "UUID колонки",
  facetKey: "Фасета",
  change: "Свойства колонки",
  action: "Действие",
  constraintId: "UUID ограничения",
  indexId: "UUID индекса",
  tableId: "UUID таблицы",
  definition: "Полное определение",
  stepId: "UUID шага",
  edgeId: "UUID связи",
  mappingId: "UUID отображения",
  sources: "Источники значений",
  destination: "Назначение значения",
  transform: "Преобразование",
  analysisStatus: "Состояние анализа",
  gaps: "Пробелы анализа",
  transport: "Транспорт",
  description: "Описание",
  artifact: "Артефакт",
  revisionId: "Точная ревизия",
  apiBindings: "Привязки API",
  editorBindings: "Привязки редактора",
  target: "Цель",
  expectedExternalKey: "Текущий желаемый ключ",
  newExternalKey: "Новый желаемый ключ",
  criteria: "Критерии",
  key: "Ключ критерия",
  required: "Обязательный критерий",
  objectKind: "Вид объекта",
  edgeKind: "Вид связи",
  selector: "Селектор свойства",
  expected: "Ожидаемое значение",
  expectedHash: "Ожидаемый хеш",
  targetIds: "UUID проверяемых объектов",
  attachment: "Приложенный тест",
  present: "Свойство присутствует",
  value: "Значение",
  status: "Состояние",
  nativeType: "Нативный тип",
  typeFamily: "Семейство типа",
  nullable: "Допускает null",
  defaultExpression: "Значение по умолчанию",
  constraintKind: "Тип ограничения",
  columnIds: "Колонки по порядку",
  reference: "Внешний ключ",
  targetTableId: "Целевая таблица",
  columnPairs: "Пары колонок",
  fromColumnId: "Исходная колонка",
  toColumnId: "Целевая колонка",
  updateAction: "При обновлении",
  deleteAction: "При удалении",
  matchType: "Совпадение",
  terms: "Термы индекса",
  unique: "Уникальность",
  predicate: "Предикат",
  method: "Метод",
  qualifiedName: "Полное имя",
  inputs: "Входные порты",
  outputs: "Выходные порты",
  parameters: "Параметры",
  results: "Результаты",
  facets: "Фасеты",
  language: "Язык",
  expression: "Выражение",
  property: "Свойство пути",
  collection: "Коллекция",
  repositoryId: "Репозиторий",
  snapshotId: "Снимок",
  file: "Файл",
  contentHash: "Хеш содержимого",
  startLine: "Начальная строка",
  endLine: "Конечная строка",
  symbol: "Символ",
  jsonPointer: "JSON Pointer",
  nodeId: "UUID узла",
  portId: "ID порта",
  source: "Исходное свойство",
  dialect: "Диалект",
  nativeDefinition: "Нативное определение",
  deferrable: "Откладываемое",
  initiallyDeferred: "Отложено изначально",
  entryStepId: "Начальный шаг",
  exitStepIds: "Конечные шаги",
  stepKind: "Вид шага",
  transactionContext: "Контекст транзакции",
  nativeText: "Исходный текст",
  redacted: "Секреты удалены",
  providerNamespace: "Provider namespace",
  externalKey: "Исходный ключ",
  assertionHash: "Хеш assertion",
  cardinality: "Кратность",
  path: "Путь",
  direction: "Направление",
  location: "Расположение",
  type: "Тип",
};
export function changeFieldLabel(key: string): string {
  return labels[key] ?? key;
}
const choices: Record<string, string> = {
  node: "Узел",
  edge: "Связь",
  known: "Известно",
  unknown: "Неизвестно",
  create: "Создать",
  update: "Изменить",
  remove: "Удалить",
  parent: "Родитель",
  attributes: "Атрибуты",
  nullable: "Допускает null",
  native_type: "Нативный тип",
  default: "По умолчанию",
  source: "Исходный объект",
  source_identity: "Текущий ключ provider",
  carried_source_identity: "Исторический ключ provider",
  oldSourceId: "UUID старого объекта",
  newSourceId: "UUID нового объекта",
  intent_identity: "Ключ созданного объекта",
  complete: "Полный",
  partial: "Частичный",
  unsupported: "Не поддерживается",
  object_exists: "Объект существует",
  object_absent: "Объект отсутствует",
  edge_exists: "Связь существует",
  field_equals: "Значение свойства",
  artifact_object_matches: "Объект артефакта",
  test_attachment: "Приложенный тест",
  runtime_check: "Проверка выполнения",
};
export function changeChoiceLabel(value: ChangeJSON): string {
  const text = String(value);
  return choices[text] ? `${choices[text]} (${text})` : text;
}
function variantTitle(input: ChangeSchema, index: number): string {
  const schema = resolveChangeSchema(input);
  const tags = Object.entries(schema.properties ?? {})
    .filter(([name, value]) => name !== "type" && "const" in value)
    .map(([name, value]) => `${changeFieldLabel(name)}: ${changeChoiceLabel(value.const!)}`);
  if (tags.length) return tags.join(" · ");
  if (input.$ref)
    return input.$ref
      .split("/")
      .at(-1)!
      .replace(/^Backend(?:Desired)?/, "");
  if (schema.type === "null") return "Явный null";
  return `${schema.type === "string" ? "Текст" : schema.type === "boolean" ? "Да / нет" : schema.type === "array" ? "Упорядоченный список" : (schema.type ?? "Значение")} · ${index + 1}`;
}

type Props = {
  schema: ChangeSchema;
  value: ChangeJSON;
  onChange: (value: ChangeJSON) => void;
  label: string;
  depth?: number;
  hide?: string[];
};
export function BackendChangeSchemaField({
  schema: input,
  value,
  onChange,
  label,
  depth = 0,
  hide = [],
}: Props) {
  const profile = useContext(ChangeSchemaProfileContext);
  const schema = changeSchemaForProfile(input, profile);
  const alternatives = schema.oneOf ?? schema.anyOf;
  if (depth > 24) return <Text c="red">Слишком глубокое вложение: {label}</Text>;
  if (alternatives) {
    const selected = changeVariantIndex(alternatives, value);
    return (
      <Stack gap="xs">
        <NativeSelect
          label={`${label} — вариант`}
          value={String(selected)}
          data={alternatives.map((option, index) => ({
            value: String(index),
            label: variantTitle(option, index),
          }))}
          onChange={(event) =>
            onChange(changeSchemaDefault(alternatives[Number(event.currentTarget.value)]!))
          }
        />
        <BackendChangeSchemaField
          schema={alternatives[selected]!}
          value={value}
          onChange={onChange}
          label={label}
          depth={depth + 1}
          hide={hide}
        />
      </Stack>
    );
  }
  if ("const" in schema)
    return (
      <Text size="sm">
        {label}: {changeChoiceLabel(schema.const!)}
      </Text>
    );
  if (schema.enum)
    return (
      <NativeSelect
        label={label}
        value={JSON.stringify(value)}
        data={schema.enum.map((option) => ({
          value: JSON.stringify(option),
          label: changeChoiceLabel(option),
        }))}
        onChange={(event) => onChange(JSON.parse(event.currentTarget.value) as ChangeJSON)}
      />
    );
  if (Array.isArray(schema.type)) {
    const nullable = schema.type.includes("null");
    const concrete = schema.type.find((type) => type !== "null") ?? "string";
    return (
      <Stack gap="xs">
        {nullable && (
          <Checkbox
            label={`${label} — явный null`}
            checked={value === null}
            onChange={(event) =>
              onChange(
                event.currentTarget.checked
                  ? null
                  : changeSchemaDefault({ ...schema, type: concrete }),
              )
            }
          />
        )}
        {value !== null && (
          <BackendChangeSchemaField
            schema={{ ...schema, type: concrete }}
            value={value}
            onChange={onChange}
            label={label}
            depth={depth + 1}
          />
        )}
      </Stack>
    );
  }
  if (schema.type === "null") return <Text size="sm">{label}: null</Text>;
  if (schema.type === "object" || schema.properties)
    return (
      <ChangeObjectFields
        schema={schema}
        value={value}
        onChange={onChange}
        label={label}
        depth={depth}
        hide={hide}
      />
    );
  if (schema.type === "array") {
    const items = Array.isArray(value) ? value : [];
    const itemSchema = schema.items ?? { type: "string" };
    return (
      <fieldset
        style={{
          border: "1px solid var(--mantine-color-default-border)",
          borderRadius: 4,
          padding: 12,
        }}
      >
        <legend>{label}</legend>
        <Stack gap="sm">
          {items.map((item, index) => (
            <Stack key={index} gap="xs">
              <BackendChangeSchemaField
                schema={itemSchema}
                value={item}
                onChange={(next) =>
                  onChange(items.map((existing, at) => (at === index ? next : existing)))
                }
                label={`${label} ${index + 1}`}
                depth={depth + 1}
              />
              <Group>
                <Button
                  size="compact-sm"
                  variant="subtle"
                  disabled={index === 0}
                  onClick={() => {
                    const next = [...items];
                    [next[index - 1], next[index]] = [next[index]!, next[index - 1]!];
                    onChange(next);
                  }}
                  aria-label={`${label} ${index + 1} вверх`}
                >
                  ↑
                </Button>
                <Button
                  size="compact-sm"
                  variant="subtle"
                  disabled={index === items.length - 1}
                  onClick={() => {
                    const next = [...items];
                    [next[index], next[index + 1]] = [next[index + 1]!, next[index]!];
                    onChange(next);
                  }}
                  aria-label={`${label} ${index + 1} вниз`}
                >
                  ↓
                </Button>
                <Button
                  size="compact-sm"
                  variant="subtle"
                  color="red"
                  onClick={() => onChange(items.filter((_, at) => at !== index))}
                >
                  Удалить {label.toLowerCase()} {index + 1}
                </Button>
              </Group>
            </Stack>
          ))}
          <Button
            variant="light"
            size="xs"
            disabled={items.length >= Math.min(schema.maxItems ?? 100, 100)}
            onClick={() => onChange([...items, changeSchemaDefault(itemSchema)])}
          >
            Добавить: {label.toLowerCase()}
          </Button>
        </Stack>
      </fieldset>
    );
  }
  if (schema.type === "boolean")
    return (
      <Checkbox
        label={label}
        checked={value === true}
        onChange={(event) => onChange(event.currentTarget.checked)}
      />
    );
  if (schema.type === "integer" || schema.type === "number")
    return (
      <TextInput
        label={label}
        inputMode={schema.type === "integer" ? "numeric" : "decimal"}
        value={typeof value === "string" || typeof value === "number" ? value : ""}
        onChange={(event) => {
          const text = event.currentTarget.value;
          const number = Number(text);
          onChange(
            text.trim() &&
              Number.isFinite(number) &&
              (schema.type !== "integer" || Number.isSafeInteger(number))
              ? number
              : text,
          );
        }}
      />
    );
  const text = typeof value === "string" ? value : "";
  if (
    label.includes("Описание") ||
    label.includes("Причина") ||
    (schema.maxLength && schema.maxLength > 4096)
  )
    return (
      <Textarea
        label={label}
        value={text}
        autosize
        minRows={2}
        maxRows={8}
        onChange={(event) => onChange(event.currentTarget.value)}
      />
    );
  return (
    <TextInput
      label={label}
      value={text}
      onChange={(event) => onChange(event.currentTarget.value)}
    />
  );
}

function ChangeObjectFields({ schema: input, value, onChange, label, depth, hide = [] }: Props) {
  const [newKey, setNewKey] = useState("");
  const object = isChangeObject(value) ? value : {};
  const schema = changeEditableSchema(input, object);
  const properties = schema.properties ?? {};
  const additional =
    typeof schema.additionalProperties === "object" ? schema.additionalProperties : null;
  function put(key: string, next: ChangeJSON) {
    onChange(completeChangeObject(input, { ...object, [key]: next }));
  }
  function remove(key: string) {
    const next = { ...object };
    delete next[key];
    onChange(next);
  }
  return (
    <Stack
      gap="sm"
      pl={depth ? "sm" : 0}
      style={depth ? { borderLeft: "2px solid var(--mantine-color-default-border)" } : undefined}
    >
      {Object.entries(properties)
        .filter(([key]) => !hide.includes(key))
        .map(([key, child]) => {
          const required = schema.required?.includes(key);
          const present = Object.hasOwn(object, key);
          const childLabel = changeFieldLabel(key);
          return (
            <Stack key={key} gap="xs">
              {!required && (
                <Checkbox
                  label={`Указать: ${childLabel.toLowerCase()}`}
                  checked={present}
                  onChange={(event) =>
                    event.currentTarget.checked ? put(key, changeSchemaDefault(child)) : remove(key)
                  }
                />
              )}
              {(required || present) && (
                <BackendChangeSchemaField
                  schema={child}
                  value={object[key] ?? null}
                  onChange={(next) => put(key, next)}
                  label={childLabel}
                  depth={(depth ?? 0) + 1}
                />
              )}
            </Stack>
          );
        })}
      {additional && (
        <>
          <Text size="sm" fw={600}>
            {label}: именованные значения
          </Text>
          {Object.entries(object)
            .filter(([key]) => !Object.hasOwn(properties, key))
            .map(([key, item]) => (
              <Stack key={key} gap="xs">
                <BackendChangeSchemaField
                  schema={additional}
                  value={item}
                  onChange={(next) => put(key, next)}
                  label={`${label} · ${key}`}
                  depth={(depth ?? 0) + 1}
                />
                <Button variant="subtle" color="red" onClick={() => remove(key)}>
                  Удалить значение {key}
                </Button>
              </Stack>
            ))}
          <Group align="end">
            <TextInput
              label={`Ключ: ${label.toLowerCase()}`}
              value={newKey}
              onChange={(event) => setNewKey(event.currentTarget.value)}
            />
            <Button
              disabled={!newKey || Object.hasOwn(object, newKey)}
              onClick={() => {
                put(newKey, changeSchemaDefault(additional));
                setNewKey("");
              }}
            >
              Добавить значение
            </Button>
          </Group>
        </>
      )}
    </Stack>
  );
}
