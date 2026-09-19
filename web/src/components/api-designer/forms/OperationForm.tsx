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
  NativeSelect,
  Stack,
  Tabs,
  Text,
  TextInput,
  Textarea,
} from "@mantine/core";
import { IconPlus, IconTrash } from "@tabler/icons-react";
import {
  HTTP_METHODS,
  getOperation,
  operationPointer,
  escapeJsonPointerToken,
  isRecord,
  renameOperation,
  updateOperation,
  type ApiDocument,
  type OperationLocation,
} from "../documentModel";
import { JsonValueEditor } from "./JsonValueEditor";
import { MediaContentEditor } from "./MediaContentEditor";
import { ResponseEditor } from "./ResponseEditor";
import { SchemaFields } from "./SchemaFields";
import { omitEmpty } from "./objectFields";
import { StringListEditor } from "./StringListEditor";
import { useFormDraftStore } from "./FormDraftContext";

export function OperationForm({
  document,
  location,
  onChange,
}: {
  document: ApiDocument;
  location: OperationLocation;
  onChange: (document: ApiDocument) => void;
}): ReactElement {
  const [current, setCurrent] = useState({ ...location, method: location.method.toLowerCase() });
  const [pathDraft, setPathDraft] = useState(location.path);
  const [methodDraft, setMethodDraft] = useState(location.method.toLowerCase());
  const [moveError, setMoveError] = useState<string>();
  const [newStatus, setNewStatus] = useState("201");
  const draftStore = useFormDraftStore();

  const operation = getOperation(document, current);
  if (!operation) {
    return <Alert color="yellow">Операция не найдена. Выберите её заново в дереве API.</Alert>;
  }
  const parameters = Array.isArray(operation.parameters) ? operation.parameters : [];
  const responses = isRecord(operation.responses) ? operation.responses : {};
  const pointer = operationPointer(current);
  const patch = (update: (value: Record<string, unknown>) => Record<string, unknown>) =>
    onChange(updateOperation(document, current, update));

  return (
    <Stack gap="md">
      <div>
        <Text fw={600}>Операция</Text>
        <Text size="sm" c="dimmed">
          Путь и метод определяют адрес операции. Остальные поля описывают контракт для клиента.
        </Text>
      </div>
      <Group align="flex-end">
        <NativeSelect
          label="Метод"
          value={methodDraft}
          onChange={(event) => setMethodDraft(event.currentTarget.value)}
        >
          {HTTP_METHODS.map((method) => (
            <option key={method} value={method}>
              {method.toUpperCase()}
            </option>
          ))}
        </NativeSelect>
        <TextInput
          label="Путь"
          value={pathDraft}
          onChange={(event) => setPathDraft(event.currentTarget.value)}
          flex={1}
        />
        <Button
          variant="default"
          onClick={() => {
            const target = { path: pathDraft.trim(), method: methodDraft };
            try {
              const next = renameOperation(document, current, target);
              draftStore.moveTree(operationPointer(current), operationPointer(target));
              setCurrent(target);
              setMoveError(undefined);
              onChange(next);
            } catch (error) {
              setMoveError(error instanceof Error ? error.message : "Не удалось изменить адрес");
            }
          }}
        >
          Изменить адрес
        </Button>
      </Group>
      {moveError && <Alert color="red">{moveError}</Alert>}
      <TextInput
        label="Краткое название"
        value={typeof operation.summary === "string" ? operation.summary : ""}
        onChange={(event) =>
          patch((value) => omitEmpty(value, "summary", event.currentTarget.value))
        }
      />
      <Textarea
        label="Описание операции"
        rows={3}
        value={typeof operation.description === "string" ? operation.description : ""}
        onChange={(event) =>
          patch((value) => omitEmpty(value, "description", event.currentTarget.value))
        }
      />
      <Group grow align="flex-start">
        <TextInput
          label="operationId"
          value={typeof operation.operationId === "string" ? operation.operationId : ""}
          onChange={(event) =>
            patch((value) => omitEmpty(value, "operationId", event.currentTarget.value))
          }
        />
        <StringListEditor
          label="Теги"
          pointer={`${pointer}/tags`}
          value={operation.tags}
          onChange={(tags) => {
            patch((value) => omitEmpty(value, "tags", tags.length === 0 ? undefined : tags));
          }}
        />
      </Group>
      <Checkbox
        label="Операция устарела (deprecated)"
        checked={operation.deprecated === true}
        onChange={(event) =>
          patch((value) => omitEmpty(value, "deprecated", event.currentTarget.checked))
        }
      />

      <Divider label="Параметры" labelPosition="left" />
      <Stack gap="sm">
        {parameters.map((rawParameter, index) => {
          const parameter = isRecord(rawParameter) ? rawParameter : {};
          const name =
            typeof parameter.name === "string" ? parameter.name : `параметр ${index + 1}`;
          const replace = (next: Record<string, unknown>) => {
            const list = [...parameters];
            list[index] = next;
            patch((value) => ({ ...value, parameters: list }));
          };
          return (
            <Card key={index} withBorder padding="sm">
              <Stack gap="xs">
                <Group align="flex-end" wrap="nowrap">
                  <TextInput
                    label="Имя параметра"
                    error={
                      parameter.in === "path" && !current.path.includes(`{${name}}`)
                        ? `В пути отсутствует {${name}}`
                        : undefined
                    }
                    value={typeof parameter.name === "string" ? parameter.name : ""}
                    onChange={(event) => replace({ ...parameter, name: event.currentTarget.value })}
                  />
                  <NativeSelect
                    label="Где передаётся"
                    value={typeof parameter.in === "string" ? parameter.in : "query"}
                    onChange={(event) =>
                      replace({
                        ...parameter,
                        in: event.currentTarget.value,
                        ...(event.currentTarget.value === "path" ? { required: true } : {}),
                      })
                    }
                  >
                    {[
                      ["path", "в пути"],
                      ["query", "в query"],
                      ["header", "в заголовке"],
                      ["cookie", "в cookie"],
                    ].map(([value, label]) => (
                      <option key={value} value={value}>
                        {label}
                      </option>
                    ))}
                  </NativeSelect>
                  <Checkbox
                    label="Обязательный"
                    checked={parameter.required === true || parameter.in === "path"}
                    disabled={parameter.in === "path"}
                    onChange={(event) =>
                      replace(omitEmpty(parameter, "required", event.currentTarget.checked))
                    }
                  />
                  <ActionIcon
                    aria-label={`Удалить параметр ${name}`}
                    color="red"
                    variant="default"
                    onClick={() => {
                      draftStore.removeTree(`${pointer}/parameters/${index}`);
                      for (let i = index + 1; i < parameters.length; i++)
                        draftStore.moveTree(
                          `${pointer}/parameters/${i}`,
                          `${pointer}/parameters/${i - 1}`,
                        );
                      const list = parameters.filter((_, itemIndex) => itemIndex !== index);
                      patch((value) =>
                        omitEmpty(value, "parameters", list.length === 0 ? undefined : list),
                      );
                    }}
                  >
                    <IconTrash size={16} />
                  </ActionIcon>
                </Group>
                <TextInput
                  aria-label={`Описание параметра ${name}`}
                  label="Описание"
                  value={typeof parameter.description === "string" ? parameter.description : ""}
                  onChange={(event) =>
                    replace(omitEmpty(parameter, "description", event.currentTarget.value))
                  }
                />
                <SchemaFields
                  pointer={`${pointer}/parameters/${index}/schema`}
                  schema={parameter.schema}
                  labelPrefix={`Схема параметра ${name}`}
                  onChange={(schema) => replace({ ...parameter, schema })}
                />
                <JsonValueEditor
                  pointer={`${pointer}/parameters/${index}/example`}
                  label={`Пример параметра ${name}`}
                  value={parameter.example}
                  resetKey={`parameter:${index}`}
                  onChange={(example) => replace({ ...parameter, example })}
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
          onClick={() =>
            patch((value) => ({
              ...value,
              parameters: [
                ...parameters,
                { name: "parameter", in: "query", schema: { type: "string" } },
              ],
            }))
          }
        >
          Добавить параметр
        </Button>
      </Stack>

      <Divider label="Тело запроса" labelPosition="left" />
      {isRecord(operation.requestBody) ? (
        <Stack gap="sm">
          <Group justify="space-between">
            <Checkbox
              label="Тело запроса обязательно"
              checked={operation.requestBody.required === true}
              onChange={(event) =>
                patch((value) => ({
                  ...value,
                  requestBody: omitEmpty(
                    operation.requestBody as Record<string, unknown>,
                    "required",
                    event.currentTarget.checked,
                  ),
                }))
              }
            />
            <Button
              color="red"
              variant="subtle"
              size="xs"
              onClick={() => {
                draftStore.removeTree(`${pointer}/requestBody`);
                patch((value) => omitEmpty(value, "requestBody", undefined));
              }}
            >
              Удалить тело запроса
            </Button>
          </Group>
          <MediaContentEditor
            pointer={`${pointer}/requestBody/content`}
            kind="request"
            content={operation.requestBody.content}
            onChange={(content) =>
              patch((value) => ({
                ...value,
                requestBody: { ...(operation.requestBody as Record<string, unknown>), content },
              }))
            }
          />
        </Stack>
      ) : (
        <Button
          variant="default"
          w="fit-content"
          onClick={() =>
            patch((value) => ({
              ...value,
              requestBody: { content: { "application/json": { schema: { type: "object" } } } },
            }))
          }
        >
          Добавить тело запроса
        </Button>
      )}

      <Divider label="Ответы" labelPosition="left" />
      {Object.keys(responses).length > 0 && (
        <Tabs defaultValue={Object.keys(responses)[0]} keepMounted={false}>
          <Tabs.List>
            {Object.keys(responses).map((status) => (
              <Tabs.Tab key={status} value={status}>
                Ответ {status}
              </Tabs.Tab>
            ))}
          </Tabs.List>
          {Object.entries(responses).map(([status, rawResponse]) => {
            const response = isRecord(rawResponse) ? rawResponse : { description: "" };
            return (
              <Tabs.Panel key={status} value={status}>
                <Group justify="flex-end" mt="xs">
                  <Button
                    color="red"
                    variant="subtle"
                    size="xs"
                    onClick={() => {
                      draftStore.removeTree(
                        `${pointer}/responses/${escapeJsonPointerToken(status)}`,
                      );
                      const next = { ...responses };
                      delete next[status];
                      patch((value) => ({ ...value, responses: next }));
                    }}
                  >
                    Удалить ответ {status}
                  </Button>
                </Group>
                <ResponseEditor
                  pointer={`${pointer}/responses/${escapeJsonPointerToken(status)}`}
                  status={status}
                  response={response}
                  onChange={(nextResponse) =>
                    patch((value) => ({
                      ...value,
                      responses: { ...responses, [status]: nextResponse },
                    }))
                  }
                />
              </Tabs.Panel>
            );
          })}
        </Tabs>
      )}
      <Group align="flex-end">
        <TextInput
          label="Новый статус ответа"
          value={newStatus}
          onChange={(event) => setNewStatus(event.currentTarget.value)}
        />
        <Button
          variant="default"
          disabled={newStatus.trim() === "" || newStatus in responses}
          onClick={() => {
            const status = newStatus.trim();
            patch((value) => ({
              ...value,
              responses: { ...responses, [status]: { description: "Ответ" } },
            }));
          }}
        >
          Добавить ответ
        </Button>
      </Group>
    </Stack>
  );
}
