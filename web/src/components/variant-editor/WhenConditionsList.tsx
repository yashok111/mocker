import type { ReactElement } from "react";
import {
  ActionIcon,
  Button,
  Divider,
  Group,
  NativeSelect,
  Stack,
  Text,
  TextInput,
} from "@mantine/core";
import { IconPlus, IconTrash } from "@tabler/icons-react";
import type { Condition } from "@/api/generated/schemas";

// WhenConditionsList is the `when[]` selector list of a response variant:
// the rows that decide whether this variant answers at all. Presentational —
// it renders the conditions it is given and reports every edit through three
// callbacks; the document itself is VariantEditor's.
//
// `when[]` survives every producer switch, because selection belongs to the
// variant and not to its producer — that rule lives in VariantEditor, which
// is why this component never sees the producer.

const DEFAULT_CONDITION: Condition = { in: "query", name: "", op: "equals", value: "" };

const IN_OPTIONS: { value: Condition["in"]; label: string }[] = [
  { value: "query", label: "query-параметр" },
  { value: "header", label: "заголовок" },
  { value: "body", label: "тело" },
];

const OP_OPTIONS: { value: Condition["op"]; label: string }[] = [
  { value: "equals", label: "равно" },
  { value: "contains", label: "содержит" },
  { value: "exists", label: "присутствует" },
];

// Whether a stored "" is possible: never — Go omits it — so the empty
// string only ever means "chosen, not typed yet" and blocks the save.
export function conditionsInvalid(when: Condition[] | undefined): boolean {
  return (when ?? []).some(
    (c) => c.name.trim() === "" || (c.op !== "exists" && (c.value ?? "") === ""),
  );
}

export function WhenConditionsList({
  when,
  testId,
  whenTestId,
  onPatch,
  onRemove,
  onAdd,
}: {
  when: Condition[];
  testId: (name: string) => string;
  whenTestId: (name: string, index: number) => string;
  onPatch: (index: number, patch: Partial<Condition>) => void;
  onRemove: (index: number) => void;
  onAdd: (condition: Condition) => void;
}): ReactElement {
  return (
    <>
      <Divider label="Когда отвечать так" labelPosition="left" />
      <Text size="xs" c="dimmed">
        Все условия ниже должны совпасть, иначе отвечает вариант активного статуса
      </Text>
      <Stack gap="xs" data-testid={testId("when")}>
        {when.map((cond, index) => (
          // Index-keyed on purpose: conditions carry no id of their own and
          // this list is edited in place, never reordered.
          // eslint-disable-next-line react/no-array-index-key
          <Group key={index} gap="xs" wrap="nowrap" align="flex-end">
            <NativeSelect
              label="Где"
              data-testid={whenTestId("in", index)}
              value={cond.in}
              onChange={(e) => onPatch(index, { in: e.currentTarget.value as Condition["in"] })}
            >
              {IN_OPTIONS.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </NativeSelect>
            <TextInput
              label="Имя"
              data-testid={whenTestId("name", index)}
              error={cond.name.trim() === "" ? "заполните" : undefined}
              value={cond.name}
              onChange={(e) => onPatch(index, { name: e.currentTarget.value })}
            />
            <NativeSelect
              label="Условие"
              data-testid={whenTestId("op", index)}
              value={cond.op}
              onChange={(e) => onPatch(index, { op: e.currentTarget.value as Condition["op"] })}
            >
              {OP_OPTIONS.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </NativeSelect>
            <TextInput
              label="Значение"
              disabled={cond.op === "exists"}
              error={cond.op !== "exists" && (cond.value ?? "") === "" ? "заполните" : undefined}
              data-testid={whenTestId("value", index)}
              value={cond.value ?? ""}
              onChange={(e) => onPatch(index, { value: e.currentTarget.value })}
            />
            <ActionIcon
              variant="default"
              color="red"
              onClick={() => onRemove(index)}
              data-testid={whenTestId("remove", index)}
              aria-label="Удалить условие"
            >
              <IconTrash size={16} />
            </ActionIcon>
          </Group>
        ))}
        <Button
          variant="default"
          size="xs"
          w="fit-content"
          leftSection={<IconPlus size={14} />}
          onClick={() => onAdd(DEFAULT_CONDITION)}
          data-testid={testId("when-add")}
        >
          Добавить условие
        </Button>
      </Stack>
    </>
  );
}
