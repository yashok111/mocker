import type { ReactElement } from "react";
import { Accordion, ActionIcon, Button, Card, Group, Stack, TextInput } from "@mantine/core";
import { IconPlus, IconTrash } from "@tabler/icons-react";
import { escapeJsonPointerToken, isRecord } from "../documentModel";
import { MediaContentEditor } from "./MediaContentEditor";
import { SchemaFields } from "./SchemaFields";
import { omitEmpty, renameKey } from "./objectFields";

function HeadersEditor({
  headers,
  onChange,
  pointer,
}: {
  headers: unknown;
  onChange: (headers: Record<string, unknown>) => void;
  pointer: string;
}): ReactElement {
  const entries = isRecord(headers) ? Object.entries(headers) : [];
  return (
    <Stack gap="xs">
      {entries.map(([name, rawHeader]) => {
        const header = isRecord(rawHeader) ? rawHeader : {};
        return (
          <Card key={name} withBorder padding="xs">
            <Stack gap="xs">
              <Group align="flex-end" wrap="nowrap">
                <TextInput
                  label="Заголовок ответа"
                  defaultValue={name}
                  onBlur={(event) =>
                    onChange(
                      renameKey(
                        Object.fromEntries(entries),
                        name,
                        event.currentTarget.value.trim(),
                      ),
                    )
                  }
                />
                <ActionIcon
                  aria-label={`Удалить заголовок ${name}`}
                  color="red"
                  variant="default"
                  onClick={() => {
                    const next = Object.fromEntries(entries);
                    delete next[name];
                    onChange(next);
                  }}
                >
                  <IconTrash size={16} />
                </ActionIcon>
              </Group>
              <TextInput
                label={`Описание заголовка ${name}`}
                value={typeof header.description === "string" ? header.description : ""}
                onChange={(event) =>
                  onChange({
                    ...Object.fromEntries(entries),
                    [name]: omitEmpty(header, "description", event.currentTarget.value),
                  })
                }
              />
              <SchemaFields
                pointer={`${pointer}/${escapeJsonPointerToken(name)}/schema`}
                compact
                labelPrefix={`Схема заголовка ${name}`}
                schema={header.schema}
                onChange={(schema) =>
                  onChange({ ...Object.fromEntries(entries), [name]: { ...header, schema } })
                }
              />
            </Stack>
          </Card>
        );
      })}
      <Button
        variant="default"
        size="xs"
        w="fit-content"
        leftSection={<IconPlus size={14} />}
        onClick={() => {
          const current = Object.fromEntries(entries);
          let name = "x-header";
          let suffix = 2;
          while (name in current) name = `x-header-${suffix++}`;
          onChange({ ...current, [name]: { schema: { type: "string" } } });
        }}
      >
        Добавить заголовок
      </Button>
    </Stack>
  );
}

export function ResponseEditor({
  status,
  response,
  onChange,
  pointer,
}: {
  status: string;
  response: Record<string, unknown>;
  onChange: (response: Record<string, unknown>) => void;
  pointer: string;
}): ReactElement {
  return (
    <Stack gap="sm" pt="sm">
      <TextInput
        label={`Описание ответа ${status}`}
        value={typeof response.description === "string" ? response.description : ""}
        onChange={(event) => onChange({ ...response, description: event.currentTarget.value })}
      />
      <Accordion variant="separated" multiple defaultValue={["content"]}>
        <Accordion.Item value="content">
          <Accordion.Control>Тело ответа</Accordion.Control>
          <Accordion.Panel>
            <MediaContentEditor
              pointer={`${pointer}/content`}
              kind="response"
              content={response.content}
              onChange={(content) => onChange({ ...response, content })}
            />
          </Accordion.Panel>
        </Accordion.Item>
        <Accordion.Item value="headers">
          <Accordion.Control>Заголовки ответа</Accordion.Control>
          <Accordion.Panel>
            <HeadersEditor
              pointer={`${pointer}/headers`}
              headers={response.headers}
              onChange={(headers) => onChange({ ...response, headers })}
            />
          </Accordion.Panel>
        </Accordion.Item>
      </Accordion>
    </Stack>
  );
}
