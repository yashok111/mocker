import { useState, type ReactElement } from "react";
import { Button, Group, Modal, NativeSelect, Stack, Text, TextInput } from "@mantine/core";
import { getOperation, listOperations } from "../api-designer/documentModel";
import { OPERATION_KEY, type LocalOperationInput } from "./canvasModel";
import type { CanvasDocument } from "./types";

const METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE"];

export function CanvasCreateOperationModal({
  document,
  label,
  onClose,
  onCreate,
}: {
  document: CanvasDocument;
  label: string;
  onClose: () => void;
  onCreate: (input: LocalOperationInput) => void;
}): ReactElement {
  const explicit = /^\s*(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS|TRACE)\s+(\/[^\s]*)\s*$/i.exec(
    label,
  );
  const [method, setMethod] = useState(explicit?.[1]?.toUpperCase() ?? "");
  const [path, setPath] = useState(explicit?.[2] ?? "");
  const [responseStatus, setResponseStatus] = useState("default");
  const [reuse, setReuse] = useState("");
  const matches = document.contracts.flatMap((contract) =>
    listOperations(contract.document).flatMap((location) => {
      if (location.method.toUpperCase() !== method || location.path !== path.trim()) return [];
      const key = getOperation(contract.document, location)?.[OPERATION_KEY];
      return typeof key === "string" ? [{ value: contract.id, label: contract.name }] : [];
    }),
  );
  const valid =
    method !== "" &&
    path.startsWith("/") &&
    !/[\s?#]/.test(path) &&
    !/[{}]/.test(path.replace(/\{[^{} /]+\}/g, ""));

  return (
    <Modal opened onClose={onClose} title="Создать операцию API" size="md">
      <Stack gap="md">
        <Text size="sm" c="dimmed">
          Укажите HTTP-метод и путь. Тело запроса и схема ответа остаются неописанными до
          редактирования в API Designer.
        </Text>
        <NativeSelect
          label="Метод"
          value={method}
          data={[{ value: "", label: "Выберите" }, ...METHODS]}
          onChange={(event) => {
            setMethod(event.currentTarget.value);
            setReuse("");
          }}
        />
        <TextInput
          label="Путь"
          placeholder="/users/{id}"
          value={path}
          onChange={(event) => {
            setPath(event.currentTarget.value);
            setReuse("");
          }}
        />
        {path.includes("{") ? (
          <Text size="xs" c="dimmed">
            Для параметров пути предполагается тип string. Уточните его в API Designer.
          </Text>
        ) : null}
        <TextInput
          label="Статус ответа"
          description="Введите HTTP-статус или default, если статус неизвестен"
          value={responseStatus}
          onChange={(event) => setResponseStatus(event.currentTarget.value)}
        />
        {matches.length ? (
          <NativeSelect
            label="Операция уже есть"
            value={reuse}
            data={[{ value: "", label: "Создать новый контракт" }, ...matches]}
            onChange={(event) => setReuse(event.currentTarget.value)}
          />
        ) : null}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            Отмена
          </Button>
          <Button
            disabled={!valid || !/^(?:[1-5]\d{2}|[1-5]XX|default)$/.test(responseStatus)}
            onClick={() =>
              onCreate({
                method,
                path: path.trim(),
                responseStatus,
                ...(reuse ? { contractId: reuse } : {}),
              })
            }
          >
            Создать операцию
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}
