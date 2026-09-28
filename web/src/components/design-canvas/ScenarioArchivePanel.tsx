import { useEffect, useRef, useState } from "react";
import { Alert, Button, Checkbox, Group, Stack, Text } from "@mantine/core";
import { ApiFailure } from "@/api/client";
import { describeApiFailureDetailed } from "@/api/errors";
import type { ScenarioExportOptions, ScenarioExportTarget } from "@/api/generated/schemas";
import type { DesignScenarioRevision } from "./designScenarioApi";
import {
  archiveItemKey,
  buildScenarioArchive,
  MAX_ARCHIVE_ITEMS,
  type ScenarioArchiveItem,
} from "./scenarioArchiveExport";
import { formatLabels } from "./scenarioExportLabels";
import { downloadScenarioBlob } from "./scenarioExportFiles";

export function ScenarioArchivePanel({
  revision,
  options,
  onLocate,
}: {
  revision: DesignScenarioRevision;
  options: ScenarioExportOptions;
  onLocate: (target: ScenarioExportTarget, pointer?: string) => void;
}) {
  const diagram = options.options.find((option) => option.format === "mermaid");
  const choices = [
    ...options.options,
    ...(["svg", "png"] as const).map((format) => ({
      format,
      contractId: undefined,
      ready: diagram?.ready ?? false,
      diagnostics: diagram?.diagnostics ?? [],
    })),
  ];
  const [selected, setSelected] = useState(
    () =>
      new Set(
        options.options
          .filter(
            (option) =>
              option.ready && (option.format === "mermaid" || option.format === "markdown"),
          )
          .map(archiveItemKey),
      ),
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const active = useRef(false);
  useEffect(() => {
    active.current = true;
    return () => {
      active.current = false;
    };
  }, []);
  const items: ScenarioArchiveItem[] = [...selected]
    .flatMap((key) => choices.filter((choice) => choice.ready && archiveItemKey(choice) === key))
    .map(({ format, contractId }) => ({ format, ...(contractId ? { contractId } : {}) }));

  async function download(): Promise<void> {
    setBusy(true);
    setError("");
    try {
      if (!active.current) return;
      const blob = await buildScenarioArchive(revision, items, {
        isCancelled: () => !active.current,
      });
      if (active.current)
        downloadScenarioBlob(blob, `scenario-${revision.scenarioId}-r${revision.id}.zip`);
    } catch (cause) {
      if (active.current)
        setError(
          cause instanceof ApiFailure
            ? describeApiFailureDetailed(cause)
            : cause instanceof Error
              ? cause.message
              : "Не удалось собрать ZIP",
        );
    } finally {
      if (active.current) setBusy(false);
    }
  }

  return (
    <Stack gap="sm" aria-label="Состав ZIP">
      <Text size="sm">
        Выберите файлы одной ревизии. В ZIP также войдёт manifest с версией и составом. Архив
        предназначен для передачи результатов; восстановление проекта из него не поддерживается.
      </Text>
      <Text size="xs" c="dimmed">
        PDF сохраняется через отдельный формат «PDF (печать)». Postman и cURL включают сохранённые
        параметры выполнения.
      </Text>
      {choices.map((choice) => {
        const key = archiveItemKey(choice);
        const contract = (
          choice.format === "asyncapi-json" || choice.format === "asyncapi-yaml"
            ? (revision.document.eventModel?.contracts ?? [])
            : revision.document.contracts
        ).find((c) => c.id === choice.contractId);
        return (
          <Stack key={key} gap={4}>
            <Checkbox
              label={`${formatLabels[choice.format]}${contract ? ` · ${contract.name}` : ""}`}
              checked={selected.has(key)}
              disabled={
                busy || !choice.ready || (!selected.has(key) && items.length >= MAX_ARCHIVE_ITEMS)
              }
              onChange={(event) => {
                const checked = event.currentTarget.checked;
                setSelected((current) => {
                  const next = new Set(current);
                  if (checked) next.add(key);
                  else next.delete(key);
                  return next;
                });
                setError("");
              }}
            />
            {(choice.diagnostics ?? []).map((diagnostic, index) => (
              <Group key={`${diagnostic.code}-${index}`} gap="xs" pl="lg">
                <Text size="xs" c={diagnostic.severity === "error" ? "red" : "dimmed"}>
                  {diagnostic.message}
                </Text>
                {diagnostic.target ? (
                  <Button
                    size="compact-xs"
                    variant="subtle"
                    onClick={() => {
                      if (diagnostic.pointer) onLocate(diagnostic.target!, diagnostic.pointer);
                      else onLocate(diagnostic.target!);
                    }}
                  >
                    Перейти к объекту
                  </Button>
                ) : null}
              </Group>
            ))}
          </Stack>
        );
      })}
      <Text size="xs" c="dimmed">
        Выбрано: {items.length} из {MAX_ARCHIVE_ITEMS}. До 32 МиБ.
      </Text>
      {error ? (
        <Alert color="red" role="alert">
          {error}
        </Alert>
      ) : null}
      <Button loading={busy} disabled={items.length === 0 || busy} onClick={() => void download()}>
        Скачать ZIP
      </Button>
    </Stack>
  );
}
