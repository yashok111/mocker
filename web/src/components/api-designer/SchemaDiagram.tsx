import { useEffect, useMemo, useState, type ReactElement } from "react";
import {
  ActionIcon,
  Alert,
  Button,
  Group,
  Loader,
  NativeSelect,
  Stack,
  Text,
  Textarea,
  Tooltip,
} from "@mantine/core";
import { useClipboard } from "@mantine/hooks";
import {
  IconCheck,
  IconCopy,
  IconDownload,
  IconZoomIn,
  IconZoomOut,
  IconZoomReset,
} from "@tabler/icons-react";
import { listSchemas, type ApiDocument, type DocumentSelection } from "./documentModel";
import { buildSchemaDiagram } from "./schemaDiagramModel";
import { renderSchemaDiagram } from "./renderSchemaDiagram";
import { createDiagramWebp } from "./exportSchemaDiagram";
import classes from "./SchemaDiagram.module.css";

interface Props {
  document: ApiDocument | null;
  selection: DocumentSelection;
  pendingFormDraft: boolean;
}

type Rendered =
  | { source: string; svg: string; width: number; error?: undefined }
  | { source: string; svg?: undefined; error: string };

function downloadDiagram(blob: Blob, extension: "svg" | "webp"): void {
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = `schema-diagram.${extension}`;
  window.document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  // Give the browser time to consume the object URL before releasing it.
  window.setTimeout(() => URL.revokeObjectURL(url), 1000);
}

export default function SchemaDiagram({
  document,
  selection,
  pendingFormDraft,
}: Props): ReactElement {
  const [choice, setChoice] = useState<{ selection: DocumentSelection; name: string }>();
  const [depth, setDepth] = useState("1");
  const [zoom, setZoom] = useState(100);
  const [rendered, setRendered] = useState<Rendered>();
  const [attempt, setAttempt] = useState(0);
  const [exportingWebp, setExportingWebp] = useState(false);
  const [exportError, setExportError] = useState<{ source: string; message: string }>();
  const clipboard = useClipboard({ timeout: 1600 });
  const names = useMemo(() => (document === null ? [] : listSchemas(document).sort()), [document]);
  const chosenName =
    choice?.selection === selection
      ? choice.name
      : selection.kind === "schema"
        ? selection.name
        : "";
  const schema = names.includes(chosenName) ? chosenName : "";
  const diagram = useMemo(
    () =>
      buildSchemaDiagram(document ?? {}, {
        schema: schema === "" ? undefined : schema,
        depth: Number(depth),
      }),
    [document, schema, depth],
  );
  const source = pendingFormDraft ? "" : diagram.source;
  const current = rendered?.source === source ? rendered : undefined;

  async function downloadWebp(): Promise<void> {
    if (!current?.svg || exportingWebp) return;
    setExportingWebp(true);
    setExportError(undefined);
    try {
      downloadDiagram(await createDiagramWebp(current.svg), "webp");
    } catch {
      setExportError({
        source,
        message: "Не удалось скачать WebP. Попробуйте ещё раз или скачайте SVG.",
      });
    } finally {
      setExportingWebp(false);
    }
  }

  useEffect(() => {
    if (source === "") return;
    let active = true;
    const timer = window.setTimeout(() => {
      void renderSchemaDiagram(source).then(
        (svg) => {
          if (active) {
            const root = new DOMParser().parseFromString(svg, "image/svg+xml").documentElement;
            const width = Number(root.getAttribute("viewBox")?.trim().split(/\s+/)[2]);
            setRendered({ source, svg, width });
          }
        },
        () => {
          if (active)
            setRendered({
              source,
              error: "Не удалось построить диаграмму. Исходник Mermaid можно скопировать ниже.",
            });
        },
      );
    }, 150);
    return () => {
      active = false;
      window.clearTimeout(timer);
    };
  }, [source, attempt]);

  if (document === null) {
    return (
      <Alert color="yellow" data-testid="schema-diagram">
        Исправьте JSON в редакторе, чтобы построить диаграмму.
      </Alert>
    );
  }
  if (pendingFormDraft) {
    return (
      <Alert color="yellow" data-testid="schema-diagram">
        Завершите редактирование полей формы, чтобы обновить диаграмму.
      </Alert>
    );
  }
  if (names.length === 0) {
    return (
      <Text c="dimmed" size="sm" data-testid="schema-diagram">
        В документе пока нет схем. Добавьте схему в дереве API.
      </Text>
    );
  }

  return (
    <Stack gap="md" data-testid="schema-diagram">
      <Group justify="space-between" align="flex-end" className={classes.toolbar}>
        <Group align="flex-end" className={classes.filters}>
          <NativeSelect
            label="Схема"
            value={schema}
            onChange={(event) => {
              setChoice({ selection, name: event.currentTarget.value });
              setZoom(100);
            }}
            data={[
              { value: "", label: "Все схемы" },
              ...names.map((name) => ({ value: name, label: name })),
            ]}
            className={classes.schemaSelect}
          />
          <NativeSelect
            label="Глубина связей"
            value={depth}
            onChange={(event) => setDepth(event.currentTarget.value)}
            disabled={schema === ""}
            data={[
              { value: "0", label: "Только схема" },
              { value: "1", label: "1 уровень" },
              { value: "2", label: "2 уровня" },
              { value: "3", label: "3 уровня" },
            ]}
          />
        </Group>
        <Group gap="xs">
          <Button
            size="xs"
            variant="default"
            leftSection={clipboard.copied ? <IconCheck size={14} /> : <IconCopy size={14} />}
            onClick={() => clipboard.copy(source)}
          >
            {clipboard.copied ? "Скопировано" : "Скопировать Mermaid"}
          </Button>
          <Button
            size="xs"
            variant="default"
            leftSection={<IconDownload size={14} />}
            disabled={!current?.svg}
            onClick={() => {
              if (current?.svg)
                downloadDiagram(
                  new Blob([current.svg], { type: "image/svg+xml;charset=utf-8" }),
                  "svg",
                );
            }}
          >
            Скачать SVG
          </Button>
          <Button
            size="xs"
            variant="default"
            leftSection={<IconDownload size={14} />}
            disabled={!current?.svg}
            loading={exportingWebp}
            onClick={() => void downloadWebp()}
          >
            Скачать WebP
          </Button>
        </Group>
      </Group>
      <Group justify="space-between" gap="xs">
        <Text size="xs" c="dimmed" aria-live="polite">
          Схемы: {diagram.schemaCount} · Связи: {diagram.relationCount} · ? — необязательное поле
        </Text>
        <Group gap={4} aria-label="Масштаб диаграммы">
          <Tooltip label="Уменьшить">
            <ActionIcon
              variant="subtle"
              aria-label="Уменьшить диаграмму"
              disabled={zoom <= 50}
              onClick={() => setZoom((value) => Math.max(50, value - 25))}
            >
              <IconZoomOut size={16} />
            </ActionIcon>
          </Tooltip>
          <Text size="xs" className={classes.zoom}>
            {zoom}%
          </Text>
          <Tooltip label="Увеличить">
            <ActionIcon
              variant="subtle"
              aria-label="Увеличить диаграмму"
              disabled={zoom >= 300}
              onClick={() => setZoom((value) => Math.min(300, value + 25))}
            >
              <IconZoomIn size={16} />
            </ActionIcon>
          </Tooltip>
          <Tooltip label="Вписать в ширину">
            <ActionIcon
              variant="subtle"
              aria-label="Вписать диаграмму в ширину"
              onClick={() => setZoom(100)}
            >
              <IconZoomReset size={16} />
            </ActionIcon>
          </Tooltip>
        </Group>
      </Group>
      {diagram.warnings.length > 0 && (
        <Alert color="yellow" title="Диаграмма показана с ограничениями">
          <ul className={classes.warnings}>
            {diagram.warnings.slice(0, 5).map((warning) => (
              <li key={warning}>{warning}</li>
            ))}
          </ul>
          {diagram.warnings.length > 5 && (
            <Text size="xs">Ещё предупреждений: {diagram.warnings.length - 5}</Text>
          )}
        </Alert>
      )}
      {clipboard.error && (
        <Alert color="yellow">
          Не удалось скопировать. Выделите исходник Mermaid ниже и скопируйте вручную.
        </Alert>
      )}
      {exportError?.source === source && (
        <Alert color="red" title="Ошибка экспорта" role="alert">
          {exportError.message}
        </Alert>
      )}
      {current?.error ? (
        <Alert color="red" title="Ошибка диаграммы">
          {current.error}
          <Button
            size="xs"
            variant="light"
            mt="sm"
            onClick={() => {
              setRendered(undefined);
              setAttempt((value) => value + 1);
            }}
          >
            Повторить
          </Button>
        </Alert>
      ) : (
        /* oxlint-disable jsx-a11y/no-noninteractive-tabindex -- Keyboard users need focus to scroll the diagram. */
        <section
          className={classes.viewport}
          aria-label="Диаграмма схем"
          tabIndex={0}
          aria-busy={!current?.svg}
        >
          {current?.svg ? (
            <figure
              className={classes.drawing}
              style={{
                width: `${zoom}%`,
                maxWidth: Number.isFinite(current.width)
                  ? `${((current.width + 48) * zoom) / 100}px`
                  : undefined,
              }}
              aria-label={schema ? `Схема ${schema} и её связи` : "Связи схем API"}
              dangerouslySetInnerHTML={{ __html: current.svg }}
            />
          ) : (
            <Group component="output" justify="center" className={classes.loading}>
              <Loader size="sm" />
              <Text size="sm" c="dimmed">
                Строим диаграмму…
              </Text>
            </Group>
          )}
        </section>
        /* oxlint-enable jsx-a11y/no-noninteractive-tabindex */
      )}
      <details className={classes.source}>
        <summary>Исходник Mermaid</summary>
        <Textarea
          aria-label="Исходник Mermaid"
          value={source}
          readOnly
          rows={12}
          spellCheck={false}
          mt="sm"
          styles={{ input: { fontFamily: "var(--mantine-font-family-monospace)", fontSize: 12 } }}
        />
      </details>
    </Stack>
  );
}
