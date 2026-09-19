import type { ReactElement } from "react";
import { ActionIcon, Button, Card, Group, Stack, TextInput } from "@mantine/core";
import { IconPlus, IconTrash } from "@tabler/icons-react";
import { escapeJsonPointerToken, isRecord } from "../documentModel";
import { JsonValueEditor } from "./JsonValueEditor";
import { SchemaFields } from "./SchemaFields";
import { renameKey } from "./objectFields";
import { useFormDraftStore } from "./FormDraftContext";

export function MediaContentEditor({
  content,
  onChange,
  kind,
  pointer,
}: {
  content: unknown;
  onChange: (content: Record<string, unknown>) => void;
  kind: "request" | "response";
  pointer: string;
}): ReactElement {
  const entries = isRecord(content) ? Object.entries(content) : [];
  const label = kind === "request" ? "запроса" : "ответа";
  const draftStore = useFormDraftStore();
  return (
    <Stack gap="sm">
      {entries.map(([mediaType, rawMedia]) => {
        const media = isRecord(rawMedia) ? rawMedia : {};
        return (
          <Card key={mediaType} withBorder padding="sm">
            <Stack gap="xs">
              <Group align="flex-end" wrap="nowrap">
                <TextInput
                  label="Media type"
                  defaultValue={mediaType}
                  onBlur={(event) => {
                    const nextName = event.currentTarget.value.trim();
                    const previous = Object.fromEntries(entries);
                    const next = renameKey(previous, mediaType, nextName);
                    if (next === previous) return;
                    draftStore.moveTree(
                      `${pointer}/${escapeJsonPointerToken(mediaType)}`,
                      `${pointer}/${escapeJsonPointerToken(nextName)}`,
                    );
                    onChange(next);
                  }}
                />
                <ActionIcon
                  aria-label={`Удалить media type ${mediaType}`}
                  color="red"
                  variant="default"
                  onClick={() => {
                    draftStore.removeTree(`${pointer}/${escapeJsonPointerToken(mediaType)}`);
                    const next = Object.fromEntries(entries);
                    delete next[mediaType];
                    onChange(next);
                  }}
                >
                  <IconTrash size={16} />
                </ActionIcon>
              </Group>
              <SchemaFields
                pointer={`${pointer}/${escapeJsonPointerToken(mediaType)}/schema`}
                schema={media.schema}
                labelPrefix={`Схема ${label} ${mediaType}`}
                onChange={(schema) =>
                  onChange({ ...Object.fromEntries(entries), [mediaType]: { ...media, schema } })
                }
              />
              <JsonValueEditor
                pointer={`${pointer}/${escapeJsonPointerToken(mediaType)}/example`}
                label={`Пример ${label} ${mediaType}`}
                value={media.example}
                resetKey={`${kind}:${mediaType}`}
                onChange={(example) =>
                  onChange({ ...Object.fromEntries(entries), [mediaType]: { ...media, example } })
                }
              />
              <JsonValueEditor
                pointer={`${pointer}/${escapeJsonPointerToken(mediaType)}/examples`}
                label={`Именованные примеры ${label} ${mediaType}`}
                description="Объект examples в формате OpenAPI"
                value={media.examples ?? {}}
                resetKey={`${kind}:${mediaType}:examples`}
                onChange={(examples) =>
                  onChange({ ...Object.fromEntries(entries), [mediaType]: { ...media, examples } })
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
          let candidate = "application/json";
          let suffix = 2;
          while (candidate in current) candidate = `application/json; profile=${suffix++}`;
          onChange({ ...current, [candidate]: { schema: { type: "object" } } });
        }}
      >
        Добавить media type
      </Button>
    </Stack>
  );
}
