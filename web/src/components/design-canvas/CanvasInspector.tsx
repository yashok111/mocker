import { useMemo, useState, type ReactElement } from "react";
import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Divider,
  Group,
  NativeSelect,
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
import { addLocalOperation, OPERATION_KEY, resolveOperation, updateMessage } from "./canvasModel";
import { scopeFormDrafts } from "./useCanvasDraft";
import { CanvasColorInput } from "./CanvasColorInput";
import { messageLabels, participantLabels } from "./labels";
import type {
  CanvasDocument,
  CanvasFragment,
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
  formStore: FormDraftStore;
}

export function CanvasInspector(props: Props): ReactElement {
  const { document, selection, onChange, onClose, onDelete, onMove } = props;
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
              onChange={(event) =>
                onChange({
                  ...document,
                  participants: document.participants.map((item) =>
                    item.id === participant.id
                      ? { ...item, kind: event.currentTarget.value as ParticipantKind }
                      : item,
                  ),
                })
              }
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
          <FragmentInspector document={document} fragment={fragment} onChange={onChange} />
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

function orderedFragmentBounds(
  document: CanvasDocument,
  fragment: CanvasFragment,
): { first: string; last: string } {
  const fromIndex = document.messages.findIndex((message) => message.id === fragment.fromMessageId);
  const toIndex = document.messages.findIndex((message) => message.id === fragment.toMessageId);
  return fromIndex <= toIndex
    ? { first: fragment.fromMessageId, last: fragment.toMessageId }
    : { first: fragment.toMessageId, last: fragment.fromMessageId };
}

function FragmentInspector({
  document,
  fragment,
  onChange,
}: {
  document: CanvasDocument;
  fragment: CanvasFragment;
  onChange: (document: CanvasDocument) => void;
}): ReactElement {
  const bounds = orderedFragmentBounds(document, fragment);
  const patch = (changes: Partial<CanvasFragment>) =>
    onChange({
      ...document,
      fragments: document.fragments.map((item) =>
        item.id === fragment.id
          ? { ...item, fromMessageId: bounds.first, toMessageId: bounds.last, ...changes }
          : item,
      ),
    });
  const firstIndex = document.messages.findIndex((message) => message.id === bounds.first);
  const lastIndex = document.messages.findIndex((message) => message.id === bounds.last);

  return (
    <>
      <NativeSelect
        label="Тип блока"
        value={fragment.kind}
        data={[
          { value: "opt", label: "Условие (opt)" },
          { value: "loop", label: "Цикл (loop)" },
        ]}
        onChange={(event) => patch({ kind: event.currentTarget.value as "opt" | "loop" })}
      />
      <Textarea
        label="Условие блока"
        value={fragment.label}
        onChange={(event) => patch({ label: event.currentTarget.value })}
      />
      <NativeSelect
        label="Первый шаг блока"
        value={bounds.first}
        data={document.messages.map((item, index) => ({
          value: item.id,
          label: `${index + 1}. ${item.label}`,
        }))}
        onChange={(event) => {
          const first = event.currentTarget.value;
          const selectedIndex = document.messages.findIndex((message) => message.id === first);
          patch({
            fromMessageId: first,
            toMessageId: selectedIndex > lastIndex ? first : bounds.last,
          });
        }}
      />
      <NativeSelect
        label="Последний шаг блока"
        value={bounds.last}
        data={document.messages.slice(Math.max(0, firstIndex)).map((item) => ({
          value: item.id,
          label: item.label,
        }))}
        onChange={(event) => patch({ toMessageId: event.currentTarget.value })}
      />
    </>
  );
}

function MessageInspector({
  document,
  message,
  onChange,
  onMove,
  onImportApi,
  formStore,
}: Props & { message: CanvasMessage }): ReactElement {
  const [schema, setSchema] = useState("");
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
  const patch = (changes: Partial<CanvasMessage>) =>
    onChange(updateMessage(document, message.id, changes));
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
            onChange(addLocalOperation(document, message.id));
          }}
        >
          Создать API для вызова
        </Button>
        <Button size="xs" variant="default" onClick={onImportApi}>
          Выбрать проект API
        </Button>
      </Group>
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
