import { useState } from "react";
import type { ReactElement } from "react";
import {
  ActionIcon,
  Alert,
  Button,
  Card,
  Checkbox,
  Divider,
  Group,
  List,
  Stack,
  Text,
  TextInput,
} from "@mantine/core";
import { IconPlus, IconTrash } from "@tabler/icons-react";
import {
  getSchema,
  escapeJsonPointerToken,
  schemaPointer,
  isRecord,
  renameSchema,
  schemaUsage,
  updateSchema,
  type ApiDocument,
} from "../documentModel";
import { JsonValueEditor } from "./JsonValueEditor";
import { SchemaFields } from "./SchemaFields";
import { useFormDraftStore } from "./FormDraftContext";

function changePropertyName(
  properties: Record<string, unknown>,
  from: string,
  to: string,
): Record<string, unknown> {
  if (from === to) return properties;
  if (to === "") throw new Error("Укажите имя свойства");
  if (to in properties) throw new Error(`Свойство "${to}" уже существует`);
  return Object.fromEntries(
    Object.entries(properties).map(([name, value]) => [name === from ? to : name, value]),
  );
}

export function SharedSchemaForm({
  document,
  name,
  onChange,
}: {
  document: ApiDocument;
  name: string;
  onChange: (document: ApiDocument) => void;
}): ReactElement {
  const [currentName, setCurrentName] = useState(name);
  const [nameDraft, setNameDraft] = useState(name);
  const [renameError, setRenameError] = useState<string>();
  const [propertyErrors, setPropertyErrors] = useState<Record<string, string>>({});
  const draftStore = useFormDraftStore();

  const schema = getSchema(document, currentName);
  if (!schema)
    return <Alert color="yellow">Схема не найдена. Выберите её заново в дереве API.</Alert>;
  const properties = isRecord(schema.properties) ? schema.properties : {};
  const required = Array.isArray(schema.required)
    ? schema.required.filter((item): item is string => typeof item === "string")
    : [];
  const usages = schemaUsage(document, currentName).filter(
    (pointer) => !pointer.startsWith(`${schemaPointer(currentName)}/`),
  );
  const patch = (update: (value: Record<string, unknown>) => Record<string, unknown>) =>
    onChange(updateSchema(document, currentName, update));

  return (
    <Stack gap="md">
      <div>
        <Text fw={600}>Общая схема</Text>
        <Text size="sm" c="dimmed">
          Ссылки на общую схему переименовываются вместе с ней одной операцией.
        </Text>
      </div>
      <Group align="flex-end">
        <TextInput
          label="Имя схемы"
          value={nameDraft}
          onChange={(event) => setNameDraft(event.currentTarget.value)}
          flex={1}
        />
        <Button
          variant="default"
          onClick={() => {
            const nextName = nameDraft.trim();
            try {
              const next = renameSchema(document, currentName, nextName);
              draftStore.moveTree(schemaPointer(currentName), schemaPointer(nextName));
              setCurrentName(nextName);
              setRenameError(undefined);
              onChange(next);
            } catch (error) {
              setRenameError(
                error instanceof Error ? error.message : "Не удалось переименовать схему",
              );
            }
          }}
        >
          Переименовать
        </Button>
      </Group>
      {renameError && <Alert color="red">{renameError}</Alert>}
      <SchemaFields
        pointer={schemaPointer(currentName)}
        schema={schema}
        labelPrefix="Схема"
        onChange={(next) => patch(() => next)}
      />
      <TextInput
        label="Описание схемы"
        value={typeof schema.description === "string" ? schema.description : ""}
        onChange={(event) =>
          patch((value) => {
            const next = { ...value };
            if (event.currentTarget.value === "") delete next.description;
            else next.description = event.currentTarget.value;
            return next;
          })
        }
      />

      <Divider label="Свойства" labelPosition="left" />
      <Stack gap="sm">
        {Object.entries(properties).map(([propertyName, rawProperty]) => {
          const property = isRecord(rawProperty) ? rawProperty : {};
          const isRequired = required.includes(propertyName);
          return (
            <Card key={propertyName} withBorder padding="sm">
              <Stack gap="xs">
                <Group align="flex-end" wrap="nowrap">
                  <TextInput
                    label="Имя свойства"
                    defaultValue={propertyName}
                    error={propertyErrors[propertyName]}
                    onBlur={(event) => {
                      const nextName = event.currentTarget.value.trim();
                      if (nextName === propertyName) {
                        setPropertyErrors({});
                        return;
                      }
                      try {
                        const nextProperties = changePropertyName(
                          properties,
                          propertyName,
                          nextName,
                        );
                        draftStore.moveTree(
                          `${schemaPointer(currentName)}/properties/${escapeJsonPointerToken(propertyName)}`,
                          `${schemaPointer(currentName)}/properties/${escapeJsonPointerToken(nextName)}`,
                        );
                        const nextRequired = required.map((item) =>
                          item === propertyName ? nextName : item,
                        );
                        patch((value) => ({
                          ...value,
                          properties: nextProperties,
                          ...(Array.isArray(value.required) ? { required: nextRequired } : {}),
                        }));
                        setPropertyErrors({});
                      } catch (error) {
                        setPropertyErrors((current) => ({
                          ...current,
                          [propertyName]:
                            error instanceof Error
                              ? error.message
                              : "Не удалось переименовать свойство",
                        }));
                      }
                    }}
                  />
                  <Checkbox
                    aria-label={`${propertyName} обязательно`}
                    label="Обязательное"
                    checked={isRequired}
                    onChange={(event) => {
                      const nextRequired = event.currentTarget.checked
                        ? [...required, propertyName]
                        : required.filter((item) => item !== propertyName);
                      patch((value) => {
                        const next = { ...value };
                        if (nextRequired.length === 0) delete next.required;
                        else next.required = nextRequired;
                        return next;
                      });
                    }}
                  />
                  <ActionIcon
                    aria-label={`Удалить свойство ${propertyName}`}
                    color="red"
                    variant="default"
                    onClick={() => {
                      draftStore.removeTree(
                        `${schemaPointer(currentName)}/properties/${escapeJsonPointerToken(propertyName)}`,
                      );
                      const nextProperties = { ...properties };
                      delete nextProperties[propertyName];
                      const nextRequired = required.filter((item) => item !== propertyName);
                      patch((value) => {
                        const next: Record<string, unknown> = {
                          ...value,
                          properties: nextProperties,
                        };
                        if (nextRequired.length === 0) delete next.required;
                        else next.required = nextRequired;
                        return next;
                      });
                    }}
                  >
                    <IconTrash size={16} />
                  </ActionIcon>
                </Group>
                <TextInput
                  aria-label={`Описание свойства ${propertyName}`}
                  label="Описание"
                  value={typeof property.description === "string" ? property.description : ""}
                  onChange={(event) => {
                    const nextProperty = { ...property };
                    if (event.currentTarget.value === "") delete nextProperty.description;
                    else nextProperty.description = event.currentTarget.value;
                    patch((value) => ({
                      ...value,
                      properties: { ...properties, [propertyName]: nextProperty },
                    }));
                  }}
                />
                <SchemaFields
                  pointer={`${schemaPointer(currentName)}/properties/${escapeJsonPointerToken(propertyName)}`}
                  schema={property}
                  labelPrefix={`Свойство ${propertyName}`}
                  onChange={(nextProperty) =>
                    patch((value) => ({
                      ...value,
                      properties: { ...properties, [propertyName]: nextProperty },
                    }))
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
            let propertyName = "property";
            let suffix = 2;
            while (propertyName in properties) propertyName = `property${suffix++}`;
            patch((value) => ({
              ...value,
              type: value.type ?? "object",
              properties: { ...properties, [propertyName]: { type: "string" } },
            }));
          }}
        >
          Добавить свойство
        </Button>
      </Stack>

      <Divider label="Использование" labelPosition="left" />
      {usages.length === 0 ? (
        <Text size="sm" c="dimmed">
          На эту схему пока нет ссылок.
        </Text>
      ) : (
        <List size="sm">
          {usages.map((pointer) => (
            <List.Item key={pointer}>{pointer}</List.Item>
          ))}
        </List>
      )}

      <Divider label="Расширенный режим" labelPosition="left" />
      <JsonValueEditor
        pointer={schemaPointer(currentName)}
        label="Полная схема JSON"
        description="Композиции oneOf/allOf/anyOf, ограничения и другие ключевые слова можно изменить здесь. Некорректный JSON останется только в этом поле."
        value={schema}
        resetKey={`schema:${currentName}`}
        minRows={10}
        validate={(value) => (isRecord(value) ? undefined : "Схема должна быть объектом JSON")}
        onChange={(value) => {
          if (isRecord(value)) patch(() => value);
        }}
      />
    </Stack>
  );
}
