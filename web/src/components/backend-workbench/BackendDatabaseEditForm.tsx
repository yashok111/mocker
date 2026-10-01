import { useState } from "react";
import {
  Button,
  Checkbox,
  Group,
  NativeSelect,
  Stack,
  Text,
  Textarea,
  TextInput,
} from "@mantine/core";
import type {
  BackendNode,
  BackendProposalCommand,
  BackendProposalCriterionInput,
  BackendDatabaseColumnPair,
} from "@/api/generated/schemas";
import { relationalFacets } from "./backendDatabaseReads";

type FKCommand = Extract<BackendProposalCommand, { type: "alter_constraint" }>;
export function BackendDatabaseEditForm({
  nodes,
  facetKey,
  onAdd,
  criteria = [],
  designedConstraints = [],
  initialColumnId,
}: {
  nodes: BackendNode[];
  facetKey: string;
  criteria?: BackendProposalCriterionInput[];
  designedConstraints?: Pick<BackendNode, "id" | "name" | "parentId">[];
  initialColumnId?: string;
  onAdd: (command: BackendProposalCommand) => void;
}) {
  const available = nodes.filter((node) => relationalFacets(node)[facetKey]);
  const tables = available.filter((node) => node.kind === "table");
  const columns = available.filter((node) => node.kind === "column");
  const constraints = [
    ...available.filter((node) => {
      const facet = relationalFacets(node)[facetKey];
      return (
        node.kind === "constraint" &&
        facet &&
        "constraintKind" in facet &&
        facet.constraintKind === "foreign_key"
      );
    }),
    ...designedConstraints,
  ];
  const [type, setType] = useState("nullable");
  const [column, setColumn] = useState(initialColumnId ?? columns[0]?.id ?? "");
  const [nullable, setNullable] = useState(initialColumnId ? "false" : "true");
  const [table, setTable] = useState(tables[0]?.id ?? "");
  const [target, setTarget] = useState(tables[1]?.id ?? tables[0]?.id ?? "");
  const [constraint, setConstraint] = useState(constraints[0]?.id ?? "");
  const [name, setName] = useState("");
  const [reason, setReason] = useState("");
  const [pairs, setPairs] = useState<BackendDatabaseColumnPair[]>([
    { fromColumnId: "", toColumnId: "" },
  ]);
  const [updateAction, setUpdate] = useState<FKCommand["updateAction"]>("no_action");
  const [deleteAction, setDelete] = useState<FKCommand["deleteAction"]>("no_action");
  const [matchType, setMatch] = useState<FKCommand["matchType"]>("simple");
  const [deferrable, setDeferrable] = useState(false);
  const [deferred, setDeferred] = useState(false);
  const [criterionKind, setCriterionKind] =
    useState<BackendProposalCriterionInput["kind"]>("existing_data");
  const [description, setDescription] = useState("");
  const [criterionTarget, setCriterionTarget] = useState(columns[0]?.id ?? tables[0]?.id ?? "");
  const [error, setError] = useState("");
  const sourceTable =
    type === "update_fk"
      ? (constraints.find((node) => node.id === constraint)?.parentId ?? "")
      : table;
  const options = (items: Pick<BackendNode, "id" | "name" | "parentId">[]) => [
    { value: "", label: "Выберите объект" },
    ...items.map((node) => ({
      value: node.id,
      label: `${nodes.find((parent) => parent.id === node.parentId)?.name ?? ""}.${node.name}`,
    })),
  ];
  const actions = ["no_action", "restrict", "cascade", "set_null", "set_default"];
  function move(index: number, delta: number) {
    const next = [...pairs];
    const from = next[index],
      to = next[index + delta];
    if (!from || !to) return;
    [next[index], next[index + delta]] = [to, from];
    setPairs(next);
  }
  function submit() {
    setError("");
    if (!reason.trim() || new TextEncoder().encode(reason).length > 4096) {
      setError("Укажите причину до 4096 байт");
      return;
    }
    const common = { commandId: crypto.randomUUID(), reason: reason.trim() };
    if (type === "nullable") {
      if (!column) {
        setError("Выберите колонку");
        return;
      }
      onAdd({ ...common, type: "alter_column", columnId: column, nullable: nullable === "true" });
    } else if (type === "criteria") {
      if (
        !description.trim() ||
        !criterionTarget ||
        new TextEncoder().encode(description).length > 4096 ||
        criteria.length >= 100
      ) {
        setError("Укажите объект и описание критерия до 4096 байт; всего до 100 критериев");
        return;
      }
      onAdd({
        ...common,
        type: "set_criteria",
        criteria: [
          ...criteria,
          {
            key: crypto.randomUUID(),
            kind: criterionKind,
            targetIds: [criterionTarget],
            description: description.trim(),
          },
        ],
      });
    } else {
      if (
        !sourceTable ||
        !target ||
        (type === "create_fk" && !name.trim()) ||
        pairs.some((pair) => !pair.fromColumnId || !pair.toColumnId)
      ) {
        setError("Выберите обе таблицы и заполните все пары колонок и имя FK");
        return;
      }
      const fk = {
        ...common,
        type: "alter_constraint" as const,
        targetTableId: target,
        columnPairs: pairs.map((pair) => ({ ...pair })),
        updateAction,
        deleteAction,
        matchType,
        deferrable,
        initiallyDeferred: deferrable && deferred,
      };
      onAdd(
        type === "create_fk"
          ? { ...fk, action: "create", tableId: table, name: name.trim() }
          : { ...fk, action: "update", constraintId: constraint },
      );
    }
    setReason("");
  }
  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        submit();
      }}
    >
      <Stack gap="sm">
        <NativeSelect
          label="Изменение"
          value={type}
          onChange={(event) => {
            setType(event.currentTarget.value);
            setError("");
          }}
          data={[
            { value: "nullable", label: "NULL / NOT NULL" },
            { value: "create_fk", label: "Создать FK" },
            { value: "update_fk", label: "Изменить FK" },
            { value: "criteria", label: "Добавить критерий" },
          ]}
        />
        {type === "nullable" ? (
          <>
            <NativeSelect
              label="Колонка"
              data={options(columns)}
              value={column}
              onChange={(event) => setColumn(event.currentTarget.value)}
            />
            <NativeSelect
              label="Допускает NULL"
              value={nullable}
              onChange={(event) => setNullable(event.currentTarget.value)}
              data={[
                { value: "true", label: "Да — NULL" },
                { value: "false", label: "Нет — NOT NULL" },
              ]}
            />
            <Text size="sm">
              Источник:{" "}
              {JSON.stringify(
                (
                  relationalFacets(
                    columns.find((node) => node.id === column) ??
                      columns[0] ??
                      ({ attributes: {} } as BackendNode),
                  )[facetKey] as { nullable?: unknown } | undefined
                )?.nullable ?? "неизвестно",
              )}
            </Text>
          </>
        ) : type === "criteria" ? (
          <>
            <NativeSelect
              label="Вид критерия"
              data={[
                "existing_data",
                "writers",
                "referential_integrity",
                "target_uniqueness",
                "migration_plan",
              ]}
              value={criterionKind}
              onChange={(event) =>
                setCriterionKind(event.currentTarget.value as typeof criterionKind)
              }
            />
            <NativeSelect
              label="Объект критерия"
              data={options(available)}
              value={criterionTarget}
              onChange={(event) => setCriterionTarget(event.currentTarget.value)}
            />
            <Textarea
              label="Описание критерия"
              value={description}
              onChange={(event) => setDescription(event.currentTarget.value)}
            />
          </>
        ) : (
          <>
            {type === "create_fk" ? (
              <>
                <NativeSelect
                  label="Таблица источника"
                  value={table}
                  data={options(tables)}
                  onChange={(event) => {
                    setTable(event.currentTarget.value);
                    setPairs([{ fromColumnId: "", toColumnId: "" }]);
                  }}
                />
                <TextInput
                  label="Имя FK"
                  value={name}
                  onChange={(event) => setName(event.currentTarget.value)}
                />
              </>
            ) : (
              <NativeSelect
                label="Ограничение FK"
                value={constraint}
                data={options(constraints)}
                onChange={(event) => {
                  setConstraint(event.currentTarget.value);
                  setPairs([{ fromColumnId: "", toColumnId: "" }]);
                }}
              />
            )}
            <NativeSelect
              label="Таблица цели"
              value={target}
              data={options(tables)}
              onChange={(event) => {
                setTarget(event.currentTarget.value);
                setPairs([{ fromColumnId: "", toColumnId: "" }]);
              }}
            />
            {pairs.map((pair, index) => (
              <Stack key={index} gap="xs" component="fieldset">
                <legend>Пара {index + 1}</legend>
                <NativeSelect
                  label={`Исходная колонка ${index + 1}`}
                  value={pair.fromColumnId}
                  data={options(columns.filter((node) => node.parentId === sourceTable))}
                  onChange={(event) =>
                    setPairs(
                      pairs.map((value, i) =>
                        i === index ? { ...value, fromColumnId: event.currentTarget.value } : value,
                      ),
                    )
                  }
                />
                <NativeSelect
                  label={`Целевая колонка ${index + 1}`}
                  value={pair.toColumnId}
                  data={options(columns.filter((node) => node.parentId === target))}
                  onChange={(event) =>
                    setPairs(
                      pairs.map((value, i) =>
                        i === index ? { ...value, toColumnId: event.currentTarget.value } : value,
                      ),
                    )
                  }
                />
                <Group>
                  <Button
                    variant="default"
                    disabled={index === 0}
                    aria-label={`Пара ${index + 1} вверх`}
                    onClick={() => move(index, -1)}
                  >
                    Вверх
                  </Button>
                  <Button
                    variant="default"
                    disabled={index === pairs.length - 1}
                    aria-label={`Пара ${index + 1} вниз`}
                    onClick={() => move(index, 1)}
                  >
                    Вниз
                  </Button>
                  <Button
                    variant="subtle"
                    disabled={pairs.length === 1}
                    aria-label={`Удалить пару ${index + 1}`}
                    onClick={() => setPairs(pairs.filter((_, i) => i !== index))}
                  >
                    Удалить
                  </Button>
                </Group>
              </Stack>
            ))}
            <Button
              variant="default"
              disabled={pairs.length >= 64}
              onClick={() => setPairs([...pairs, { fromColumnId: "", toColumnId: "" }])}
            >
              Добавить пару
            </Button>
            <NativeSelect
              label="При обновлении"
              data={actions}
              value={updateAction}
              onChange={(event) => setUpdate(event.currentTarget.value as typeof updateAction)}
            />
            <NativeSelect
              label="При удалении"
              data={actions}
              value={deleteAction}
              onChange={(event) => setDelete(event.currentTarget.value as typeof deleteAction)}
            />
            <NativeSelect
              label="Сопоставление FK"
              data={["simple", "full", "partial"]}
              value={matchType}
              onChange={(event) => setMatch(event.currentTarget.value as typeof matchType)}
            />
            <Checkbox
              label="Отложенное ограничение"
              checked={deferrable}
              onChange={(event) => {
                setDeferrable(event.currentTarget.checked);
                if (!event.currentTarget.checked) setDeferred(false);
              }}
            />
            <Checkbox
              label="Проверять в конце транзакции"
              checked={deferred}
              disabled={!deferrable}
              onChange={(event) => setDeferred(event.currentTarget.checked)}
            />
            <Text size="sm">
              Уникальность цели и данные требуют проверки. Ограничения диалекта будут показаны при
              предпросмотре.
            </Text>
          </>
        )}
        <Textarea
          label="Причина изменения"
          value={reason}
          onChange={(event) => setReason(event.currentTarget.value)}
          required
        />
        {error && (
          <Text role="alert" c="red">
            {error}
          </Text>
        )}
        <Button type="submit">Добавить в буфер</Button>
      </Stack>
    </form>
  );
}
