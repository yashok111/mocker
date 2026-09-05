import { useState } from "react";
import type { InputHTMLAttributes, ReactElement } from "react";
import {
  Alert,
  Badge,
  Button,
  Card,
  Divider,
  Group,
  List,
  Loader,
  Stack,
  Text,
  TextInput,
  Title,
  UnstyledButton,
} from "@mantine/core";
import { Dropzone } from "@mantine/dropzone";
import type { FileWithPath } from "@mantine/dropzone";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertTriangle, IconRefresh, IconTrash, IconUpload } from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useForm } from "react-hook-form";
import { type } from "arktype";
import dayjs from "dayjs";
import {
  getListSpecsQueryKey,
  useDeleteSpec,
  useGetSpec,
  useGetSpecReport,
  useImportSpec,
  useListSpecOperations,
  useListSpecs,
} from "@/api/generated/specs/specs.ts";
import {
  getListResourceSuggestionsQueryKey,
  useRederiveSuggestions,
} from "@/api/generated/resources/resources.ts";
import type { ReportView, SpecView } from "@/api/generated/schemas";
import { ApiFailure } from "@/api/client";
import { describeApiFailure, describeApiFailureDetailed } from "@/api/errors";
import { arktypeResolver } from "@/validation/resolver";
import { userName } from "@/validation/name";

// SpecsPage is DESIGN §14 screen 3, the P1 subset: list, import (upload only —
// URL import and the schema-tree editor are P2/P3 per DESIGN §22), the import
// report, a spec's operations on demand, and delete behind a confirmation.
//
// The outermost element carries data-testid="specs-page" OUTSIDE every state
// switch below: web/src/routes/routes.test.tsx proves /specs is reachable by
// finding this marker alone, and a marker that only appeared in the success
// branch would make that reachability assertion depend on this screen's own
// fetch stub instead of on the route resolving (per the phase context, §0
// of the component contract).

const importForm = type({ name: userName });
type ImportForm = typeof importForm.infer;

// baseName strips a file's extension for the default import name — "petstore
// v1.json" becomes "petstore v1", the thing an operator actually wants to
// call the spec, editable before it is sent.
function baseName(fileName: string): string {
  const idx = fileName.lastIndexOf(".");
  return idx > 0 ? fileName.slice(0, idx) : fileName;
}

// abbreviateHash shows enough of the content hash to eyeball "same spec
// again?" without wrapping the row onto a second line.
function abbreviateHash(hash: string): string {
  return hash.length <= 12 ? hash : `${hash.slice(0, 12)}…`;
}

// createdAt arrives as Unix seconds (internal/admin/spec_handlers.go writes
// sp.CreatedAt.Unix()), not milliseconds — dayjs needs telling which.
function formatTimestamp(unixSeconds: number): string {
  return dayjs.unix(unixSeconds).format("DD.MM.YYYY HH:mm");
}

export function SpecsPage(): ReactElement {
  const specs = useListSpecs();
  const [selectedId, setSelectedId] = useState<number | null>(null);

  return (
    <div data-testid="specs-page">
      <Stack gap="md">
        <Title order={1}>Спеки</Title>
        <ImportSpecForm />
        {specs.isPending ? (
          // role on the Text, not the Group: the live region should be the
          // sentence a screen reader announces, not the flex box around it.
          <Group gap="xs">
            <Loader size="sm" />
            <Text size="sm" component="output">
              Загрузка…
            </Text>
          </Group>
        ) : specs.isError ? (
          <Stack gap="sm" data-testid="specs-error">
            <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
              {describeApiFailure(specs.error)}
            </Alert>
            <Button
              variant="default"
              w="fit-content"
              onClick={() => void specs.refetch()}
              data-testid="specs-retry"
            >
              Повторить
            </Button>
          </Stack>
        ) : specs.data.status !== 200 ? (
          <Alert
            color="red"
            icon={<IconAlertTriangle size={18} />}
            role="alert"
            data-testid="specs-error"
          >
            {describeApiFailure(null)}
          </Alert>
        ) : specs.data.data.length === 0 ? (
          // An empty list is an empty state, not an error — nothing has gone
          // wrong, nobody has imported a document yet.
          <Text data-testid="specs-empty">Спек пока нет — импортируйте документ выше</Text>
        ) : (
          <SpecList specs={specs.data.data} selectedId={selectedId} onSelect={setSelectedId} />
        )}
        {selectedId !== null ? (
          <SpecDetail id={selectedId} onDeleted={() => setSelectedId(null)} />
        ) : null}
      </Stack>
    </div>
  );
}

function ImportSpecForm(): ReactElement {
  const queryClient = useQueryClient();
  // droppedDocument is the file's own text, held until the operator confirms
  // (or edits) the name and submits — a drop is not itself an upload, since
  // the name field must stay editable per the phase brief.
  const [droppedDocument, setDroppedDocument] = useState<string | null>(null);
  const [imported, setImported] = useState<{
    id: number;
    duplicate: boolean;
    report: ReportView | null;
  } | null>(null);

  const {
    register,
    handleSubmit,
    reset,
    formState: { errors },
  } = useForm<ImportForm>({
    resolver: arktypeResolver(importForm),
    defaultValues: { name: "" },
  });

  const importSpec = useImportSpec({
    mutation: {
      onSuccess: (res) => {
        if (res.status !== 200 && res.status !== 201) {
          return;
        }
        setImported({ id: res.data.id, duplicate: res.status === 200, report: res.data.report });
        setDroppedDocument(null);
        reset({ name: "" });
        void queryClient.invalidateQueries({ queryKey: getListSpecsQueryKey() });
      },
    },
  });

  function handleDrop(files: FileWithPath[]): void {
    const file = files[0];
    if (!file) {
      return;
    }
    setImported(null);
    void file.text().then((text) => {
      setDroppedDocument(text);
      reset({ name: baseName(file.name) });
    });
  }

  return (
    <Card withBorder p="md" data-testid="spec-import-form">
      <Stack gap="sm">
        <Dropzone
          onDrop={handleDrop}
          multiple={false}
          data-testid="spec-dropzone"
          // Cast, not a plain literal: InputHTMLAttributes has no index
          // signature for data-* when used outside JSX's own attribute
          // typing, but this IS a real DOM attribute react-dropzone forwards
          // to the underlying <input type="file">, and it is the only handle
          // a test has on that element (react-dropzone drives it via a plain
          // change event, so userEvent.upload works against it directly).
          inputProps={{ "data-testid": "spec-file-input" } as InputHTMLAttributes<HTMLInputElement>}
        >
          <Group gap="xs" justify="center" py="md">
            <IconUpload size={20} />
            <Text size="sm">
              Перетащите файл спеки (.json или .yaml) сюда или нажмите, чтобы выбрать
            </Text>
          </Group>
        </Dropzone>
        {droppedDocument !== null ? (
          <form
            onSubmit={handleSubmit((values) =>
              importSpec.mutate({
                data: { name: values.name.trim(), source: "upload", document: droppedDocument },
              }),
            )}
          >
            <Stack gap="sm">
              <TextInput
                label="Название"
                data-testid="spec-import-name"
                error={errors.name?.message}
                {...register("name")}
              />
              <Button
                type="submit"
                w="fit-content"
                loading={importSpec.isPending}
                data-testid="spec-import-submit"
              >
                {importSpec.isPending ? "Импортируем…" : "Импортировать"}
              </Button>
            </Stack>
          </form>
        ) : null}
        {importSpec.isError ? (
          <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
            {describeApiFailureDetailed(importSpec.error)}
          </Alert>
        ) : null}
        {imported !== null ? (
          <Alert color={imported.duplicate ? "blue" : "green"} data-testid="spec-import-result">
            <Group justify="space-between" wrap="wrap">
              <span>
                {imported.duplicate
                  ? "Такой документ уже был импортирован раньше — используется существующая спека, вторая не добавлена"
                  : "Спека импортирована"}
              </span>
              <CreateWorkspaceWithSpec specId={imported.id} testId="spec-import-create-workspace" />
            </Group>
          </Alert>
        ) : null}
        {imported?.report ? <ReportSummary report={imported.report} /> : null}
      </Stack>
    </Card>
  );
}

function specStatus(basePath: string): string {
  return basePath === "" ? "/" : basePath;
}

// workspaceResourcesKeyRE is the shape getListWorkspaceResourcesQueryKey(id)
// always produces (resources.ts:508): the single-element query key
// ["/api/workspaces/{id}/resources"]. RederiveAction below invalidates by
// this PREDICATE rather than by a key built from an id — this screen has no
// workspace id to build one from (D4.1 forbids /specs from looking one up),
// and a rederive's result is visible to every workspace bound to the spec,
// not to one this screen could name (D8.2).
const workspaceResourcesKeyRE = /^\/api\/workspaces\/\d+\/resources$/;

function SpecList({
  specs,
  selectedId,
  onSelect,
}: {
  specs: SpecView[];
  selectedId: number | null;
  onSelect: (id: number) => void;
}): ReactElement {
  return (
    <Card withBorder p={0} data-testid="spec-list">
      <Stack gap={0}>
        {specs.map((spec) => (
          <UnstyledButton
            key={spec.id}
            data-testid="spec-row"
            onClick={() => onSelect(spec.id)}
            px="md"
            py="sm"
            w="100%"
            style={{
              borderTop: "1px solid var(--mantine-color-gray-3)",
              background: spec.id === selectedId ? "var(--mantine-color-gray-1)" : undefined,
            }}
          >
            <Group justify="space-between" wrap="nowrap" align="flex-start">
              <div>
                <Text size="sm" fw={500}>
                  {spec.name}{" "}
                  <Text span c="dimmed" size="xs">
                    {spec.version}
                  </Text>
                </Text>
                <Text size="xs" c="dimmed">
                  {spec.format} · {specStatus(spec.basePath)} · {formatTimestamp(spec.createdAt)} ·{" "}
                  {abbreviateHash(spec.hash)}
                </Text>
              </div>
              <RederiveAction id={spec.id} />
            </Group>
          </UnstyledButton>
        ))}
      </Stack>
    </Card>
  );
}

// RederiveAction is the per-row action D8.2 places on /specs: it re-runs
// derivation over an already-imported spec and reports what changed. The
// button sits inside the row's own UnstyledButton (the row's onClick selects
// the spec), so its own click must not bubble into that selection.
function RederiveAction({ id }: { id: number }): ReactElement {
  const queryClient = useQueryClient();
  const [error, setError] = useState<string | null>(null);

  const rederive = useRederiveSuggestions({
    mutation: {
      onSuccess: (res) => {
        setError(null);
        if (res.status !== 200) {
          return;
        }
        const body = res.data;
        notifications.show(
          body.changed
            ? {
                color: "green",
                message: `Пересобрано: поколение ${body.generation}, добавлено — ${body.added.length}, убрано — ${body.removed.length}`,
              }
            : { color: "blue", message: "Изменений нет" },
        );
        // By predicate, not by key: the roster this call changes is rendered
        // per-workspace (ResourcesPage.tsx) and this screen holds no
        // workspace id to build a key from — see workspaceResourcesKeyRE
        // above. The spec's own suggestion listing has a key this screen CAN
        // build, so that one is invalidated directly.
        void queryClient.invalidateQueries({
          predicate: (query) => {
            const key = query.queryKey[0];
            return typeof key === "string" && workspaceResourcesKeyRE.test(key);
          },
        });
        void queryClient.invalidateQueries({ queryKey: getListResourceSuggestionsQueryKey(id) });
      },
      onError: (err) => {
        // No invalidation on a refusal (409 stale_generation above all) —
        // nothing was written, so nothing downstream is stale.
        setError(describeApiFailureDetailed(err));
      },
    },
  });

  function handleClick(): void {
    setError(null);
    modals.openConfirmModal({
      title: "Пересобрать ресурсы",
      children: (
        <Text size="sm">
          Результат станет виден каждому воркспейсу, привязанному к этой спеке — не только текущему.
          Семейство, которое новое поколение уберёт, будет отмечено как «stranded» на следующем
          сбросе данных (reset-data).
        </Text>
      ),
      labels: { confirm: "Пересобрать", cancel: "Отмена" },
      confirmProps: { "data-testid": "spec-rederive-confirm" },
      onConfirm: () => rederive.mutate({ id }),
    });
  }

  return (
    <Stack gap={4} align="flex-end">
      <Button
        variant="default"
        size="xs"
        leftSection={<IconRefresh size={14} />}
        loading={rederive.isPending}
        onClick={(e) => {
          e.stopPropagation();
          handleClick();
        }}
        data-testid="spec-rederive"
      >
        Пересобрать ресурсы
      </Button>
      {error !== null ? (
        <Text size="xs" c="red" role="alert" data-testid="spec-rederive-error">
          {error}
        </Text>
      ) : null}
    </Stack>
  );
}

function ReportSummary({ report }: { report: ReportView }): ReactElement {
  return (
    <Stack gap="xs" data-testid="spec-report">
      <Group gap="sm">
        <Badge variant="light">{report.format}</Badge>
        <Text size="xs" c="dimmed">
          базовый путь {specStatus(report.basePath)} ({report.basePathOrigin})
        </Text>
      </Group>
      <Text size="xs">
        операций: {report.operations}
        {report.degraded > 0 ? `, из них с потерей точности: ${report.degraded}` : ""}
      </Text>
      {report.warnings.length > 0 ? (
        <List size="xs" data-testid="spec-report-warnings">
          {report.warnings.map((w) => (
            <List.Item key={`${w.pointer}-${w.code}`}>
              <Text size="xs" c="dimmed" span>
                {w.pointer}
              </Text>{" "}
              — {w.code}: {w.message}
            </List.Item>
          ))}
        </List>
      ) : null}
    </Stack>
  );
}

function SpecDetail({ id, onDeleted }: { id: number; onDeleted: () => void }): ReactElement {
  const queryClient = useQueryClient();
  const spec = useGetSpec(id);
  // 404 is the NORMAL answer for a spec with no stored report (§3.2) — retry
  // disabled so the global retry: 1 doesn't spend a round trip confirming
  // "still no report" before the UI can render "нет отчёта".
  const report = useGetSpecReport(id, { query: { retry: false } });
  const [opsRequested, setOpsRequested] = useState(false);
  const operations = useListSpecOperations(
    id,
    { limit: 500 },
    { query: { enabled: opsRequested } },
  );
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const deleteSpec = useDeleteSpec({
    mutation: {
      onSuccess: () => {
        setDeleteError(null);
        void queryClient.invalidateQueries({ queryKey: getListSpecsQueryKey() });
        onDeleted();
      },
    },
  });

  function handleDelete(name: string): void {
    modals.openConfirmModal({
      title: "Удалить спеку",
      children: (
        <Text size="sm">
          Удалить спеку «{name}»? Если она ещё привязана к воркспейсу, сервер откажет.
        </Text>
      ),
      labels: { confirm: "Удалить", cancel: "Отмена" },
      confirmProps: { color: "red", "data-testid": "spec-delete-confirm" },
      onConfirm: () => {
        deleteSpec.mutate(
          { id },
          // describeApiFailureDetailed, not describeApiFailure: a 409 here
          // names WHICH workspace still holds the spec, and that sentence is
          // the actionable content, per the phase brief.
          { onError: (err) => setDeleteError(describeApiFailureDetailed(err)) },
        );
      },
    });
  }

  return (
    <Card withBorder p="md" data-testid="spec-detail">
      {spec.isPending ? (
        <Group gap="xs">
          <Loader size="sm" />
          <Text size="sm" component="output">
            Загрузка…
          </Text>
        </Group>
      ) : spec.isError ? (
        <Stack gap="sm">
          <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
            {describeApiFailure(spec.error)}
          </Alert>
          <Button
            variant="default"
            w="fit-content"
            onClick={() => void spec.refetch()}
            data-testid="spec-detail-retry"
          >
            Повторить
          </Button>
        </Stack>
      ) : spec.data.status !== 200 ? (
        <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
          {describeApiFailure(null)}
        </Alert>
      ) : (
        // An IIFE, not `spec.data.data` read again below: TypeScript's
        // narrowing from `spec.data.status !== 200 ? … : …` does not survive
        // into a nested closure like the delete button's onClick, so a
        // second read there would widen `spec.data` back to the full
        // response union. Binding it once, here, keeps the narrowing local
        // to this branch without splitting the JSX into a separate component
        // just to get a typed parameter.
        (() => {
          const view = spec.data.data;
          return (
            <Stack gap="sm">
              <Group justify="space-between" wrap="nowrap">
                <div>
                  <Title order={3} data-testid="spec-detail-name">
                    {view.name}
                  </Title>
                  <Text size="xs" c="dimmed">
                    {view.version} · {view.format} · базовый путь {specStatus(view.basePath)}
                  </Text>
                  <CreateWorkspaceWithSpec specId={view.id} testId="spec-detail-create-workspace" />
                </div>
                <Button
                  variant="default"
                  color="red"
                  size="xs"
                  leftSection={<IconTrash size={16} />}
                  onClick={() => handleDelete(view.name)}
                  loading={deleteSpec.isPending}
                  data-testid="spec-delete"
                >
                  Удалить
                </Button>
              </Group>
              {deleteError !== null ? (
                <Alert
                  color="red"
                  icon={<IconAlertTriangle size={18} />}
                  role="alert"
                  data-testid="spec-delete-error"
                >
                  {deleteError}
                </Alert>
              ) : null}

              <Divider label="Отчёт об импорте" />
              {report.isPending ? (
                <Group gap="xs">
                  <Loader size="sm" />
                  <Text size="sm" component="output">
                    Загрузка…
                  </Text>
                </Group>
              ) : report.error instanceof ApiFailure && report.error.status === 404 ? (
                <Text size="sm" c="dimmed" data-testid="spec-report-missing">
                  нет отчёта
                </Text>
              ) : report.isError ? (
                <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
                  {describeApiFailure(report.error)}
                </Alert>
              ) : report.data.status !== 200 ? (
                <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
                  {describeApiFailure(null)}
                </Alert>
              ) : (
                <ReportSummary report={report.data.data} />
              )}

              <Divider label="Операции" />
              {!opsRequested ? (
                <Button
                  variant="default"
                  w="fit-content"
                  onClick={() => setOpsRequested(true)}
                  data-testid="spec-load-operations"
                >
                  Показать операции
                </Button>
              ) : operations.isPending ? (
                <Group gap="xs">
                  <Loader size="sm" />
                  <Text size="sm" component="output">
                    Загрузка…
                  </Text>
                </Group>
              ) : operations.isError ? (
                <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
                  {describeApiFailure(operations.error)}
                </Alert>
              ) : operations.data.status !== 200 ? (
                <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
                  {describeApiFailure(null)}
                </Alert>
              ) : (
                <Stack gap="xs" data-testid="spec-operations-list">
                  {operations.data.data.length === 500 ? (
                    // The server clamps at 500 and never truncates silently
                    // past that (§3.2) — this is the one place that limit is
                    // visible, so it has to say so rather than stopping at
                    // 500 rows unremarked.
                    <Text size="xs" c="orange" data-testid="spec-operations-capped">
                      Показаны первые 500 операций — их может быть больше
                    </Text>
                  ) : null}
                  {operations.data.data.map((op) => (
                    <Text size="xs" key={op.id} data-testid="spec-operation-row">
                      {op.method} {op.path}
                    </Text>
                  ))}
                </Stack>
              )}
            </Stack>
          );
        })()
      )}
    </Card>
  );
}

// CreateWorkspaceWithSpec is the next step after an import (A21, G3): the
// workspaces list with this spec preselected in the create card. Before it
// the trail died at «Спека импортирована» — no link, no hint that a
// workspace has to be created and bound.
function CreateWorkspaceWithSpec({ specId, testId }: { specId: number; testId: string }) {
  const navigate = useNavigate();
  return (
    <Button
      size="xs"
      variant="light"
      onClick={() => void navigate({ to: "/", search: { specId: String(specId) } })}
      data-testid={testId}
    >
      Создать воркспейс с этой спекой
    </Button>
  );
}
