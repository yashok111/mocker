import type { ReactElement } from "react";
import { Divider, SimpleGrid, Stack, Text, TextInput, Textarea } from "@mantine/core";
import { isRecord, setAtJsonPointer, type ApiDocument } from "../documentModel";
import { JsonValueEditor } from "./JsonValueEditor";

function patchInfo(document: ApiDocument, field: string, value: string): ApiDocument {
  const info = isRecord(document.info) ? document.info : {};
  const next = { ...info };
  if (value === "") delete next[field];
  else next[field] = value;
  return { ...document, info: next };
}

export function DocumentMetadataForm({
  document,
  onChange,
}: {
  document: ApiDocument;
  onChange: (document: ApiDocument) => void;
}): ReactElement {
  const info = isRecord(document.info) ? document.info : {};
  const components = isRecord(document.components) ? document.components : {};
  return (
    <Stack gap="md">
      <div>
        <Text fw={600}>Документ OpenAPI</Text>
        <Text size="sm" c="dimmed">
          Эти данные видят читатели документации и инструменты, которые импортируют контракт.
        </Text>
      </div>
      <SimpleGrid cols={{ base: 1, xs: 3 }}>
        <TextInput
          label="Название API"
          required
          value={typeof info.title === "string" ? info.title : ""}
          onChange={(event) => onChange(patchInfo(document, "title", event.currentTarget.value))}
        />
        <TextInput
          label="Версия API"
          required
          value={typeof info.version === "string" ? info.version : ""}
          onChange={(event) => onChange(patchInfo(document, "version", event.currentTarget.value))}
        />
        <TextInput
          label="Версия OpenAPI"
          value={typeof document.openapi === "string" ? document.openapi : ""}
          onChange={(event) => onChange({ ...document, openapi: event.currentTarget.value })}
        />
      </SimpleGrid>
      <Textarea
        label="Описание API"
        rows={4}
        value={typeof info.description === "string" ? info.description : ""}
        onChange={(event) =>
          onChange(patchInfo(document, "description", event.currentTarget.value))
        }
      />
      <Divider label="Безопасность" labelPosition="left" />
      <Text size="sm" c="dimmed">
        Декларации описывают требования контракта. Мок не начинает проверять авторизацию только
        из-за их наличия.
      </Text>
      <JsonValueEditor
        label="Требования security (JSON)"
        pointer="/security"
        description='Массив требований, например [{"bearer": []}]'
        value={document.security ?? []}
        resetKey="document-security"
        onChange={(security) => onChange({ ...document, security })}
      />
      <JsonValueEditor
        label="Схемы securitySchemes (JSON)"
        pointer="/components/securitySchemes"
        value={components.securitySchemes ?? {}}
        resetKey="document-security-schemes"
        onChange={(securitySchemes) =>
          onChange(setAtJsonPointer(document, "/components/securitySchemes", securitySchemes))
        }
      />
    </Stack>
  );
}
