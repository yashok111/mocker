import { Button, Group, NativeSelect, Stack, Text, Textarea, TextInput } from "@mantine/core";
import type {
  ResponseRuleResultCondition,
  ResponseRuleResultSource,
} from "@/api/generated/schemas";
import {
  isResultConditionNumberJSON,
  isResultConditionScalarJSON,
  numericResultConditionOps,
  resultConditionOpNames,
  type ResponseRule,
} from "./model";
import {
  conditionChildren,
  conditionDepth,
  conditionLeaves,
  type EditableResultCondition,
  type ResultLeaf,
} from "./resultConditions";
import { resultConditionSources } from "./resultConditionSources";
import styles from "./ResponseRules.module.css";

type Sources = ReturnType<typeof resultConditionSources>;
export default function ResultConditionEditor({
  value,
  rule,
  nodeId,
  onChange,
}: {
  value: ResponseRuleResultCondition;
  rule: ResponseRule;
  nodeId: string;
  onChange: (condition: ResponseRuleResultCondition) => void;
}) {
  const sources = resultConditionSources(rule, nodeId);
  return (
    <Stack gap="xs">
      <TreeEditor
        value={value}
        sources={sources}
        depth={1}
        path=""
        totalLeaves={conditionLeaves(value).length}
        onChange={(next) => onChange(next as ResponseRuleResultCondition)}
      />
      <Text size="xs" c="dimmed">
        До 16 условий и 4 уровней. И / ИЛИ проверяются по порядку: после определённого результата
        оставшиеся условия пропускаются.
      </Text>
    </Stack>
  );
}
function TreeEditor({
  value,
  sources,
  depth,
  path,
  totalLeaves,
  onChange,
}: {
  value: EditableResultCondition;
  sources: Sources;
  depth: number;
  path: string;
  totalLeaves: number;
  onChange: (value: EditableResultCondition) => void;
}) {
  const suffix = path ? ` · ${path}` : "";
  const children = conditionChildren(value);
  const group = "all" in value ? "all" : "any" in value ? "any" : "leaf";
  const createLeaf = (): ResultLeaf => ({
    source: { ...conditionLeaves(value)[0]!.source },
    op: "exists",
  });
  const wrapAllowed = totalLeaves < 16 && depth + conditionDepth(value) <= 4;
  function changeChildren(next: EditableResultCondition[]) {
    onChange(group === "all" ? { all: next } : { any: next });
  }
  return (
    <Stack gap="xs" className={children ? styles.conditionGroup : undefined}>
      <NativeSelect
        label={`Структура условия${suffix}`}
        value={group}
        data={[
          ...(!children ? [{ value: "leaf", label: "Одно условие" }] : []),
          { value: "all", label: "И · все условия", disabled: !children && !wrapAllowed },
          { value: "any", label: "ИЛИ · любое условие", disabled: !children && !wrapAllowed },
        ]}
        onChange={(event) => {
          const next = event.currentTarget.value;
          if (next === "leaf") return;
          if (!children && !wrapAllowed) return;
          const list = children ?? [value, createLeaf()];
          onChange(next === "all" ? { all: list } : { any: list });
        }}
      />
      {children ? (
        <>
          <Text size="xs" c="dimmed">
            {group === "all"
              ? "Каждое условие должно выполниться."
              : "Достаточно одного выполненного условия."}
          </Text>
          {children.map((child, index) => {
            const childPath = path ? `${path}.${index + 1}` : String(index + 1);
            return (
              <Stack key={index} gap="xs" className={styles.conditionChild}>
                <Text size="sm" fw={600}>
                  Условие {childPath}
                </Text>
                <TreeEditor
                  value={child}
                  sources={sources}
                  depth={depth + 1}
                  path={childPath}
                  totalLeaves={totalLeaves}
                  onChange={(next) =>
                    changeChildren(children.map((item, i) => (i === index ? next : item)))
                  }
                />
                <Group gap="xs">
                  <Button
                    size="xs"
                    variant="subtle"
                    color="red"
                    disabled={children.length <= 2}
                    onClick={() => changeChildren(children.filter((_, i) => i !== index))}
                  >
                    Удалить условие {childPath}
                  </Button>
                  <Button size="xs" variant="subtle" onClick={() => onChange(child)}>
                    Оставить только условие {childPath}
                  </Button>
                </Group>
              </Stack>
            );
          })}
          <Group>
            <Button
              size="xs"
              variant="default"
              disabled={totalLeaves >= 16 || children.length >= 16 || depth >= 4}
              onClick={() => changeChildren([...children, createLeaf()])}
            >
              Добавить условие{suffix}
            </Button>
          </Group>
        </>
      ) : (
        <LeafEditor
          value={value as ResultLeaf}
          sources={sources}
          suffix={suffix}
          onChange={onChange}
        />
      )}
    </Stack>
  );
}
function SourceEditor({
  value,
  sources,
  label,
  pointerLabel,
  onChange,
}: {
  value: ResponseRuleResultSource;
  sources: Sources;
  label: string;
  pointerLabel: string;
  onChange: (value: ResponseRuleResultSource) => void;
}) {
  const options = [
    { value: "", label: "Выберите узел" },
    ...sources.producers.map((node) => ({ value: node.id, label: node.name || node.id })),
  ];
  if (value.nodeId && !options.some((option) => option.value === value.nodeId))
    options.push({ value: value.nodeId, label: `${value.nodeId} · результат недоступен` });
  return (
    <>
      <NativeSelect
        label={label}
        value={value.nodeId}
        data={options}
        error={sources.unavailableReason(value.nodeId)}
        onChange={(event) => onChange({ ...value, nodeId: event.currentTarget.value })}
      />
      <TextInput
        label={pointerLabel}
        placeholder="/status"
        value={value.pointer ?? ""}
        onChange={(event) => onChange({ ...value, pointer: event.currentTarget.value })}
      />
    </>
  );
}
function LeafEditor({
  value,
  sources,
  suffix,
  onChange,
}: {
  value: ResultLeaf;
  sources: Sources;
  suffix: string;
  onChange: (value: ResultLeaf) => void;
}) {
  const comparison = value.op !== "exists" && value.op !== "not_exists";
  const numeric = numericResultConditionOps.has(value.op);
  const reference = "valueFrom" in value;
  return (
    <>
      <SourceEditor
        value={value.source}
        sources={sources}
        label={`Узел результата условия${suffix}`}
        pointerLabel={`JSON Pointer условия${suffix}`}
        onChange={(source) => onChange({ ...value, source })}
      />
      <Text size="xs" c="dimmed">
        Пустой путь выбирает весь результат. Вложенные поля: /items/0/id; символы / и ~ записываются
        как ~1 и ~0.
      </Text>
      <NativeSelect
        label={`Сравнение результата${suffix}`}
        value={value.op}
        data={Object.entries(resultConditionOpNames).map(([value, label]) => ({ value, label }))}
        onChange={(event) => {
          const op = event.currentTarget.value as ResultLeaf["op"];
          if (op === "exists" || op === "not_exists") onChange({ source: value.source, op });
          else if ("valueFrom" in value)
            onChange({ source: value.source, op, valueFrom: value.valueFrom });
          else
            onChange({
              source: value.source,
              op,
              valueJSON:
                "valueJSON" in value
                  ? value.valueJSON
                  : numericResultConditionOps.has(op)
                    ? "0"
                    : "null",
            } as ResultLeaf);
        }}
      />
      {comparison && (
        <>
          <NativeSelect
            label={`Источник ожидаемого значения${suffix}`}
            value={reference ? "result" : "literal"}
            data={[
              { value: "literal", label: "Значение JSON" },
              { value: "result", label: "Результат узла сущности" },
            ]}
            onChange={(event) => {
              if (event.currentTarget.value === "result")
                onChange({
                  source: value.source,
                  op: value.op as Exclude<ResultLeaf["op"], "exists" | "not_exists">,
                  valueFrom: { source: "result", nodeId: value.source.nodeId, pointer: "" },
                });
              else
                onChange({
                  source: value.source,
                  op: value.op,
                  valueJSON: numeric ? "0" : "null",
                } as ResultLeaf);
            }}
          />
          {reference ? (
            <SourceEditor
              value={value.valueFrom}
              sources={sources}
              label={`Узел ожидаемого результата${suffix}`}
              pointerLabel={`JSON Pointer ожидаемого результата${suffix}`}
              onChange={(valueFrom) => onChange({ ...value, valueFrom })}
            />
          ) : (
            "valueJSON" in value && (
              <Textarea
                label={`Ожидаемое значение JSON${suffix}`}
                autosize
                minRows={2}
                maxRows={8}
                value={value.valueJSON}
                error={
                  !(numeric
                    ? isResultConditionNumberJSON(value.valueJSON)
                    : isResultConditionScalarJSON(value.valueJSON))
                    ? numeric
                      ? "Введите JSON-число. Строки, null и другие типы для порядка не подходят."
                      : "Введите JSON-строку в кавычках, число, true, false или null."
                    : undefined
                }
                onChange={(event) => onChange({ ...value, valueJSON: event.currentTarget.value })}
              />
            )
          )}
        </>
      )}
      <Text size="xs" c="dimmed">
        {comparison
          ? numeric
            ? "Сравниваются только числа, точно и без округления. Отсутствующее поле или другой тип вызывают ошибку симуляции."
            : 'Сравниваются значения одного JSON-типа: например, "paid", 1, true или null. Числа сравниваются точно: 1, 1.0 и 1e0 равны. Отсутствующее поле и несовместимые типы вызывают ошибку симуляции.'
          : "Проверяется наличие поля, включая null, false, 0, пустую строку, объект или массив. Отсутствие сущности обрабатывается выходом «Не найдена» её узла."}
      </Text>
    </>
  );
}
