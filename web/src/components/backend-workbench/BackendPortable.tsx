import { useEffect, useRef, useState } from "react";
import {
  Alert,
  Anchor,
  Button,
  Code,
  FileInput,
  Group,
  Paper,
  Stack,
  Table,
  Text,
  Textarea,
  TextInput,
  Title,
} from "@mantine/core";
import {
  exportBackendProject,
  getBackendExportChunk,
  resolveBackendPortableSelection,
} from "@/api/generated/backend-projects/backend-projects";
import type {
  BackendPortableArtifactMapping,
  BackendPortableSession,
  BackendPortablePreview,
  BackendPortableExport,
  ExportBackendProjectRequest,
  ResolveBackendPortableSelectionRequest,
} from "@/api/generated/schemas";
import { parseBrowserSafeJson } from "@/api/preciseJson";
import { ApiFailure } from "@/api/client";
import { describeApiFailureDetailed } from "@/api/errors";
import { hashBackendUTF8 } from "./backendImportHash";
import {
  loadPortableCheckpoint,
  savePortableCheckpoint,
  readPortableBundle,
  runPortableAttempt,
  portableCheckpointAfter,
  type PortableAttempt,
  type PortableBundle,
  type PortableCheckpoint,
} from "./backendPortableTransfer";

function downloadJSON(name: string, value: unknown) {
  const url = URL.createObjectURL(new Blob([JSON.stringify(value)], { type: "application/json" }));
  const link = document.createElement("a");
  link.href = url;
  link.download = name;
  document.body.append(link);
  try {
    link.click();
  } finally {
    link.remove();
    setTimeout(() => URL.revokeObjectURL(url), 0);
  }
}
export function BackendPortable({
  projectId,
  onDirty,
}: {
  projectId?: string;
  onDirty?: (dirty: boolean) => void;
}) {
  const key = `mocker-portable-import-v1:${projectId ?? "global"}`;
  const exportKey = `mocker-portable-export-v1:${projectId ?? "global"}`;
  const exportsKey = `${exportKey}:sessions`;
  type SavedExport = BackendPortableSession & { cleanupKey?: string };
  const [exports, setExports] = useState<SavedExport[]>(() => {
    try {
      const raw = localStorage.getItem(exportsKey);
      const parsed = raw ? parseBrowserSafeJson(raw) : [];
      return Array.isArray(parsed) ? (parsed as SavedExport[]) : [];
    } catch {
      return [];
    }
  });
  function saveExports(values: SavedExport[]) {
    localStorage.setItem(exportsKey, JSON.stringify(values));
    setExports(values);
  }

  const [checkpoint, setCheckpoint] = useState(() => loadPortableCheckpoint(key));
  const current = useRef(checkpoint);
  const [bundle, setBundle] = useState<PortableBundle>();
  const [preview, setPreview] = useState<BackendPortablePreview>();
  const [name, setName] = useState("");
  const [mappingText, setMappingText] = useState("[]");
  const [selectionText, setSelectionText] = useState(
    JSON.stringify({ target: { revisionId: "" }, diagramViews: [], savedViews: [] }, null, 2),
  );
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const running = useRef(false);
  const [exported, setExported] = useState<BackendPortableExport>();
  const [pendingExport, setPendingExport] = useState<ExportBackendProjectRequest | undefined>(
    () => {
      try {
        const raw = localStorage.getItem(exportKey);
        return raw ? (parseBrowserSafeJson(raw) as ExportBackendProjectRequest) : undefined;
      } catch {
        return undefined;
      }
    },
  );
  const dirty =
    exports.some((e) => !!e.cleanupKey) ||
    !!checkpoint?.pending ||
    !!pendingExport ||
    (!!checkpoint?.session && !["committed", "aborted"].includes(checkpoint.session.state));
  useEffect(() => {
    onDirty?.(dirty);
    return () => onDirty?.(false);
  }, [dirty, onDirty]);
  function save(value: PortableCheckpoint) {
    savePortableCheckpoint(key, value);
    current.current = value;
    setCheckpoint(value);
  }
  async function task(action: () => Promise<void>) {
    if (running.current) return;
    running.current = true;
    setBusy(true);
    setError("");
    try {
      await action();
    } catch (cause) {
      setError(
        cause instanceof Error && !(cause instanceof ApiFailure)
          ? cause.message
          : describeApiFailureDetailed(cause),
      );
    } finally {
      running.current = false;
      setBusy(false);
    }
  }
  async function attempt(operation: PortableAttempt) {
    const before = current.current;
    if (!before) throw new Error("Выберите portable-файл");
    save({ ...before, pending: operation });
    try {
      const result = await runPortableAttempt(operation);
      const next = portableCheckpointAfter(current.current!, operation, result);
      save(next);
      if ("unresolved" in result) setPreview(result);
      if ("project" in result)
        setMessage(`Создан проект ${result.project.name}. Точная квитанция сохранена.`);
    } catch (cause) {
      if (cause instanceof ApiFailure && [400, 404, 409, 413, 422].includes(cause.status))
        save({ ...current.current!, pending: undefined, candidate: undefined });
      throw cause;
    }
  }
  async function chooseFile(file: File | null) {
    if (!file) return;
    await task(async () => {
      if (file.size > 2 * 268435456 + 16 * 1048576)
        throw new Error("Portable-файл превышает допустимый размер");
      const parsed = await readPortableBundle(await file.text());
      const prior = current.current;
      if (
        prior &&
        prior.session &&
        !["committed", "aborted"].includes(prior.session.state) &&
        prior.manifestHash !== parsed.hash
      )
        throw new Error("Есть незавершённый импорт другого manifest. Сначала отмените его.");
      setBundle(parsed.bundle);
      setPreview(undefined);
      setMessage(`Проверено ${parsed.bundle.chunks.length} chunks; SHA-256 совпадают.`);
      if (
        !prior ||
        prior.manifestHash !== parsed.hash ||
        ["committed", "aborted"].includes(prior.session?.state ?? "")
      )
        save({ manifestHash: parsed.hash, nextChunk: 0 });
    });
  }
  async function upload() {
    if (!bundle) throw new Error("Повторно выберите тот же portable-файл");
    if (current.current?.pending) await attempt(current.current.pending);
    if (!current.current?.session)
      await attempt({
        kind: "begin",
        body: { manifest: bundle.manifest, idempotencyKey: crypto.randomUUID() },
      });
    while (current.current!.nextChunk < bundle.chunks.length) {
      const state = current.current!;
      await attempt({
        kind: "put",
        id: state.session!.id,
        body: {
          expectedVersion: state.session!.version,
          index: state.nextChunk,
          body: bundle.chunks[state.nextChunk]!,
          idempotencyKey: crypto.randomUUID(),
        },
      });
    }
    setMessage("Все chunks сохранены. Проверьте mappings и запросите Preview.");
  }
  function mappings(): BackendPortableArtifactMapping[] {
    const value = parseBrowserSafeJson(mappingText);
    if (!Array.isArray(value)) throw new Error("Mappings должны быть массивом");
    return value as BackendPortableArtifactMapping[];
  }
  async function prepare() {
    const state = current.current;
    if (!state?.session) throw new Error("Сначала загрузите chunks");
    await attempt({
      kind: "preview",
      id: state.session.id,
      body: {
        expectedVersion: state.session.version,
        name,
        artifactMappings: mappings(),
        idempotencyKey: crypto.randomUUID(),
      },
    });
  }
  async function commit() {
    const state = current.current;
    if (!state?.candidate || !state.session) throw new Error("Нужен точный Preview");
    await attempt({
      kind: "commit",
      id: state.session.id,
      body: {
        expectedVersion: state.session.version,
        candidateHash: state.candidate.hash,
        idempotencyKey: crypto.randomUUID(),
      },
    });
  }
  async function abort() {
    const state = current.current;
    if (!state?.session) throw new Error("Staging ещё не создан");
    await attempt({
      kind: "abort",
      id: state.session.id,
      body: { expectedVersion: state.session.version, idempotencyKey: crypto.randomUUID() },
    });
    setPreview(undefined);
    setMessage("Staging отменён; проект не создан.");
  }
  async function exportBundle() {
    if (!projectId) return;
    let request = pendingExport;
    if (!request) {
      const input = parseBrowserSafeJson(selectionText) as ResolveBackendPortableSelectionRequest;
      const resolved = await resolveBackendPortableSelection(projectId, input);
      if (resolved.status !== 200) throw new Error("Не удалось разрешить точные export pins");
      request = { selection: resolved.data, idempotencyKey: crypto.randomUUID() };
      localStorage.setItem(exportKey, JSON.stringify(request));
      setPendingExport(request);
    }
    let response;
    try {
      response = await exportBackendProject(projectId, request);
    } catch (cause) {
      if (cause instanceof ApiFailure && [400, 404, 409, 413, 422].includes(cause.status)) {
        localStorage.removeItem(exportKey);
        setPendingExport(undefined);
      }
      throw cause;
    }
    if (response.status !== 200) throw new Error("Экспорт не подтверждён");
    const result = response.data;
    setExported(result);
    if (!exports.some((e) => e.id === result.session.id)) saveExports([...exports, result.session]);
    const chunks: string[] = [];
    for (const descriptor of result.manifest.chunks) {
      const chunk = await getBackendExportChunk(result.session.id, descriptor.index, {
        manifestHash: result.session.manifestHash,
      });
      if (
        chunk.status !== 200 ||
        chunk.data.manifestHash !== result.session.manifestHash ||
        chunk.data.index !== descriptor.index ||
        new TextEncoder().encode(chunk.data.body).byteLength !== descriptor.bytes ||
        (await hashBackendUTF8(chunk.data.body)) !== descriptor.sha256
      )
        throw new Error("Экспортированный chunk не совпадает с manifest");
      chunks.push(chunk.data.body);
    }
    downloadJSON(`backend-${projectId}.portable.json`, { manifest: result.manifest, chunks });
    localStorage.removeItem(exportKey);
    setPendingExport(undefined);
    setMessage("Portable-файл передан для скачивания.");
  }
  async function cleanupExport(entry: SavedExport) {
    const attemptEntry = { ...entry, cleanupKey: entry.cleanupKey ?? crypto.randomUUID() };
    saveExports(exports.map((e) => (e.id === entry.id ? attemptEntry : e)));
    const result = await runPortableAttempt({
      kind: "abort",
      id: entry.id,
      body: { expectedVersion: entry.version, idempotencyKey: attemptEntry.cleanupKey },
    });
    if ("session" in result || result.id !== entry.id || result.state !== "aborted")
      throw new Error("Очистка экспорта не подтверждена");
    saveExports(exports.filter((e) => e.id !== entry.id));
    setMessage("Chunks экспорта удалены. Скачанный portable-файл сохранён у вас.");
  }
  let samePreview = false;
  try {
    samePreview = checkpoint?.candidate?.request === JSON.stringify([name, mappings()]);
  } catch {
    /* invalid mappings require correction before preview */
  }
  return (
    <Paper withBorder p="md" data-testid="backend-portable">
      <Stack>
        <Title order={2} tabIndex={-1}>
          Перенос Backend Workbench
        </Title>
        {projectId && (
          <>
            <Textarea
              label="Точный target и сохранённые виды для экспорта (JSON)"
              value={selectionText}
              minRows={5}
              autosize
              maxRows={12}
              disabled={busy || !!pendingExport}
              onChange={(e) => setSelectionText(e.currentTarget.value)}
            />
            <Button
              variant="default"
              disabled={busy || !!checkpoint?.pending}
              onClick={() => void task(exportBundle)}
            >
              {pendingExport ? "Повторить точный экспорт" : "Скачать portable-пакет"}
            </Button>
            {exported && (
              <Text size="xs">
                Manifest {exported.session.manifestHash} · {exported.manifest.chunks.length} chunks
                · target {exported.manifest.selection.targetHash}
              </Text>
            )}
          </>
        )}
        {exports.length > 0 && (
          <details>
            <summary>Сохранённые export sessions ({exports.length})</summary>
            <Text size="sm">
              После скачивания можно освободить серверные chunks. До очистки они доступны для
              точного повтора.
            </Text>
            {exports.map((entry) => (
              <Group key={entry.id}>
                <Text size="xs">{entry.id}</Text>
                <Button
                  variant="subtle"
                  disabled={busy || !!pendingExport}
                  onClick={() => void task(() => cleanupExport(entry))}
                >
                  {entry.cleanupKey ? "Повторить очистку экспорта" : "Удалить chunks экспорта"}
                </Button>
              </Group>
            ))}
          </details>
        )}
        <FileInput
          label="Portable JSON для нового проекта"
          accept=".json,application/json"
          disabled={busy || !!checkpoint?.pending || !!pendingExport}
          onChange={(file) => void chooseFile(file)}
        />
        {checkpoint?.session && (
          <Text size="sm">
            Staging {checkpoint.session.id} · версия {checkpoint.session.version} ·{" "}
            {checkpoint.session.state} · загружено {checkpoint.nextChunk} chunks
          </Text>
        )}
        {checkpoint?.session && !bundle && checkpoint.session.state === "staging" && (
          <Text>Выберите тот же файл, чтобы продолжить загрузку с сохранённого шага.</Text>
        )}
        <TextInput
          label="Название нового проекта"
          value={name}
          disabled={busy || !!checkpoint?.pending}
          onChange={(e) => {
            setName(e.currentTarget.value);
            setPreview(undefined);
          }}
        />
        <Textarea
          label="Явные artifact mappings (JSON)"
          description="Пустой массив сохраняет foreign refs неразрешёнными. Local pin требует точного installation UUID, owner/revision ID и hash."
          value={mappingText}
          minRows={3}
          autosize
          maxRows={12}
          disabled={busy || !!checkpoint?.pending}
          onChange={(e) => {
            setMappingText(e.currentTarget.value);
            setPreview(undefined);
          }}
        />
        <Group>
          <Button
            variant="default"
            disabled={
              busy ||
              !bundle ||
              !!checkpoint?.pending ||
              (!!checkpoint?.session && checkpoint.session.state !== "staging")
            }
            onClick={() => void task(upload)}
          >
            Загрузить chunks
          </Button>
          <Button
            variant="default"
            disabled={
              busy ||
              !!checkpoint?.pending ||
              !checkpoint?.session ||
              !["staging", "ready"].includes(checkpoint.session.state)
            }
            onClick={() => void task(prepare)}
          >
            Preview импорта
          </Button>
          <Button
            disabled={
              busy ||
              !!checkpoint?.pending ||
              !samePreview ||
              checkpoint?.session?.state !== "ready"
            }
            onClick={() => void task(commit)}
          >
            Создать проект атомарно
          </Button>
          <Button
            variant="subtle"
            disabled={
              busy ||
              !!checkpoint?.pending ||
              !checkpoint?.session ||
              !["staging", "ready"].includes(checkpoint.session.state)
            }
            onClick={() => void task(abort)}
          >
            Отменить staging
          </Button>
        </Group>
        {checkpoint?.pending && (
          <Alert color="yellow" title="Результат запроса не подтверждён">
            Редактирование заблокировано до повтора точного запроса.
            <Button
              variant="default"
              disabled={busy}
              onClick={() => void task(() => attempt(checkpoint.pending!))}
            >
              Повторить portable-запрос
            </Button>
          </Alert>
        )}
        {preview && (
          <Stack aria-live="polite">
            <Text>
              Подготовлено {preview.recordCount} записей. Новый project ID: {preview.projectId}. До
              Commit проект отсутствует.
            </Text>
            <Text>
              {preview.unresolved.length
                ? `Неразрешённые foreign refs: ${preview.unresolved.length}. Numeric ID не подставляются автоматически.`
                : "Все выбранные artifact mappings проверены."}
            </Text>
            {preview.unresolved.length > 0 && (
              <Code block>{JSON.stringify(preview.unresolved, null, 2)}</Code>
            )}
            <Table captionSide="top">
              <Table.Caption>Первые 20 соответствий из {preview.idMap.length}</Table.Caption>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>Вид</Table.Th>
                  <Table.Th>Origin ID</Table.Th>
                  <Table.Th>Local ID</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {preview.idMap.slice(0, 20).map((entry, i) => (
                  <Table.Tr key={i}>
                    <Table.Td>{entry.origin.kind}</Table.Td>
                    <Table.Td>{entry.origin.id}</Table.Td>
                    <Table.Td>{entry.local.id}</Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
            <Button
              variant="subtle"
              onClick={() =>
                downloadJSON(`portable-${preview.projectId}-id-map.json`, preview.idMap)
              }
            >
              Скачать полный ID map
            </Button>
          </Stack>
        )}
        {error && (
          <Alert color="red" role="alert">
            {error}
          </Alert>
        )}
        <Text component="output" aria-live="polite">
          {busy ? "Выполняется portable-операция…" : message}
        </Text>
        {checkpoint?.committedProjectId && (
          <Anchor href={`/backend-projects/${checkpoint.committedProjectId}`}>
            Открыть импортированный проект
          </Anchor>
        )}
      </Stack>
    </Paper>
  );
}
