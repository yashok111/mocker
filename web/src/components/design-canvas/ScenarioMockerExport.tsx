import { useState } from "react";
import { Alert, Button, Modal, Stack, Text } from "@mantine/core";
import type { CanvasDocument } from "./types";
import { serializeMockerScenario } from "./scenarioMockerFile";
import { downloadScenarioBlob } from "./scenarioExportFiles";

export function ScenarioMockerExport({
  document,
  formDrafts,
}: {
  document: CanvasDocument;
  formDrafts: Record<string, string>;
}) {
  const [opened, setOpened] = useState(false);
  const [error, setError] = useState("");
  function download(): void {
    setError("");
    try {
      const text = serializeMockerScenario(document, formDrafts);
      downloadScenarioBlob(new Blob([text], { type: "application/json" }), "scenario.mocker");
      setOpened(false);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Не удалось экспортировать сценарий");
    }
  }
  return (
    <>
      <Button
        variant="default"
        size="sm"
        onClick={() => {
          setError("");
          setOpened(true);
        }}
      >
        Экспорт .mocker
      </Button>
      <Modal
        opened={opened}
        onClose={() => setOpened(false)}
        title="Экспорт сценария .mocker"
        centered
      >
        <Stack>
          <Text size="sm">
            Файл сохранит текущее состояние диаграммы, незавершённые поля и встроенные контракты.
            Его можно импортировать как новый сценарий.
          </Text>
          <Text size="sm">
            В файл также войдут сохранённые заголовки, тела запросов и переменные. Связанные API
            будут перенесены как независимые копии.
          </Text>
          {error ? (
            <Alert color="red" role="alert">
              {error}
            </Alert>
          ) : null}
          <Button onClick={download}>Скачать .mocker</Button>
        </Stack>
      </Modal>
    </>
  );
}
