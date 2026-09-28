import { useMemo, useState, type ReactElement } from "react";
import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Divider,
  Group,
  NativeSelect,
  NumberInput,
  Stack,
  Text,
  Textarea,
  TextInput,
} from "@mantine/core";
import {
  IconArrowDown,
  IconArrowLeft,
  IconArrowRight,
  IconArrowUp,
  IconPlus,
  IconTrash,
  IconX,
} from "@tabler/icons-react";
import { Link } from "@tanstack/react-router";
import { DocumentForm } from "../api-designer/DocumentForm";
import type { FormDraftStore } from "../api-designer/forms/formDraftStore";
import { getOperation, listOperations, listSchemas } from "../api-designer/documentModel";
import {
  addLocalOperation,
  OPERATION_KEY,
  resolveOperation,
  spaceParticipant,
  updateMessage,
} from "./canvasModel";
import { MAX_PARTICIPANT_OFFSET_X } from "./types";
import { scopeFormDrafts } from "./useCanvasDraft";
import { CanvasFragmentInspector } from "./CanvasFragmentInspector";
import { CanvasColorInput } from "./CanvasColorInput";
import { CanvasCreateOperationModal } from "./CanvasCreateOperationModal";
import { messageLabels, participantLabels } from "./labels";
import type {
  CanvasDocument,
  CanvasMessage,
  CanvasSelection,
  MessageKind,
  ParticipantKind,
} from "./types";
import classes from "./DesignCanvas.module.css";

interface Props {
  document: CanvasDocument;
  selection: CanvasSelection;
  onChange: (document: CanvasDocument) => void;
  onClose: () => void;
  onDelete: () => void;
  onMove: (offset: number) => void;
  onImportApi: () => void;
  onAddAfter?: () => void;
  onReply?: () => void;
  onDuplicate?: () => void;
  onEditLabel?: (selection: CanvasSelection) => void;
  formStore: FormDraftStore;
  onOpenEventEditor?: (messageId: string) => void;
}

export function CanvasInspector(props: Props): ReactElement {
  const { document, selection, onChange, onClose, onDelete, onMove } = props;
  const [dependencyError, setDependencyError] = useState("");
  const participant =
    selection?.kind === "participant"
      ? document.participants.find((item) => item.id === selection.id)
      : undefined;
  const message =
    selection?.kind === "message"
      ? document.messages.find((item) => item.id === selection.id)
      : undefined;
  const fragment =
    selection?.kind === "fragment"
      ? document.fragments.find((item) => item.id === selection.id)
      : undefined;
  const title = participant
    ? "Объект"
    : message
      ? "Сообщение"
      : fragment
        ? "Условный блок"
        : "Свойства";

  return (
    <section className={classes.inspector} aria-label="Свойства выбранного объекта">
      <Group justify="space-between" className={classes.inspectorHeader}>
        <Text fw={650}>{title}</Text>
        <ActionIcon variant="subtle" color="gray" aria-label="Закрыть свойства" onClick={onClose}>
          <IconX size={18} />
        </ActionIcon>
      </Group>
      <Stack className={classes.inspectorBody} gap="md">
        {dependencyError ? (
          <Alert color="red" role="alert">
            {dependencyError}
          </Alert>
        ) : null}
        {selection ? (
          <Button variant="default" onClick={() => props.onEditLabel?.(selection)}>
            Изменить подпись на диаграмме
          </Button>
        ) : null}
        {participant ? (
          <>
            <TextInput
              label="Название объекта"
              value={participant.name}
              onChange={(event) =>
                onChange({
                  ...document,
                  participants: document.participants.map((item) =>
                    item.id === participant.id
                      ? { ...item, name: event.currentTarget.value }
                      : item,
                  ),
                })
              }
            />
            <NativeSelect
              label="Тип объекта"
              value={participant.kind}
              data={Object.entries(participantLabels).map(([value, label]) => ({ value, label }))}
              onChange={(event) => {
                const kind = event.currentTarget.value as ParticipantKind;
                if (
                  document.eventModel?.contracts.some(
                    (item) => item.participantId === participant.id,
                  ) &&
                  !["client", "service", "external", "other"].includes(kind)
                ) {
                  setDependencyError(
                    "Объект владеет событийным контрактом. Сначала измените или удалите контракт.",
                  );
                  return;
                }
                setDependencyError("");
                onChange({
                  ...document,
                  participants: document.participants.map((item) =>
                    item.id === participant.id ? { ...item, kind } : item,
                  ),
                });
              }}
            />
            <Textarea
              label="Описание объекта"
              rows={3}
              value={participant.description}
              onChange={(event) =>
                onChange({
                  ...document,
                  participants: document.participants.map((item) =>
                    item.id === participant.id
                      ? { ...item, description: event.currentTarget.value }
                      : item,
                  ),
                })
              }
            />
            <CanvasColorInput
              label="Цвет карточки"
              value={participant.color}
              onChange={(color) =>
                onChange({
                  ...document,
                  participants: document.participants.map((item) =>
                    item.id === participant.id ? { ...item, color } : item,
                  ),
                })
              }
            />
            <NumberInput
              label="Дополнительный отступ слева"
              description="Раздвигает колонки; участники справа сдвигаются вместе."
              value={participant.offsetX ?? 0}
              min={0}
              max={MAX_PARTICIPANT_OFFSET_X}
              step={20}
              allowDecimal={false}
              allowNegative={false}
              suffix=" px"
              onChange={(value) => {
                if (typeof value === "number")
                  onChange(spaceParticipant(document, participant.id, value));
              }}
            />
            <Button
              variant="subtle"
              disabled={!participant.offsetX}
              onClick={() => onChange(spaceParticipant(document, participant.id, 0))}
            >
              Сбросить отступ
            </Button>
            <Group grow>
              <Button
                variant="default"
                leftSection={<IconArrowLeft size={15} />}
                onClick={() => onMove(-1)}
                disabled={document.participants[0]?.id === participant.id}
              >
                Левее
              </Button>
              <Button
                variant="default"
                rightSection={<IconArrowRight size={15} />}
                onClick={() => onMove(1)}
                disabled={document.participants.at(-1)?.id === participant.id}
              >
                Правее
              </Button>
            </Group>
          </>
        ) : null}
        {message ? <MessageInspector {...props} message={message} /> : null}
        {fragment ? (
          <CanvasFragmentInspector
            key={fragment.id}
            document={document}
            fragment={fragment}
            onChange={onChange}
            formStore={props.formStore}
          />
        ) : null}
        {participant || message || fragment ? (
          <>
            <Divider />
            <Button
              variant="subtle"
              color="red"
              leftSection={<IconTrash size={15} />}
              onClick={onDelete}
            >
              Удалить {participant ? "объект" : message ? "сообщение" : "блок"}
            </Button>
          </>
        ) : (
          <Text c="dimmed" size="sm">
            Выберите объект, сообщение или блок на диаграмме.
          </Text>
        )}
      </Stack>
    </section>
  );
}

function MessageInspector({
  document,
  message,
  onChange,
  onMove,
  onImportApi,
  onAddAfter,
  onReply,
  onDuplicate,
  formStore,
  onOpenEventEditor,
}: Props & { message: CanvasMessage }): ReactElement {
  const [schema, setSchema] = useState("");
  const [createOperation, setCreateOperation] = useState(false);
  const resolved = message.operation ? resolveOperation(document, message.operation) : null;
  const scopedStore = useMemo(
    () => scopeFormDrafts(formStore, resolved?.contract.id ?? "none"),
    [formStore, resolved?.contract.id],
  );
  const index = document.messages.findIndex((item) => item.id === message.id);
  const priorRequests = document.messages
    .slice(0, index)
    .filter(
      (item) =>
        item.kind === "request" && item.fromId === message.toId && item.toId === message.fromId,
    );
  const participants = document.participants.map((item) => ({
    value: item.id,
    label: item.name || "Без имени",
  }));
  const patch = (changes: Partial<CanvasMessage>) => {
    const hasEventBindings = Boolean(message.eventBindings?.length);
    const breaksBinding =
      hasEventBindings &&
      ((changes.kind !== undefined && changes.kind !== "event") ||
        (changes.fromId !== undefined && changes.fromId !== message.fromId) ||
        (changes.toId !== undefined && changes.toId !== message.toId) ||
        changes.operation !== undefined);
    if (
      breaksBinding &&
      !window.confirm("Изменение снимет событийные привязки этой стрелки. Продолжить?")
    )
      return;
    const next = breaksBinding ? { ...changes, eventBindings: undefined } : changes;
    onChange(updateMessage(document, message.id, next));
  };
  const operationChoices = document.contracts.flatMap((contract) =>
    listOperations(contract.document).flatMap((location) => {
      const key = getOperation(contract.document, location)?.[OPERATION_KEY];
      return typeof key !== "string"
        ? []
        : [
            {
              value: JSON.stringify([contract.id, key]),
              label: `${contract.name} · ${location.method.toUpperCase()} ${location.path}`,
            },
          ];
    }),
  );
  const schemas = resolved ? listSchemas(resolved.contract.document) : [];
  const selectedSchema = schemas.includes(schema) ? schema : "";

  return (
    <>
      <Group grow>
        <Button variant="default" onClick={onAddAfter}>
          Добавить после
        </Button>
      </Group>
      {message.kind === "event" ? (
        <>
          <Divider label="Событие Kafka" labelPosition="left" />
          <Button variant="light" onClick={() => onOpenEventEditor?.(message.id)}>
            Описать событие
          </Button>
          {(message.eventBindings ?? []).map((binding) => {
            const contract = document.eventModel?.contracts.find(
              (item) => item.id === binding.contractId,
            );
            const operation = contract?.operations.find((item) => item.id === binding.operationId);
            const topic = document.eventModel?.channels.find(
              (item) => item.id === operation?.channelId,
            );
            const eventType = document.eventModel?.messages.find(
              (item) => item.id === operation?.messageId,
            );
            return (
              <Text key={`${binding.contractId}/${binding.operationId}`} size="sm">
                {operation?.action ?? "?"} · {contract?.name ?? "Контракт недоступен"} ·{" "}
                {topic?.address || "topic не указан"} · {eventType?.name ?? "тип события не указан"}
              </Text>
            );
          })}
        </>
      ) : null}
      <Group grow>
        {message.kind === "request" ? (
          <Button variant="default" onClick={onReply}>
            Ответить
          </Button>
        ) : null}
        <Button variant="default" onClick={onDuplicate}>
          Дублировать
        </Button>
      </Group>
      <TextInput
        label="Название сообщения"
        value={message.label}
        onChange={(event) => patch({ label: event.currentTarget.value })}
      />
      <NativeSelect
        label="Тип сообщения"
        value={message.kind}
        data={Object.entries(messageLabels).map(([value, label]) => ({ value, label }))}
        onChange={(event) =>
          patch({ kind: event.currentTarget.value as MessageKind, replyToId: undefined })
        }
      />
      <Group grow align="flex-start">
        <NativeSelect
          label="Отправитель"
          value={message.fromId}
          data={participants}
          onChange={(event) => patch({ fromId: event.currentTarget.value, replyToId: undefined })}
        />
        <NativeSelect
          label="Получатель"
          value={message.toId}
          data={participants}
          onChange={(event) => patch({ toId: event.currentTarget.value, replyToId: undefined })}
        />
      </Group>
      {message.kind === "response" ? (
        <NativeSelect
          label="Ответ на вызов"
          value={message.replyToId ?? ""}
          data={[
            { value: "", label: "Без привязки" },
            ...priorRequests.map((item) => ({ value: item.id, label: item.label })),
          ]}
          onChange={(event) => patch({ replyToId: event.currentTarget.value || undefined })}
        />
      ) : null}
      <Textarea
        label="Описание сообщения"
        value={message.description}
        rows={2}
        onChange={(event) => patch({ description: event.currentTarget.value })}
      />
      <CanvasColorInput
        label="Цвет карточки"
        value={message.color}
        onChange={(color) => patch({ color })}
      />
      {message.kind !== "note" ? (
        <CanvasColorInput
          label="Цвет стрелки"
          value={message.arrowColor}
          onChange={(arrowColor) => patch({ arrowColor })}
        />
      ) : null}
      <Group grow>
        <Button
          variant="default"
          leftSection={<IconArrowUp size={15} />}
          disabled={index === 0}
          onClick={() => onMove(-1)}
        >
          Раньше
        </Button>
        <Button
          variant="default"
          leftSection={<IconArrowDown size={15} />}
          disabled={index === document.messages.length - 1}
          onClick={() => onMove(1)}
        >
          Позже
        </Button>
      </Group>
      <Divider label="Контракт API" labelPosition="left" />
      <NativeSelect
        label="Операция API"
        value={
          message.operation
            ? JSON.stringify([message.operation.contractId, message.operation.operationKey])
            : ""
        }
        data={[
          { value: "", label: "Описательное сообщение" },
          ...(message.operation && !resolved
            ? [
                {
                  value: JSON.stringify([
                    message.operation.contractId,
                    message.operation.operationKey,
                  ]),
                  label: "Операция недоступна",
                },
              ]
            : []),
          ...operationChoices,
        ]}
        onChange={(event) => {
          setSchema("");
          const value = event.currentTarget.value;
          const pair = value ? (JSON.parse(value) as [string, string]) : null;
          patch({ operation: pair ? { contractId: pair[0], operationKey: pair[1] } : undefined });
        }}
      />
      <Group grow>
        <Button
          size="xs"
          variant="light"
          leftSection={<IconPlus size={14} />}
          disabled={message.operation !== undefined}
          onClick={() => {
            setSchema("");
            setCreateOperation(true);
          }}
        >
          Создать API для вызова
        </Button>
        <Button size="xs" variant="default" onClick={onImportApi}>
          Выбрать проект API
        </Button>
      </Group>
      {createOperation ? (
        <CanvasCreateOperationModal
          document={document}
          label={message.label}
          onClose={() => setCreateOperation(false)}
          onCreate={(input) => {
            onChange(addLocalOperation(document, message.id, input));
            setCreateOperation(false);
          }}
        />
      ) : null}
      {message.operation && !resolved ? (
        <Alert color="yellow">Связанная операция недоступна. Выберите её заново.</Alert>
      ) : null}
      {resolved ? (
        <>
          <Group justify="space-between">
            <Badge color="gray" variant="light">
              {resolved.contract.mode === "linked" ? "Общий API" : "Локальная копия API"}
            </Badge>
            {resolved.contract.source ? (
              <Link
                to="/designs/$id"
                params={{ id: resolved.contract.source.designId }}
                target="_blank"
                rel="noreferrer"
              >
                Открыть оригинал
              </Link>
            ) : null}
          </Group>
          <Text size="xs" c="dimmed">
            {resolved.contract.mode === "linked"
              ? "Правки контракта сохраняются в общий API-проект при сохранении сценария."
              : "Правки контракта применяются ко всем сообщениям этой копии и сохраняются вместе со сценарием."}
            {resolved.contract.source
              ? ` Исходная ревизия: ${resolved.contract.source.revisionId}.`
              : ""}
          </Text>
          {schemas.length ? (
            <NativeSelect
              label="Редактируемый объект API"
              value={selectedSchema}
              onChange={(event) => setSchema(event.currentTarget.value)}
              data={[
                { value: "", label: "Операция" },
                ...schemas.map((name) => ({ value: name, label: `Схема · ${name}` })),
              ]}
            />
          ) : null}
          <DocumentForm
            document={resolved.contract.document}
            selection={
              selectedSchema
                ? { kind: "schema", name: selectedSchema }
                : { kind: "operation", ...resolved.location }
            }
            draftStore={scopedStore}
            onChange={(next) =>
              onChange({
                ...document,
                contracts: document.contracts.map((contract) =>
                  contract.id === resolved.contract.id ? { ...contract, document: next } : contract,
                ),
              })
            }
          />
        </>
      ) : null}
    </>
  );
}
