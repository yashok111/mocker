import { useEffect, useMemo, useRef, useState, type ReactElement } from "react";
import { Alert, Button, Group, Loader, Modal, NativeSelect, Stack, Text } from "@mantine/core";
import { ApiFailure } from "@/api/client";
import { describeApiFailureDetailed } from "@/api/errors";
import type {
  ScenarioExportArtifact,
  ScenarioExportDiagnostic,
  ScenarioExportFormat,
  ScenarioExportOptions,
  ScenarioExportTarget,
} from "@/api/generated/schemas";
import type { DesignScenarioRevision } from "./designScenarioApi";
import { loadScenarioArtifact, loadScenarioExportOptions } from "./scenarioExportApi";
import { downloadScenarioArtifact, downloadScenarioBlob } from "./scenarioExportFiles";

export type ResultFormat = ScenarioExportFormat | "svg" | "png" | "pdf";
export interface ScenarioResultSelection {
  format: ResultFormat;
  contractId: string;
}
const formatLabels: Record<ResultFormat, string> = {
  plantuml: "PlantUML",
  mermaid: "Mermaid",
  "openapi-json": "OpenAPI JSON",
  "openapi-yaml": "OpenAPI YAML",
  postman: "Postman",
  curl: "cURL",
  markdown: "Markdown",
  html: "HTML",
  pdf: "PDF (печать)",
  svg: "SVG",
  png: "PNG",
};

export interface ScenarioResultsModalProps {
  opened: boolean;
  scenarioId: number;
  revision: DesignScenarioRevision;
  latestRevisionId?: number;
  onRefresh?: () => void;
  selection?: ScenarioResultSelection;
  onSelectionChange?: (selection: ScenarioResultSelection) => void;
  onClose(reason?: "action"): void;
  onLocate(target: ScenarioExportTarget): void;
  onPrepareContracts(): void;
  onRun(): void;
}

export function ScenarioResultsModal({
  opened,
  scenarioId,
  revision,
  latestRevisionId,
  onRefresh,
  selection,
  onSelectionChange,
  onClose,
  onLocate,
  onPrepareContracts,
  onRun,
}: ScenarioResultsModalProps): ReactElement {
  const [localSelection, setLocalSelection] = useState<ScenarioResultSelection>({
    format: "mermaid",
    contractId: "",
  });
  const currentSelection = selection ?? localSelection;
  const format = currentSelection.format;
  const contractId = revision.document.contracts.some(
    (item) => item.id === currentSelection.contractId,
  )
    ? currentSelection.contractId
    : "";
  function chooseSelection(next: ScenarioResultSelection): void {
    if (onSelectionChange) onSelectionChange(next);
    else setLocalSelection(next);
  }
  const [options, setOptions] = useState<ScenarioExportOptions | null>(null);
  const [artifact, setArtifact] = useState<ScenarioExportArtifact | null>(null);
  const [artifactKey, setArtifactKey] = useState("");
  const [image, setImage] = useState<Blob | null>(null);
  const [imageKey, setImageKey] = useState("");
  const [imageUrl, setImageUrl] = useState<string | null>(null);
  const [error, setError] = useState("");
  const [blockedDiagnostics, setBlockedDiagnostics] = useState<ScenarioExportDiagnostic[]>([]);
  const [loading, setLoading] = useState(false);
  const documentFrame = useRef<HTMLIFrameElement>(null);
  const [loadedDocumentKey, setLoadedDocumentKey] = useState("");
  const isDocument = format === "html" || format === "pdf";
  const serverFormat = format === "pdf" ? "html" : format;
  const isContract = format === "openapi-json" || format === "openapi-yaml";
  const requestKey = `${scenarioId}:${revision.id}:${format}:${isContract ? contractId : ""}`;
  const currentOptions =
    options?.scenarioId === scenarioId && options.revisionId === revision.id ? options : null;
  const option = currentOptions?.options.find(
    (item) =>
      item.format === serverFormat && (item.contractId ?? "") === (isContract ? contractId : ""),
  );
  const visibleArtifact = artifactKey === requestKey ? artifact : null;
  const visibleImage = imageKey === requestKey ? image : null;
  const selectedContract = revision.document.contracts.find((item) => item.id === contractId);
  const included = useMemo(
    () =>
      revision.document.messages.filter((message) => message.operation?.contractId === contractId),
    [revision, contractId],
  );
  const descriptive = useMemo(
    () =>
      revision.document.messages.filter((message) => message.operation?.contractId !== contractId),
    [revision, contractId],
  );

  useEffect(() => {
    if (!opened) return;
    let active = true;
    setOptions(null);
    setArtifact(null);
    setImage(null);
    setLoadedDocumentKey("");
    setError("");
    setLoading(true);
    void loadScenarioExportOptions(scenarioId, revision.id)
      .then((next) => {
        if (active) setOptions(next);
      })
      .catch((cause: unknown) => {
        if (active) setError(describeApiFailureDetailed(cause));
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [opened, scenarioId, revision.id]);

  useEffect(() => {
    if (!opened || !currentOptions) return;
    if (isContract && !contractId) return;
    if (isContract && !selectedContract) return;
    setArtifact(null);
    setImage(null);
    setLoadedDocumentKey("");
    setBlockedDiagnostics([]);
    setError("");
    if (option && !option.ready) return;
    let active = true;
    setLoading(true);
    const request =
      format === "svg" || format === "png"
        ? import("./sequenceImageExport")
            .then(({ exportSequenceImage }) => exportSequenceImage(revision.document, format))
            .then((blob) => {
              if (active) {
                setImageKey(requestKey);
                setImage(blob);
              }
            })
        : loadScenarioArtifact(
            scenarioId,
            revision.id,
            serverFormat as ScenarioExportFormat,
            isContract ? contractId : undefined,
          ).then((next) => {
            if (active) {
              setArtifactKey(requestKey);
              setArtifact(next);
            }
          });
    void request
      .catch((cause: unknown) => {
        if (!active) return;
        setError(describeApiFailureDetailed(cause));
        if (
          cause instanceof ApiFailure &&
          cause.status === 422 &&
          cause.details &&
          typeof cause.details === "object" &&
          "diagnostics" in cause.details &&
          Array.isArray(cause.details.diagnostics)
        ) {
          setBlockedDiagnostics(cause.details.diagnostics as ScenarioExportDiagnostic[]);
        }
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [
    opened,
    currentOptions,
    format,
    serverFormat,
    contractId,
    scenarioId,
    revision,
    option,
    isContract,
    selectedContract,
    requestKey,
  ]);

  useEffect(() => {
    if (!image) {
      setImageUrl(null);
      return;
    }
    const url = URL.createObjectURL(image);
    setImageUrl(url);
    return () => URL.revokeObjectURL(url);
  }, [image]);

  const diagnostics = [...(option?.diagnostics ?? []), ...blockedDiagnostics];
  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title="Получить результат"
      size="xl"
      trapFocus
      returnFocus
    >
      <Stack gap="md">
        <Group justify="space-between">
          <Text size="sm">
            Ревизия {revision.version} · #{revision.id}
          </Text>
          {latestRevisionId !== undefined && latestRevisionId !== revision.id && onRefresh ? (
            <Button variant="light" onClick={onRefresh}>
              Обновить до текущей ревизии
            </Button>
          ) : null}
        </Group>
        <NativeSelect
          label="Формат результата"
          value={format}
          onChange={(event) =>
            chooseSelection({
              ...currentSelection,
              format: event.currentTarget.value as ResultFormat,
            })
          }
          data={Object.entries(formatLabels).map(([value, label]) => ({ value, label }))}
        />
        {isDocument ? (
          <Text size="sm" c="dimmed">
            Документ содержит диаграмму, участников, шаги и сохранённые API-контракты.
            {format === "pdf"
              ? " В окне печати выберите «Сохранить как PDF». Большая диаграмма разделена на листы."
              : " HTML можно открыть без подключения к сети."}
          </Text>
        ) : null}
        {format === "postman" || format === "curl" ? (
          <Text size="sm" c="dimmed">
            {format === "postman"
              ? "Коллекция Postman v2.1 содержит включённые HTTP-запросы и настройки выполнения. Перед запуском проверьте переменные и базовые URL сервисов в коллекции."
              : "Команды cURL используют сохранённые параметры выполнения. Перед запуском проверьте базовые URL сервисов в файле .sh. Ограничения проверок и извлечения значений указаны ниже."}
          </Text>
        ) : null}
        {isContract ? (
          <>
            <NativeSelect
              label="Весь API-контракт"
              value={contractId}
              onChange={(event) =>
                chooseSelection({ ...currentSelection, contractId: event.currentTarget.value })
              }
              data={[
                { value: "", label: "Выберите контракт" },
                ...revision.document.contracts.map((contract) => ({
                  value: contract.id,
                  label: contract.name,
                })),
              ]}
            />
            {selectedContract ? (
              <Text size="sm">
                Включены в контракт: {included.map((message) => message.label).join(", ") || "нет"}.
                Описательные или из других контрактов:{" "}
                {descriptive.map((message) => message.label).join(", ") || "нет"}.
              </Text>
            ) : null}
          </>
        ) : null}
        {loading ? <Loader aria-label="Загружаем результат" /> : null}
        {error ? (
          <Alert color="red" role="alert">
            {error}
          </Alert>
        ) : null}
        {diagnostics.length ? (
          <Stack gap="xs" aria-label="Диагностика результата">
            {diagnostics.map((diagnostic, index) => (
              <Group key={`${diagnostic.code}-${index}`} gap="xs">
                <Text size="sm">{diagnostic.message}</Text>
                {diagnostic.target ? (
                  <Button
                    size="compact-xs"
                    variant="subtle"
                    onClick={() => {
                      onClose("action");
                      onLocate(diagnostic.target!);
                    }}
                  >
                    Перейти к {diagnostic.target.kind === "contract" ? "контракту" : "объекту"}
                  </Button>
                ) : null}
              </Group>
            ))}
          </Stack>
        ) : null}
        {isContract && (!selectedContract || option?.ready === false) ? (
          <Button
            variant="light"
            onClick={() => {
              onClose("action");
              onPrepareContracts();
            }}
          >
            Описать API
          </Button>
        ) : null}
        {visibleArtifact && isDocument ? (
          <iframe
            key={requestKey}
            ref={documentFrame}
            title="Предпросмотр документа"
            sandbox="allow-same-origin allow-modals"
            srcDoc={visibleArtifact.content}
            onLoad={() => setLoadedDocumentKey(requestKey)}
            style={{
              width: "100%",
              height: 420,
              border: "1px solid var(--mantine-color-default-border)",
              background: "white",
            }}
          />
        ) : visibleArtifact ? (
          <pre
            aria-label="Предпросмотр результата"
            style={{ overflow: "auto", maxHeight: 360, whiteSpace: "pre-wrap" }}
          >
            {visibleArtifact.content}
          </pre>
        ) : null}
        {visibleImage && imageUrl ? (
          <img
            src={imageUrl}
            alt={`Предпросмотр ${formatLabels[format]}`}
            style={{ maxWidth: "100%", maxHeight: 360 }}
          />
        ) : null}
        <Group>
          {format === "pdf" ? (
            <Button
              disabled={!visibleArtifact || loadedDocumentKey !== requestKey}
              onClick={() => {
                try {
                  const target = documentFrame.current?.contentWindow;
                  if (!target) throw new Error("Document frame unavailable");
                  target.focus();
                  target.print();
                } catch {
                  setError(
                    "Не удалось открыть печать. Скачайте HTML и откройте его в браузере для сохранения PDF.",
                  );
                }
              }}
            >
              Печать / сохранить PDF
            </Button>
          ) : null}
          <Button
            variant={format === "pdf" ? "default" : "filled"}
            disabled={!visibleArtifact && !visibleImage}
            onClick={() => {
              if (visibleArtifact) downloadScenarioArtifact(visibleArtifact);
              if (visibleImage)
                downloadScenarioBlob(
                  visibleImage,
                  `${revision.document.title || "scenario"}-${revision.id}.${format}`,
                );
            }}
          >
            Скачать {format === "pdf" ? "HTML" : formatLabels[format]}
          </Button>
          <Button
            variant="default"
            onClick={() => {
              onClose("action");
              onPrepareContracts();
            }}
          >
            Создать мок
          </Button>
          <Button
            variant="default"
            onClick={() => {
              onClose("action");
              onRun();
            }}
          >
            Открыть выполнение
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}
