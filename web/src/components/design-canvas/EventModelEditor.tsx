import { useEffect, useState, type ReactElement } from "react";
import {
  Alert,
  Button,
  Checkbox,
  Divider,
  Group,
  Modal,
  NativeSelect,
  NumberInput,
  Stack,
  Text,
  Textarea,
  TextInput,
} from "@mantine/core";
import type { FormDraftStore } from "../api-designer/forms/formDraftStore";
import { createCanvasId } from "./canvasId";
import {
  createEventModel,
  describeArrowSide,
  eventDependents,
  removeEventEntity,
  removeEventOperation,
  resolveEventOperation,
} from "./canvasEvents";
import type { CanvasDocument } from "./types";
import type { EventCollection, EventModel } from "./eventTypes";

const collectionLabels: Record<EventCollection, string> = {
  servers: "Kafka servers",
  channels: "Topics",
  messages: "Типы событий",
  schemas: "JSON-схемы",
  contracts: "Событийные контракты",
};
const collections = Object.keys(collectionLabels) as EventCollection[];
const emptyOption = { value: "", label: "Выберите объект" };
const targetCollections: Record<string, EventCollection> = {
  "event-server": "servers",
  "event-channel": "channels",
  "event-message": "messages",
  "event-schema": "schemas",
  "event-contract": "contracts",
};

function diagnosticControl(kind: string, pointer?: string): { label: string; index: number } {
  const parts = pointer?.split("/").filter(Boolean) ?? [];
  const field = parts.at(-1);
  const operationAt = parts.indexOf("operations");
  const exampleAt = parts.indexOf("examples");
  const operationIndex = operationAt >= 0 ? Number(parts[operationAt + 1]) : 0;
  const exampleIndex = exampleAt >= 0 ? Number(parts[exampleAt + 1]) : 0;
  if (exampleAt >= 0 && Number.isInteger(exampleIndex) && exampleIndex >= 0) {
    if (field === "payloadJSON")
      return { label: `Payload JSON · пример ${exampleIndex + 1}`, index: 0 };
    if (field === "headersJSON")
      return { label: `Headers JSON · пример ${exampleIndex + 1}`, index: 0 };
    if (field === "name") return { label: `Имя примера ${exampleIndex + 1}`, index: 0 };
  }
  const index = Number.isInteger(operationIndex) && operationIndex >= 0 ? operationIndex : 0;
  if (operationAt >= 0) {
    const labels: Record<string, string> = {
      name: "Название операции",
      description: "Описание операции",
      action: "Роль",
      channelId: "Topic операции",
      messageId: "Тип события операции",
      groupId: "Consumer group ID",
      clientId: "Client ID",
    };
    return { label: labels[field ?? ""] ?? "Название операции", index };
  }
  const labels: Record<string, string> = {
    schemaJSON: "JSON Schema Draft 07",
    address: "Адрес topic",
    discriminatorProperty: "Свойство discriminator",
    partitions: "Partitions",
    replicas: "Replicas",
    host: "Kafka host:port",
    protocol: "Протокол",
    auth: "Аутентификация",
    payloadSchemaId: "Схема payload",
    headersSchemaId: "Схема headers",
    keySchemaId: "Схема Kafka key",
    participantId: "Владелец-приложение",
    version: "Версия контракта",
    name: "Название",
    description: "Описание",
  };
  const fallback: Record<string, string> = {
    "event-schema": "JSON Schema Draft 07",
    "event-message": "Схема payload",
    "event-channel": "Адрес topic",
    "event-server": "Kafka host:port",
    "event-contract": "Версия контракта",
  };
  return { label: labels[field ?? ""] ?? fallback[kind] ?? "Название", index: 0 };
}

function createEntity(
  collection: EventCollection,
  document: CanvasDocument,
): EventModel[EventCollection][number] {
  const id = createCanvasId();
  switch (collection) {
    case "servers":
      return {
        id,
        name: "Kafka",
        description: "",
        host: "",
        protocol: "kafka",
        auth: "unspecified",
      };
    case "channels":
      return {
        id,
        name: "Новый topic",
        description: "",
        address: "",
        serverIds: [],
        messageIds: [],
      };
    case "messages":
      return { id, name: "Новое событие", description: "", examples: [] };
    case "schemas":
      return { id, name: "Payload", description: "", schemaJSON: "{}" };
    case "contracts": {
      const owner = document.participants.find(
        (item) =>
          ["client", "service", "external", "other"].includes(item.kind) &&
          !document.eventModel?.contracts.some((contract) => contract.participantId === item.id),
      );
      if (!owner) throw new Error("Добавьте приложение без событийного контракта");
      return {
        id,
        name: `${owner.name || owner.id} events`,
        description: "",
        participantId: owner.id,
        version: "1.0.0",
        operations: [],
      };
    }
  }
}

function patchEntity(
  document: CanvasDocument,
  collection: EventCollection,
  id: string,
  patch: object,
): CanvasDocument {
  const model = document.eventModel;
  if (!model) return document;
  if (collection === "channels" && "messageIds" in patch) {
    const nextIds = patch.messageIds as string[];
    const used = model.contracts.flatMap((contract) =>
      contract.operations.filter(
        (operation) => operation.channelId === id && !nextIds.includes(operation.messageId),
      ),
    );
    if (used.length)
      throw new Error("Тип события используется операцией этого topic. Сначала измените операцию.");
  }
  return {
    ...document,
    eventModel: {
      ...model,
      [collection]: model[collection].map((item) =>
        item.id === id ? { ...item, ...patch } : item,
      ),
    },
  };
}

function JsonSource({
  label,
  value,
  scope,
  pointer,
  store,
  onChange,
  root,
}: {
  label: string;
  value: string;
  scope: string;
  pointer: string;
  store: FormDraftStore;
  onChange: (value: string) => void;
  root?: "schema" | "headers";
}): ReactElement {
  const draft = store.get(scope);
  const source = draft?.source ?? value;
  const error = draft?.error;
  return (
    <Textarea
      label={label}
      description="Исходный JSON сохраняется без изменения чисел."
      aria-invalid={Boolean(error)}
      error={error ? `${pointer}: ${error}` : undefined}
      minRows={5}
      autosize
      value={source}
      styles={{ input: { fontFamily: "monospace" } }}
      onChange={(event) => {
        const next = event.currentTarget.value;
        let nextError = "";
        try {
          if (new TextEncoder().encode(next).length > 262144) throw new Error("Максимум 256 КиБ");
          const parsed: unknown = JSON.parse(next);
          if (
            root === "schema" &&
            !(
              typeof parsed === "boolean" ||
              (parsed !== null && typeof parsed === "object" && !Array.isArray(parsed))
            )
          )
            throw new Error("Корень схемы должен быть объектом или boolean");
          if (
            root === "headers" &&
            !(parsed !== null && typeof parsed === "object" && !Array.isArray(parsed))
          )
            throw new Error("Headers должны быть JSON-объектом");
        } catch (cause) {
          nextError = cause instanceof Error ? cause.message : "Некорректный JSON";
        }
        if (nextError) store.set(scope, { source: next, propertySource: "", error: nextError });
        else {
          onChange(next);
          store.remove(scope);
        }
      }}
    />
  );
}

export function EventModelEditor({
  opened,
  document,
  onChange,
  onClose,
  formStore,
  arrowId,
  target,
}: {
  opened: boolean;
  document: CanvasDocument;
  onChange: (document: CanvasDocument) => void;
  onClose: () => void;
  formStore: FormDraftStore;
  arrowId?: string;
  target?: { kind: string; id: string; pointer?: string } | null;
}): ReactElement {
  const [collection, setCollection] = useState<EventCollection>(() =>
    target ? (targetCollections[target.kind] ?? "channels") : "channels",
  );
  const [selected, setSelected] = useState(() => target?.id ?? "");
  const [guidedChannel, setGuidedChannel] = useState("");
  const [guidedMessage, setGuidedMessage] = useState("");
  const [error, setError] = useState("");
  const model = document.eventModel;
  const arrow = document.messages.find((item) => item.id === arrowId && item.kind === "event");
  const currentCollection = collection;
  const item = model?.[currentCollection].find((candidate) => candidate.id === selected);
  const channel =
    item && currentCollection === "channels"
      ? model?.channels.find((candidate) => candidate.id === item.id)
      : undefined;
  const server =
    item && currentCollection === "servers"
      ? model?.servers.find((candidate) => candidate.id === item.id)
      : undefined;
  const eventMessage =
    item && currentCollection === "messages"
      ? model?.messages.find((candidate) => candidate.id === item.id)
      : undefined;
  const schema =
    item && currentCollection === "schemas"
      ? model?.schemas.find((candidate) => candidate.id === item.id)
      : undefined;
  const contract =
    item && currentCollection === "contracts"
      ? model?.contracts.find((candidate) => candidate.id === item.id)
      : undefined;
  useEffect(() => {
    if (!opened || !target) return;
    const timer = window.setTimeout(() => {
      const dialog = Array.from(
        window.document.querySelectorAll<HTMLElement>('[role="dialog"]'),
      ).find((element) => element.querySelector("h2")?.textContent === "События Kafka и AsyncAPI");
      if (!dialog) return;
      const { label, index } = diagnosticControl(target.kind, target.pointer);
      const labels = Array.from(dialog.querySelectorAll<HTMLLabelElement>("label")).filter(
        (element) => element.textContent?.trim() === label,
      );
      const chosen = labels[index] ?? labels[0];
      const control = chosen?.htmlFor ? window.document.getElementById(chosen.htmlFor) : null;
      if (control instanceof HTMLElement) {
        control.focus();
        control.scrollIntoView?.({ block: "nearest" });
      }
    }, 100);
    return () => window.clearTimeout(timer);
  }, [opened, target]);

  function run(action: () => CanvasDocument): void {
    try {
      onChange(action());
      setError("");
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Не удалось изменить событие");
    }
  }
  function patch(patchValue: object): void {
    if (item) run(() => patchEntity(document, currentCollection, item.id, patchValue));
  }
  function add(collectionToAdd: EventCollection): void {
    try {
      const upgraded = createEventModel(document);
      const created = createEntity(collectionToAdd, upgraded);
      const nextModel = upgraded.eventModel!;
      onChange({
        ...upgraded,
        eventModel: { ...nextModel, [collectionToAdd]: [...nextModel[collectionToAdd], created] },
      });
      setCollection(collectionToAdd);
      setSelected(created.id);
      setError("");
      if (collectionToAdd === "channels") setGuidedChannel(created.id);
      if (collectionToAdd === "messages") setGuidedMessage(created.id);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Не удалось добавить объект");
    }
  }
  function patchOperation(id: string, patchValue: object): void {
    if (!contract || !model) return;
    const operation = contract.operations.find((value) => value.id === id);
    if (!operation) return;
    if (
      ("action" in patchValue || "channelId" in patchValue || "messageId" in patchValue) &&
      document.messages.some((message) =>
        message.eventBindings?.some(
          (binding) => binding.contractId === contract.id && binding.operationId === id,
        ),
      )
    ) {
      setError("Сначала снимите привязку операции со стрелки");
      return;
    }
    const nextOperation = { ...operation, ...patchValue };
    if (
      contract.operations.some(
        (value) =>
          value.id !== id &&
          value.action === nextOperation.action &&
          value.channelId === nextOperation.channelId &&
          value.messageId === nextOperation.messageId,
      )
    ) {
      setError("Операция с такой ролью, topic и типом события уже есть");
      return;
    }
    run(() => {
      const targetChannel = model.channels.find((value) => value.id === nextOperation.channelId);
      if (!targetChannel || !model.messages.some((value) => value.id === nextOperation.messageId))
        throw new Error("Выберите существующий topic и тип события");
      const nextDocument = patchEntity(document, "contracts", contract.id, {
        operations: contract.operations.map((value) => (value.id === id ? nextOperation : value)),
      });
      return targetChannel.messageIds.includes(nextOperation.messageId)
        ? nextDocument
        : {
            ...nextDocument,
            eventModel: {
              ...nextDocument.eventModel!,
              channels: model.channels.map((value) =>
                value.id === targetChannel.id
                  ? { ...value, messageIds: [...value.messageIds, nextOperation.messageId] }
                  : value,
              ),
            },
          };
    });
  }
  function remove(): void {
    if (!item) return;
    try {
      const next = removeEventEntity(document, currentCollection, item.id);
      onChange(next);
      formStore.removeTree(
        `/event-${currentCollection === "messages" ? "message" : currentCollection === "schemas" ? "schema" : currentCollection === "channels" ? "channel" : currentCollection === "servers" ? "server" : "contract"}/${item.id}`,
      );
      setSelected("");
      setError("");
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Не удалось удалить объект");
    }
  }
  const schemaChoices = [
    emptyOption,
    ...(model?.schemas ?? []).map((value) => ({ value: value.id, label: value.name || value.id })),
  ];
  const messageChoices = [
    emptyOption,
    ...(model?.messages ?? []).map((value) => ({ value: value.id, label: value.name || value.id })),
  ];
  const channelChoices = [
    emptyOption,
    ...(model?.channels ?? []).map((value) => ({
      value: value.id,
      label: `${value.name || value.id} · ${value.address || "без адреса"}`,
    })),
  ];
  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title="События Kafka и AsyncAPI"
      size="xl"
      trapFocus
      returnFocus
    >
      <Stack gap="md">
        {error ? (
          <Alert color="red" role="alert">
            {error}
          </Alert>
        ) : null}
        {arrow ? (
          <Stack gap="xs" p="sm" style={{ border: "1px solid var(--mocker-border)" }}>
            <Text fw={600}>Описать событие · {arrow.label || arrow.id}</Text>
            <Text size="sm">
              Выберите общий topic и тип события. Для каждой стороны приложение получает свою
              операцию.
            </Text>
            <Group grow>
              <NativeSelect
                label="Общий topic"
                value={guidedChannel}
                data={channelChoices}
                onChange={(event) => setGuidedChannel(event.currentTarget.value)}
              />
              <NativeSelect
                label="Общий тип события"
                value={guidedMessage}
                data={messageChoices}
                onChange={(event) => setGuidedMessage(event.currentTarget.value)}
              />
            </Group>
            <Group>
              <Button variant="light" onClick={() => add("channels")}>
                Создать topic
              </Button>
              <Button variant="light" onClick={() => add("messages")}>
                Создать тип события
              </Button>
            </Group>
            <Group>
              {(["send", "receive"] as const).map((action) => {
                const participantId = action === "send" ? arrow.fromId : arrow.toId;
                const participant = document.participants.find(
                  (value) => value.id === participantId,
                );
                const existing = arrow.eventBindings?.find(
                  (binding) =>
                    resolveEventOperation(document, binding)?.operation.action === action,
                );
                return (
                  <Button
                    key={action}
                    variant="default"
                    disabled={
                      !guidedChannel ||
                      !guidedMessage ||
                      participant?.kind === "queue" ||
                      Boolean(existing)
                    }
                    onClick={() => {
                      const replace =
                        Boolean(arrow.operation) &&
                        window.confirm("Заменить HTTP-привязку событийной операцией?");
                      if (arrow.operation && !replace) return;
                      run(() =>
                        describeArrowSide(
                          document,
                          arrow.id,
                          action,
                          guidedChannel,
                          guidedMessage,
                          replace,
                        ),
                      );
                    }}
                  >
                    {existing
                      ? `${action}: связано`
                      : `${action === "send" ? "Отправляет" : "Получает"} · ${participant?.name || participantId}`}
                  </Button>
                );
              })}
            </Group>
            {arrow.eventBindings?.length ? (
              <Stack gap={4}>
                {arrow.eventBindings.map((binding) => {
                  const resolved = resolveEventOperation(document, binding);
                  return (
                    <Group
                      key={`${binding.contractId}/${binding.operationId}`}
                      justify="space-between"
                    >
                      <Text size="sm">
                        {resolved
                          ? `${resolved.operation.action} · ${resolved.contract.name} · ${model?.channels.find((value) => value.id === resolved.operation.channelId)?.address || "topic не указан"}`
                          : "Операция недоступна"}
                      </Text>
                      <Button
                        size="xs"
                        variant="subtle"
                        onClick={() =>
                          run(() => ({
                            ...document,
                            messages: document.messages.map((message) => {
                              if (message.id !== arrow.id) return message;
                              const remaining =
                                message.eventBindings?.filter(
                                  (value) =>
                                    value.contractId !== binding.contractId ||
                                    value.operationId !== binding.operationId,
                                ) ?? [];
                              return {
                                ...message,
                                eventBindings: remaining.length ? remaining : undefined,
                              };
                            }),
                          }))
                        }
                      >
                        Снять привязку
                      </Button>
                    </Group>
                  );
                })}
              </Stack>
            ) : null}
          </Stack>
        ) : null}
        <Divider label="Общая модель сценария" />
        <Group grow>
          <NativeSelect
            label="Раздел"
            value={currentCollection}
            data={collections.map((value) => ({ value, label: collectionLabels[value] }))}
            onChange={(event) => {
              setCollection(event.currentTarget.value as EventCollection);
              setSelected("");
            }}
          />
          <NativeSelect
            label="Объект"
            value={item?.id ?? ""}
            data={[
              emptyOption,
              ...(model?.[currentCollection] ?? []).map((value) => ({
                value: value.id,
                label: value.name || value.id,
              })),
            ]}
            onChange={(event) => setSelected(event.currentTarget.value)}
          />
        </Group>
        <Group>
          <Button onClick={() => add(currentCollection)}>
            Добавить · {collectionLabels[currentCollection]}
          </Button>
          {item ? (
            <Button color="red" variant="subtle" onClick={remove}>
              Удалить объект
            </Button>
          ) : null}
        </Group>
        {item ? (
          <Stack gap="sm" key={`${currentCollection}:${item.id}`}>
            <Text size="xs" c="dimmed">
              ID: {item.id}
            </Text>
            <TextInput
              label="Название"
              value={item.name}
              onChange={(event) => patch({ name: event.currentTarget.value })}
            />
            <Textarea
              label="Описание"
              value={item.description}
              onChange={(event) => patch({ description: event.currentTarget.value })}
              minRows={2}
            />
            {server ? (
              <>
                <TextInput
                  label="Kafka host:port"
                  value={server.host}
                  onChange={(event) => patch({ host: event.currentTarget.value })}
                />
                <NativeSelect
                  label="Протокол"
                  value={server.protocol}
                  data={[
                    { value: "kafka", label: "Kafka" },
                    { value: "kafka-secure", label: "Kafka + TLS" },
                  ]}
                  onChange={(event) => patch({ protocol: event.currentTarget.value })}
                />
                <NativeSelect
                  label="Аутентификация"
                  value={server.auth}
                  data={["unspecified", "none", "plain", "scramSha256", "scramSha512"]}
                  onChange={(event) => patch({ auth: event.currentTarget.value })}
                />
              </>
            ) : null}
            {channel ? (
              <>
                <TextInput
                  label="Адрес topic"
                  value={channel.address}
                  onChange={(event) => patch({ address: event.currentTarget.value })}
                />
                <TextInput
                  label="Свойство discriminator"
                  value={channel.discriminatorProperty ?? ""}
                  onChange={(event) =>
                    patch({ discriminatorProperty: event.currentTarget.value || undefined })
                  }
                />
                <NumberInput
                  label="Partitions"
                  value={channel.kafka?.partitions ?? ""}
                  min={1}
                  max={2147483647}
                  allowDecimal={false}
                  onChange={(value) =>
                    patch({
                      kafka: {
                        ...channel.kafka,
                        partitions: typeof value === "number" ? value : undefined,
                      },
                    })
                  }
                />
                <NumberInput
                  label="Replicas"
                  value={channel.kafka?.replicas ?? ""}
                  min={1}
                  max={2147483647}
                  allowDecimal={false}
                  onChange={(value) =>
                    patch({
                      kafka: {
                        ...channel.kafka,
                        replicas: typeof value === "number" ? value : undefined,
                      },
                    })
                  }
                />
                <Text fw={600} size="sm">
                  Доступные серверы
                </Text>
                {model?.servers.map((value) => (
                  <Checkbox
                    key={value.id}
                    label={value.name || value.id}
                    checked={channel.serverIds.includes(value.id)}
                    onChange={(event) =>
                      patch({
                        serverIds: event.currentTarget.checked
                          ? [...channel.serverIds, value.id]
                          : channel.serverIds.filter((id) => id !== value.id),
                      })
                    }
                  />
                ))}
                <Text fw={600} size="sm">
                  Типы сообщений на topic
                </Text>
                {model?.messages.map((value) => (
                  <Checkbox
                    key={value.id}
                    label={value.name || value.id}
                    checked={channel.messageIds.includes(value.id)}
                    onChange={(event) =>
                      patch({
                        messageIds: event.currentTarget.checked
                          ? [...channel.messageIds, value.id]
                          : channel.messageIds.filter((id) => id !== value.id),
                      })
                    }
                  />
                ))}
              </>
            ) : null}
            {schema ? (
              <>
                <JsonSource
                  label="JSON Schema Draft 07"
                  value={schema.schemaJSON}
                  scope={`/event-schema/${schema.id}/schemaJSON`}
                  pointer={`/eventModel/schemas/${model?.schemas.findIndex((value) => value.id === schema.id)}/schemaJSON`}
                  store={formStore}
                  root="schema"
                  onChange={(value) => patch({ schemaJSON: value })}
                />
                <Text size="sm">
                  Зависимые типы и контракты:{" "}
                  {eventDependents(document, "schemas", schema.id).join(", ") || "нет"}
                </Text>
              </>
            ) : null}
            {eventMessage ? (
              <>
                {(["payloadSchemaId", "headersSchemaId", "keySchemaId"] as const).map((field) => (
                  <NativeSelect
                    key={field}
                    label={
                      {
                        payloadSchemaId: "Схема payload",
                        headersSchemaId: "Схема headers",
                        keySchemaId: "Схема Kafka key",
                      }[field]
                    }
                    value={eventMessage[field] ?? ""}
                    data={schemaChoices}
                    onChange={(event) => patch({ [field]: event.currentTarget.value || undefined })}
                  />
                ))}
                <Text fw={600}>Примеры</Text>
                {eventMessage.examples.map((example, index) => (
                  <Stack
                    key={index}
                    gap="xs"
                    p="xs"
                    style={{ border: "1px solid var(--mocker-border)" }}
                  >
                    <Group grow>
                      <TextInput
                        label={`Имя примера ${index + 1}`}
                        value={example.name}
                        onChange={(event) =>
                          patch({
                            examples: eventMessage.examples.map((value, i) =>
                              i === index ? { ...value, name: event.currentTarget.value } : value,
                            ),
                          })
                        }
                      />
                      <Button
                        color="red"
                        variant="subtle"
                        onClick={() => {
                          const base = `/event-message/${eventMessage.id}/examples`;
                          formStore.removeTree(`${base}/${index}`);
                          for (let next = index + 1; next < eventMessage.examples.length; next++)
                            formStore.moveTree(`${base}/${next}`, `${base}/${next - 1}`);
                          patch({ examples: eventMessage.examples.filter((_, i) => i !== index) });
                        }}
                      >
                        Удалить пример
                      </Button>
                    </Group>
                    <JsonSource
                      label={`Payload JSON · пример ${index + 1}`}
                      value={example.payloadJSON}
                      scope={`/event-message/${eventMessage.id}/examples/${index}/payloadJSON`}
                      pointer={`/eventModel/messages/${model?.messages.findIndex((value) => value.id === eventMessage.id)}/examples/${index}/payloadJSON`}
                      store={formStore}
                      onChange={(value) =>
                        patch({
                          examples: eventMessage.examples.map((entry, i) =>
                            i === index ? { ...entry, payloadJSON: value } : entry,
                          ),
                        })
                      }
                    />
                    <JsonSource
                      label={`Headers JSON · пример ${index + 1}`}
                      value={example.headersJSON ?? "{}"}
                      scope={`/event-message/${eventMessage.id}/examples/${index}/headersJSON`}
                      pointer={`/eventModel/messages/${model?.messages.findIndex((value) => value.id === eventMessage.id)}/examples/${index}/headersJSON`}
                      store={formStore}
                      root="headers"
                      onChange={(value) =>
                        patch({
                          examples: eventMessage.examples.map((entry, i) =>
                            i === index ? { ...entry, headersJSON: value } : entry,
                          ),
                        })
                      }
                    />
                  </Stack>
                ))}
                <Button
                  variant="light"
                  disabled={eventMessage.examples.length >= 20}
                  onClick={() =>
                    patch({
                      examples: [...eventMessage.examples, { name: "Пример", payloadJSON: "{}" }],
                    })
                  }
                >
                  Добавить пример
                </Button>
              </>
            ) : null}
            {contract ? (
              <>
                <NativeSelect
                  label="Владелец-приложение"
                  value={contract.participantId}
                  data={document.participants
                    .filter((value) =>
                      ["client", "service", "external", "other"].includes(value.kind),
                    )
                    .map((value) => ({ value: value.id, label: value.name || value.id }))}
                  onChange={(event) => {
                    if (
                      document.eventModel?.contracts.some(
                        (value) =>
                          value.id !== contract.id &&
                          value.participantId === event.currentTarget.value,
                      )
                    ) {
                      setError("У приложения уже есть событийный контракт");
                      return;
                    }
                    if (
                      document.messages.some((message) =>
                        message.eventBindings?.some(
                          (binding) => binding.contractId === contract.id,
                        ),
                      )
                    ) {
                      setError("Сначала снимите привязки стрелок этого контракта");
                      return;
                    }
                    patch({ participantId: event.currentTarget.value });
                  }}
                />
                <TextInput
                  label="Версия контракта"
                  value={contract.version}
                  onChange={(event) => patch({ version: event.currentTarget.value })}
                />
                <Text fw={600}>Операции</Text>
                {contract.operations.map((operation) => (
                  <Stack
                    key={operation.id}
                    gap="xs"
                    p="xs"
                    style={{ border: "1px solid var(--mocker-border)" }}
                  >
                    <Group justify="space-between">
                      <Text size="xs">ID: {operation.id}</Text>
                      <Button
                        size="xs"
                        color="red"
                        variant="subtle"
                        onClick={() =>
                          run(() => removeEventOperation(document, contract.id, operation.id))
                        }
                      >
                        Удалить операцию
                      </Button>
                    </Group>
                    <TextInput
                      label="Название операции"
                      value={operation.name}
                      onChange={(event) =>
                        patchOperation(operation.id, { name: event.currentTarget.value })
                      }
                    />
                    <Textarea
                      label="Описание операции"
                      value={operation.description}
                      onChange={(event) =>
                        patchOperation(operation.id, { description: event.currentTarget.value })
                      }
                    />
                    <NativeSelect
                      label="Роль"
                      value={operation.action}
                      data={[
                        { value: "send", label: "send · отправляет" },
                        { value: "receive", label: "receive · получает" },
                      ]}
                      onChange={(event) => {
                        if (
                          document.messages.some((message) =>
                            message.eventBindings?.some(
                              (binding) =>
                                binding.contractId === contract.id &&
                                binding.operationId === operation.id,
                            ),
                          )
                        ) {
                          setError("Сначала снимите привязку операции со стрелки");
                          return;
                        }
                        patchOperation(operation.id, {
                          action: event.currentTarget.value,
                          kafka: undefined,
                        });
                      }}
                    />
                    <NativeSelect
                      label="Topic операции"
                      value={operation.channelId}
                      data={channelChoices}
                      onChange={(event) =>
                        patchOperation(operation.id, { channelId: event.currentTarget.value })
                      }
                    />
                    <NativeSelect
                      label="Тип события операции"
                      value={operation.messageId}
                      data={messageChoices}
                      onChange={(event) =>
                        patchOperation(operation.id, { messageId: event.currentTarget.value })
                      }
                    />
                    {operation.action === "receive" ? (
                      <>
                        <TextInput
                          label="Consumer group ID"
                          value={operation.kafka?.groupId ?? ""}
                          onChange={(event) =>
                            patchOperation(operation.id, {
                              kafka: {
                                ...operation.kafka,
                                groupId: event.currentTarget.value || undefined,
                              },
                            })
                          }
                        />
                        <TextInput
                          label="Client ID"
                          value={operation.kafka?.clientId ?? ""}
                          onChange={(event) =>
                            patchOperation(operation.id, {
                              kafka: {
                                ...operation.kafka,
                                clientId: event.currentTarget.value || undefined,
                              },
                            })
                          }
                        />
                      </>
                    ) : null}
                  </Stack>
                ))}
                <Button
                  variant="light"
                  onClick={() => {
                    if (!model?.channels.length || !model.messages.length) {
                      setError("Сначала добавьте topic и тип события");
                      return;
                    }
                    const initialChannel = model.channels[0]!;
                    const initialMessage =
                      model.messages.find((value) =>
                        initialChannel.messageIds.includes(value.id),
                      ) ?? model.messages[0]!;
                    const candidate = {
                      id: createCanvasId(),
                      name: "Новая операция",
                      description: "",
                      action: "send" as const,
                      channelId: initialChannel.id,
                      messageId: initialMessage.id,
                    };
                    if (
                      contract.operations.some(
                        (value) =>
                          value.action === candidate.action &&
                          value.channelId === candidate.channelId &&
                          value.messageId === candidate.messageId,
                      )
                    ) {
                      setError(
                        "Операция send для этого topic и типа события уже есть. Измените существующую или выберите другой topic.",
                      );
                      return;
                    }
                    const next = patchEntity(document, "contracts", contract.id, {
                      operations: [...contract.operations, candidate],
                    });
                    onChange(
                      initialChannel.messageIds.includes(initialMessage.id)
                        ? next
                        : {
                            ...next,
                            eventModel: {
                              ...next.eventModel!,
                              channels: model.channels.map((value) =>
                                value.id === initialChannel.id
                                  ? {
                                      ...value,
                                      messageIds: [...value.messageIds, initialMessage.id],
                                    }
                                  : value,
                              ),
                            },
                          },
                    );
                  }}
                >
                  Добавить операцию
                </Button>
              </>
            ) : null}
          </Stack>
        ) : (
          <Text size="sm" c="dimmed">
            Выберите объект или добавьте новый.
          </Text>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            Закрыть
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}
