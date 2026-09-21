import { useMemo, useState, type ReactElement } from "react";
import {
  Alert,
  Badge,
  Button,
  Checkbox,
  Group,
  Modal,
  NativeSelect,
  Stack,
  Text,
  TextInput,
} from "@mantine/core";
import type { DesignScenarioCommand } from "@/api/generated/schemas";
import { describeApiFailureDetailed } from "@/api/errors";
import { createCanvasId } from "./canvasId";
import {
  buildCanvasContract,
  previewCanvasContract,
  type ConversionRow,
} from "./canvasContractConversion";
import type { CanvasDocument } from "./types";

const METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE"];

export function CanvasContractConversionModal({
  document,
  expectedVersion,
  onClose,
  onCreate,
}: {
  document: CanvasDocument;
  expectedVersion: number;
  onClose: () => void;
  onCreate: (commands: DesignScenarioCommand[], expectedVersion: number) => Promise<void>;
}): ReactElement {
  const [preview] = useState(() => previewCanvasContract(document));
  const [rows, setRows] = useState(preview.rows);
  const [name, setName] = useState(`${document.title} API`);
  const [contractId] = useState(createCanvasId);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const result = useMemo(
    () => buildCanvasContract(document, rows, name, contractId),
    [document, rows, name, contractId],
  );
  const selected = rows.filter((row) => row.included);
  const operationCount = new Set(selected.map((row) => `${row.method} ${row.path}`)).size;

  function updateRow(id: string, patch: Partial<ConversionRow>): void {
    setRows((current) => current.map((row) => (row.id === id ? { ...row, ...patch } : row)));
    setError("");
  }

  async function create(): Promise<void> {
    if (pending || !result.contract || result.errors.length > 0) return;
    setPending(true);
    setError("");
    const contract = result.contract;
    const commands: DesignScenarioCommand[] = [
      {
        type: "create_contract",
        contract: {
          id: contract.id,
          name: contract.name,
          document: contract.document,
          mode: "copy",
        },
      },
      ...result.bindings.map(({ messageId, operationKey }) => ({
        type: "bind_operation" as const,
        messageId,
        contractId: contract.id,
        operationKey,
      })),
      { type: "materialize_contract", contractId: contract.id },
    ];
    try {
      await onCreate(commands, expectedVersion);
      onClose();
    } catch (cause) {
      setError(describeApiFailureDetailed(cause));
    } finally {
      setPending(false);
    }
  }

  return (
    <Modal
      opened
      title="Контракт по всей схеме"
      size={920}
      onClose={pending ? () => {} : onClose}
      closeOnEscape={!pending}
      closeOnClickOutside={!pending}
      closeButtonProps={{ "aria-label": "Закрыть предпросмотр", disabled: pending }}
    >
      <Stack gap="md">
        <Text size="sm" c="dimmed">
          Выберите HTTP-вызовы и уточните недостающие поля. Будет создан API-проект с draft mock, а
          выбранные сообщения получат ссылки на его операции. Повторные вызовы объединяются.
        </Text>
        <TextInput
          label="Название API"
          value={name}
          disabled={pending}
          onChange={(event) => {
            setName(event.currentTarget.value);
            setError("");
          }}
        />
        <Group justify="space-between">
          <Text size="sm" fw={600}>
            Операций: {operationCount} · Вызовов:{" "}
            {selected.reduce((count, row) => count + row.messageIds.length, 0)}
          </Text>
          <Text size="xs" c="dimmed">
            Снимок сценария · v{expectedVersion}
          </Text>
        </Group>
        {rows.length === 0 ? (
          <Text c="dimmed">На схеме нет HTTP-вызовов для создания контракта.</Text>
        ) : null}
        {rows.map((row) => (
          <Stack
            key={row.id}
            gap="xs"
            p="sm"
            style={{
              border: "1px solid var(--mocker-border)",
              borderRadius: "var(--mantine-radius-sm)",
            }}
          >
            <Group justify="space-between" wrap="nowrap">
              <Checkbox
                checked={row.included}
                disabled={pending}
                label={row.label || "Без названия"}
                aria-label={`Включить: ${row.label || "Без названия"}`}
                onChange={(event) => updateRow(row.id, { included: event.currentTarget.checked })}
              />
              <Group gap="xs" wrap="nowrap">
                <Text size="xs" c="dimmed">
                  {row.targetName}
                </Text>
                <Badge variant="light">Вызовов: {row.messageIds.length}</Badge>
              </Group>
            </Group>
            <Group align="flex-start" wrap="nowrap">
              <NativeSelect
                label="Метод"
                w={130}
                value={row.method}
                disabled={pending || !row.included}
                data={[
                  { value: "", label: "Выберите" },
                  ...METHODS.map((method) => ({ value: method, label: method.toUpperCase() })),
                ]}
                onChange={(event) => updateRow(row.id, { method: event.currentTarget.value })}
              />
              <TextInput
                label="Путь"
                style={{ flex: 1 }}
                placeholder="/users/{id}"
                value={row.path}
                disabled={pending || !row.included}
                onChange={(event) => updateRow(row.id, { path: event.currentTarget.value })}
              />
            </Group>
            {row.existingStatuses.length > 0 ? (
              <Text size="xs" c="dimmed">
                Ответы в контракте: {row.existingStatuses.join(", ")}
              </Text>
            ) : null}
            {row.responses.map((response) => (
              <Group key={response.messageId} wrap="nowrap" align="center">
                <Text size="sm" style={{ flex: 1 }}>
                  Ответ: {response.label || "Без названия"}
                </Text>
                <TextInput
                  aria-label={`HTTP-статус: ${response.label || "Без названия"}`}
                  placeholder="200"
                  w={130}
                  value={response.status}
                  disabled={pending || !row.included}
                  onChange={(event) =>
                    updateRow(row.id, {
                      responses: row.responses.map((item) =>
                        item.messageId === response.messageId
                          ? { ...item, status: event.currentTarget.value }
                          : item,
                      ),
                    })
                  }
                />
              </Group>
            ))}
            {row.included
              ? result.errors
                  .filter((issue) => issue.rowId === row.id)
                  .map((issue, index) => (
                    <Text key={index} size="sm" c="red">
                      {issue.message}
                    </Text>
                  ))
              : null}
          </Stack>
        ))}
        {preview.skippedCount > 0 ? (
          <Text size="xs" c="dimmed">
            Сообщений вне HTTP-контракта: {preview.skippedCount}. Внутренние шаги и обращения к БД
            остаются на схеме.
          </Text>
        ) : null}
        {result.warnings.length > 0 ? (
          <Alert color="yellow" role="note" title="Проверьте перед созданием">
            {result.warnings.map((issue, index) => (
              <Text key={index} size="sm">
                {issue.message}
              </Text>
            ))}
          </Alert>
        ) : null}
        {result.errors
          .filter((issue) => !issue.rowId)
          .map((issue, index) => (
            <Text key={index} size="sm" c="red">
              {issue.message}
            </Text>
          ))}
        {error ? (
          <Alert color="red" role="alert">
            {error}
          </Alert>
        ) : null}
        <Group justify="flex-end">
          <Button variant="default" disabled={pending} onClick={onClose}>
            Отмена
          </Button>
          <Button
            loading={pending}
            disabled={!result.contract || result.errors.length > 0}
            onClick={() => void create()}
          >
            Создать API-проект
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}
