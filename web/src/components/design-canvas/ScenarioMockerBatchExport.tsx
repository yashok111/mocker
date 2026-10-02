import { useRef, useState } from "react";
import { Alert, Button, Checkbox, Modal, Stack, Text } from "@mantine/core";
import { exportScenarioTransfer } from "@/api/generated/design-scenarios/design-scenarios";
import { describeApiFailureDetailed } from "@/api/errors";
import type { DesignScenarioSummary } from "./designScenarioApi";
import { parseMockerTransfer } from "./scenarioMockerFile";
import { downloadScenarioBlob } from "./scenarioExportFiles";

export function ScenarioMockerBatchExport({ scenarios }: { scenarios: DesignScenarioSummary[] }) {
  const [opened, setOpened] = useState(false);
  const [selected, setSelected] = useState<number[]>([]);
  const [history, setHistory] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const active = useRef(false);
  async function download() {
    if (active.current || !selected.length) return;
    active.current = true;
    setBusy(true);
    setError("");
    try {
      const response = await exportScenarioTransfer({
        scenarioIds: selected,
        includeHistory: history,
      });
      if (response.status !== 200) throw new Error("Не удалось экспортировать пакет");
      const { bundle } = parseMockerTransfer(JSON.stringify(response.data));
      downloadScenarioBlob(
        new Blob([JSON.stringify(bundle)], { type: "application/json" }),
        "scenarios.mocker",
      );
      setOpened(false);
    } catch (cause) {
      setError(describeApiFailureDetailed(cause));
    } finally {
      active.current = false;
      setBusy(false);
    }
  }
  return (
    <>
      <Button
        variant="default"
        disabled={!scenarios.length}
        onClick={() => {
          setSelected([]);
          setError("");
          setOpened(true);
        }}
      >
        Экспорт пакета
      </Button>
      <Modal
        opened={opened}
        onClose={() => {
          if (!active.current) setOpened(false);
        }}
        title="Экспорт пакета .mocker"
        centered
        withCloseButton={!busy}
      >
        <Stack>
          <Text size="sm">
            Выберите до 20 сохранённых сценариев. Файл включает контракты, заголовки, тела запросов
            и переменные. До 2 МБ, до 200 ревизий на сценарий.
          </Text>
          {scenarios.map((scenario) => (
            <Checkbox
              key={scenario.id}
              label={scenario.name}
              checked={selected.includes(scenario.id)}
              disabled={busy || (!selected.includes(scenario.id) && selected.length >= 20)}
              onChange={(event) => {
                const checked = event.currentTarget.checked;
                setSelected((ids) =>
                  checked ? [...ids, scenario.id] : ids.filter((id) => id !== scenario.id),
                );
              }}
            />
          ))}
          <Checkbox
            label="Включить историю ревизий"
            checked={history}
            disabled={busy}
            onChange={(event) => setHistory(event.currentTarget.checked)}
          />
          {error ? (
            <Alert color="red" role="alert">
              {error}
            </Alert>
          ) : null}
          <Button
            loading={busy}
            disabled={busy || !selected.length}
            onClick={() => void download()}
          >
            Скачать пакет .mocker
          </Button>
        </Stack>
      </Modal>
    </>
  );
}
