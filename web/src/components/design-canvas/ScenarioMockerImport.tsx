import { useEffect, useRef, useState } from "react";
import { Alert, Button, Checkbox, Group, Modal, Stack, Text } from "@mantine/core";
import { useNavigate } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { describeApiFailureDetailed } from "@/api/errors";
import { designScenarioKeys, useCreateDesignScenario } from "./designScenarioApi";
import { importScenarioTransfer } from "@/api/generated/design-scenarios/design-scenarios";
import type {
  ImportScenarioTransferRequest,
  ScenarioTransferResult,
} from "@/api/generated/schemas";
import { MAX_MOCKER_FILE_BYTES, parseMockerTransfer } from "./scenarioMockerFile";

export function ScenarioMockerImport() {
  const [opened, setOpened] = useState(false);
  return (
    <>
      <Button variant="default" onClick={() => setOpened(true)}>
        Импорт .mocker
      </Button>
      {opened ? <ImportDialog onClose={() => setOpened(false)} /> : null}
    </>
  );
}

function ImportDialog({ onClose }: { onClose: () => void }) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [parsed, setParsed] = useState<ReturnType<typeof parseMockerTransfer> | null>(null);
  const [relink, setRelink] = useState(false);
  const [transferring, setTransferring] = useState(false);
  const [result, setResult] = useState<ScenarioTransferResult | null>(null);
  const [error, setError] = useState("");
  const [reading, setReading] = useState(false);
  const sequence = useRef(0);
  const submitting = useRef(false);
  useEffect(
    () => () => {
      sequence.current++;
    },
    [],
  );
  const create = useCreateDesignScenario({
    onSuccess: async (response) => {
      await queryClient.invalidateQueries({ queryKey: designScenarioKeys.all });
      onClose();
      void navigate({
        to: "/design-scenarios/$id" as never,
        params: { id: response.data.scenario.id } as never,
      });
    },
    onSettled: () => {
      submitting.current = false;
    },
  });

  const busy = create.isPending || transferring;
  async function importPackage(): Promise<void> {
    if (!parsed || reading || submitting.current) return;
    submitting.current = true;
    if (parsed.single && !relink) {
      const snapshot = parsed.bundle.scenarios[0]!.revisions[0]!;
      create.mutate({ document: snapshot.document, formDrafts: snapshot.formDrafts });
      return;
    }
    setTransferring(true);
    setError("");
    try {
      const response = await importScenarioTransfer({
        bundle: parsed.bundle,
        relink,
      } as ImportScenarioTransferRequest);
      if (response.status !== 201) throw new Error("Не удалось импортировать пакет");
      setResult(response.data);
      await queryClient.invalidateQueries({ queryKey: designScenarioKeys.all });
    } catch (cause) {
      setError(describeApiFailureDetailed(cause));
    } finally {
      setTransferring(false);
      submitting.current = false;
    }
  }

  async function readFile(file?: File): Promise<void> {
    const current = ++sequence.current;
    setParsed(null);
    setError("");
    create.reset();
    setReading(false);
    if (!file) return;
    if (file.size > MAX_MOCKER_FILE_BYTES) {
      setError("Размер файла .mocker превышает 2 МБ");
      return;
    }
    setReading(true);
    try {
      const result = parseMockerTransfer(await file.text());
      if (sequence.current === current) setParsed(result);
    } catch (cause) {
      if (sequence.current === current)
        setError(cause instanceof Error ? cause.message : "Не удалось прочитать файл");
    } finally {
      if (sequence.current === current) setReading(false);
    }
  }

  return (
    <Modal
      opened
      onClose={() => {
        if (!submitting.current) onClose();
      }}
      title="Импорт сценария .mocker"
      centered
      withCloseButton={!busy}
    >
      <Stack>
        {result ? (
          <Stack>
            <Text>Создано сценариев: {result.scenarios.length}.</Text>
            <Text size="sm">
              Связей с API: {result.linkedContracts}, копий контрактов: {result.copiedContracts} (по
              всем ревизиям).
            </Text>
            {result.scenarios.map((scenario) => (
              <Button
                key={scenario.id}
                variant="default"
                onClick={() => {
                  onClose();
                  void navigate({
                    to: "/design-scenarios/$id" as never,
                    params: { id: scenario.id } as never,
                  });
                }}
              >
                Открыть {scenario.name}
              </Button>
            ))}
            <Button onClick={onClose}>Готово</Button>
          </Stack>
        ) : (
          <>
            <Text size="sm">
              Будут созданы новые сценарии. Пакет импортируется целиком вместе с историей.
            </Text>
            <label>
              Файл .mocker
              <input
                type="file"
                accept=".mocker,application/json"
                disabled={busy}
                onChange={(event) => {
                  const file = event.currentTarget.files?.[0];
                  event.currentTarget.value = "";
                  void readFile(file);
                }}
              />
            </label>
            <Text size="xs" c="dimmed">
              До 2 МБ.
            </Text>
            {reading ? <Text component="output">Читаем файл…</Text> : null}
            {parsed ? (
              <Stack gap={4}>
                <Text>
                  Сценариев: {parsed.bundle.scenarios.length}, ревизий:{" "}
                  {parsed.bundle.scenarios.reduce(
                    (count, scenario) => count + scenario.revisions.length,
                    0,
                  )}
                  .
                </Text>
                {parsed.bundle.scenarios.map((scenario, index) => (
                  <Text key={index} fw={600}>
                    {scenario.revisions.at(-1)!.document.title || "Без названия"}
                  </Text>
                ))}
              </Stack>
            ) : null}
            <Checkbox
              label="Связать совпадающие API"
              checked={relink}
              disabled={busy}
              onChange={(event) => setRelink(event.currentTarget.checked)}
            />
            <Text size="xs" c="dimmed">
              Связь восстановится только при точном совпадении контракта с одним API на этом
              сервере. Остальные контракты останутся копиями.
            </Text>
            {error || create.isError ? (
              <Alert color="red" role="alert">
                {error || describeApiFailureDetailed(create.error)}
              </Alert>
            ) : null}
            <Group justify="flex-end">
              <Button variant="default" disabled={busy} onClick={onClose}>
                Отмена
              </Button>
              <Button
                loading={busy}
                disabled={!parsed || reading || busy}
                onClick={() => void importPackage()}
              >
                Импортировать сценарий
              </Button>
            </Group>
          </>
        )}
      </Stack>
    </Modal>
  );
}
