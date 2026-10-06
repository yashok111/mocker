import { useEffect, useRef, useState } from "react";
import {
  Alert,
  Button,
  Checkbox,
  Code,
  Group,
  Paper,
  Stack,
  Table,
  Text,
  Textarea,
  Title,
} from "@mantine/core";
import {
  previewBackendMaterialization,
  applyBackendMaterialization,
} from "@/api/generated/backend-projects/backend-projects";
import type {
  PreviewBackendMaterializationRequest,
  ApplyBackendMaterializationRequest,
  BackendMaterializationPreview,
  BackendMaterializationReceipt,
} from "@/api/generated/schemas";
import { ApiFailure } from "@/api/client";
import { parseBrowserSafeJson } from "@/api/preciseJson";
import { describeApiFailureDetailed } from "@/api/errors";

const template = JSON.stringify(
  {
    profileVersion: "backend-http-draft-v1",
    target: { changeProposal: { proposalId: "", proposalRevisionId: "" } },
    targetHash: "",
    sourceScope: [],
    targets: [],
    translations: [],
  },
  null,
  2,
);

export function BackendMaterialization({
  projectId,
  onDirty,
}: {
  projectId: string;
  onDirty?: (value: boolean) => void;
}) {
  const [text, setText] = useState(template);
  const [partial, setPartial] = useState(false);
  const [excluded, setExcluded] = useState("");
  const [reason, setReason] = useState("");
  const [preview, setPreview] = useState<{
    result: BackendMaterializationPreview;
    input: PreviewBackendMaterializationRequest;
  }>();
  const storageKey = `mocker-materialization-v1:${projectId}`;
  const [pending, setPending] = useState<ApplyBackendMaterializationRequest | undefined>(() => {
    try {
      const saved = localStorage.getItem(storageKey);
      return saved
        ? (parseBrowserSafeJson(saved) as ApplyBackendMaterializationRequest)
        : undefined;
    } catch {
      return undefined;
    }
  });
  const [receipt, setReceipt] = useState<BackendMaterializationReceipt>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const working = useRef(false);
  const dirty = text !== template || partial || !!reason || !!pending;
  useEffect(() => {
    onDirty?.(dirty);
    return () => onDirty?.(false);
  }, [dirty, onDirty]);
  const change = (update: () => void) => {
    update();
    setPreview(undefined);
    setReceipt(undefined);
    setError("");
  };
  async function prepare() {
    if (working.current) return;
    working.current = true;
    setBusy(true);
    setError("");
    setPreview(undefined);
    try {
      const raw: unknown = parseBrowserSafeJson(text);
      if (!raw || typeof raw !== "object" || Array.isArray(raw))
        throw new Error("Укажите объект плана");
      const input = {
        ...raw,
        partialSimulation: partial,
        excludedIds: partial ? excluded.split(/[\s,]+/).filter(Boolean) : [],
        reason,
      } as PreviewBackendMaterializationRequest;
      const response = await previewBackendMaterialization(projectId, input);
      if (response.status !== 200) throw new Error("Предпросмотр не получен");
      setPreview({ result: response.data, input });
    } catch (err) {
      setError(
        err instanceof SyntaxError
          ? "План должен быть корректным JSON"
          : describeApiFailureDetailed(err),
      );
    } finally {
      working.current = false;
      setBusy(false);
    }
  }
  async function apply() {
    if (working.current || (!pending && !preview?.result.canApply)) return;
    const request = pending ?? {
      ...preview!.input,
      candidateHash: preview!.result.candidateHash,
      idempotencyKey: crypto.randomUUID(),
    };
    // Retain exact original input and key until receipt arrives, including after
    // a lost response. Normalized preview.input is not the original copy intent.
    try {
      localStorage.setItem(storageKey, JSON.stringify(request));
    } catch {
      setError(
        "Не удалось сохранить запрос для безопасного повтора. Освободите локальное хранилище и повторите.",
      );
      return;
    }
    setPending(request);
    working.current = true;
    setBusy(true);
    setError("");
    try {
      const response = await applyBackendMaterialization(projectId, request);
      if (response.status !== 200) throw new Error("Результат применения неизвестен");
      localStorage.removeItem(storageKey);
      setReceipt(response.data);
      setPending(undefined);
      setPreview(undefined);
    } catch (err) {
      setError(describeApiFailureDetailed(err));
      if (err instanceof ApiFailure && [400, 404, 409, 413, 422].includes(err.status)) {
        localStorage.removeItem(storageKey);
        setPending(undefined);
        setPreview(undefined);
      }
    } finally {
      working.current = false;
      setBusy(false);
    }
  }
  return (
    <Paper withBorder p="md" data-testid="backend-materialization">
      <Stack>
        <Title order={2} tabIndex={-1}>
          Материализация черновиков
        </Title>
        <Text size="sm">
          Явный перевод выбранного proposal в API и HTTP-сценарии. Изменения draft mock показаны в
          плане. Публикация выполняется через review API.
        </Text>
        <Textarea
          label="План перевода: точные pins, объекты и команды владельцев"
          value={text}
          minRows={8}
          autosize
          maxRows={24}
          disabled={busy || !!pending}
          onChange={(event) => change(() => setText(event.currentTarget.value))}
        />
        <Checkbox
          label="Согласен на частичную симуляцию выбранного scope"
          checked={partial}
          disabled={busy || !!pending}
          onChange={(event) => change(() => setPartial(event.currentTarget.checked))}
        />
        {partial && (
          <Textarea
            label="Исключённые ID (через пробел или запятую)"
            value={excluded}
            disabled={busy || !!pending}
            onChange={(event) => change(() => setExcluded(event.currentTarget.value))}
          />
        )}
        <Textarea
          label="Причина перевода / исключений"
          value={reason}
          disabled={busy || !!pending}
          onChange={(event) => change(() => setReason(event.currentTarget.value))}
        />
        {error && (
          <Alert color="red" role="alert">
            {error}
          </Alert>
        )}
        {pending && (
          <Alert color="yellow">
            Результат ещё не подтверждён. Повторите тот же запрос, чтобы получить сохранённую
            квитанцию.
          </Alert>
        )}
        <Group>
          <Button
            variant="default"
            onClick={() => void prepare()}
            loading={busy && !pending}
            disabled={busy || !!pending || (partial && (!excluded.trim() || !reason.trim()))}
          >
            Предпросмотр материализации
          </Button>
          <Button
            onClick={() => void apply()}
            loading={busy && !!pending}
            disabled={busy || (!pending && !preview?.result.canApply)}
          >
            {pending ? "Повторить применение" : "Применить к черновикам"}
          </Button>
        </Group>
        {preview && (
          <Stack aria-live="polite">
            <Text fw={600}>
              {preview.result.equivalence === "partial_simulation"
                ? "Частичная симуляция"
                : "Структурная проекция"}
            </Text>
            <Table captionSide="top">
              <Table.Caption>Планируемые изменения владельцев</Table.Caption>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>Цель</Table.Th>
                  <Table.Th>Владелец</Table.Th>
                  <Table.Th>Связанные эффекты</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {preview.result.effects.map((effect) => (
                  <Table.Tr key={effect.targetKey}>
                    <Table.Td>{effect.targetKey}</Table.Td>
                    <Table.Td>{effect.kind}</Table.Td>
                    <Table.Td>
                      {effect.draftMock ? "Обновление draft mock" : "Сохранение сценария"}
                      {effect.linkedFrom.length ? ` · из ${effect.linkedFrom.join(", ")}` : ""}
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
            <Table captionSide="top">
              <Table.Caption>Покрытие выбранного scope</Table.Caption>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>ID</Table.Th>
                  <Table.Th>Статус</Table.Th>
                  <Table.Th>Причина</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {preview.result.coverage.map((row) => (
                  <Table.Tr key={row.sourceId}>
                    <Table.Td>{row.sourceId}</Table.Td>
                    <Table.Td>{row.status}</Table.Td>
                    <Table.Td>{row.reason}</Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
            {preview.result.diagnostics.map((message) => (
              <Alert key={message} color="yellow">
                {message}
              </Alert>
            ))}
            <details>
              <summary>Точные нормализованные документы и версии</summary>
              <Code block>{JSON.stringify(preview.result.input, null, 2)}</Code>
            </details>
          </Stack>
        )}
        {receipt && (
          <Alert color="green" role="status">
            Черновики сохранены. Квитанция {receipt.id}. Публикация и выполнение не запускались.
          </Alert>
        )}
      </Stack>
    </Paper>
  );
}
