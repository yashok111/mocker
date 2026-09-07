import type { ReactElement } from "react";
import { ActionIcon, Button, Divider, Group, Stack, Text, TextInput } from "@mantine/core";
import { IconPlus, IconTrash } from "@tabler/icons-react";
import type { ProducerMode } from "../VariantEditor";

// HeadersKeyValueList is the hand-rolled key/value editor for a variant's
// response headers. Presentational: it owns no state and writes nothing to
// the wire — the rows come in, an edited copy of them goes back out through
// onRowsChange, and VariantEditor decides what that means for the document
// (an empty name is never written; an empty map becomes `undefined`).
//
// Rows are an ARRAY and not the wire's map, which is VariantEditor's own
// decision and the reason this component exists at all: two rows may share a
// name while one is being typed, and a map would collapse them and lose a
// row under the operator's cursor.
export function HeadersKeyValueList({
  applies,
  producer,
  rows,
  testId,
  onRowsChange,
}: {
  /** Whether the mock plane actually SERVES these headers under the current
   * producer. When it does not, the list is hidden behind a sentence saying
   * where the headers come from instead — storing what would never be sent
   * is the bug a reader of A21 found. */
  applies: boolean;
  producer: ProducerMode;
  rows: [string, string][];
  testId: (name: string) => string;
  onRowsChange: (rows: [string, string][]) => void;
}): ReactElement {
  function setHeader(index: number, key: string, value: string): void {
    onRowsChange(rows.map((row, i) => (i === index ? [key, value] : row)));
  }

  function removeHeader(index: number): void {
    onRowsChange(rows.filter((_, i) => i !== index));
  }

  return (
    <>
      {applies ? (
        <Divider label="Заголовки ответа" labelPosition="left" />
      ) : (
        <Text size="xs" c="dimmed" data-testid={testId("headers-note")}>
          {producer === "function"
            ? "Заголовки ответа задаёт сама функция (третье возвращаемое значение)."
            : "Заголовки ответа отдаются только у закреплённого тела или файла."}
        </Text>
      )}
      <Stack gap="xs" data-testid={testId("headers")} hidden={!applies}>
        {rows.map(([key, value], index) => (
          // Index-keyed: entries are edited in place, never reordered.
          // eslint-disable-next-line react/no-array-index-key
          <Group key={index} gap="xs" wrap="nowrap" align="flex-end">
            <TextInput
              label="Заголовок"
              placeholder="X-Request-Id"
              data-testid={testId(`header-name-${index}`)}
              value={key}
              onChange={(e) => setHeader(index, e.currentTarget.value, value)}
            />
            <TextInput
              label="Значение"
              data-testid={testId(`header-value-${index}`)}
              value={value}
              onChange={(e) => setHeader(index, key, e.currentTarget.value)}
            />
            <ActionIcon
              variant="default"
              color="red"
              onClick={() => removeHeader(index)}
              data-testid={testId(`header-remove-${index}`)}
              aria-label="Удалить заголовок"
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
          onClick={() => onRowsChange([...rows, ["", ""]])}
          data-testid={testId("header-add")}
        >
          Добавить заголовок
        </Button>
      </Stack>
    </>
  );
}
