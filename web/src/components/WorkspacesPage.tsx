import { useState } from "react";
import type { ChangeEvent, ReactElement } from "react";
import {
  Alert,
  Anchor,
  Button,
  Card,
  Group,
  Input,
  Loader,
  NativeSelect,
  Stack,
  Text,
  TextInput,
  Title,
  UnstyledButton,
} from "@mantine/core";
import { modals } from "@mantine/modals";
import { IconAlertTriangle, IconFileImport, IconPlus, IconTrash } from "@tabler/icons-react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { useForm } from "react-hook-form";
import { type } from "arktype";
import {
  getListWorkspacesQueryKey,
  useCreateWorkspace,
  useDeleteWorkspace,
  useImportWorkspace,
  useListWorkspaces,
} from "@/api/generated/workspaces/workspaces.ts";
import { useListSpecs } from "@/api/generated/specs/specs.ts";
import type {
  ImportWorkspaceView,
  SpecView,
  WorkspaceExportDocument,
  WorkspaceView,
} from "@/api/generated/schemas";
import { describeApiFailure, describeApiFailureDetailed } from "@/api/errors";
import { arktypeResolver } from "@/validation/resolver";
import { userName } from "@/validation/name";

// WorkspacesPage is DESIGN §14 screen 2, minus the auto-create half (P1d-2: no
// MOCKER_DEFAULT_SPEC handling here). It lists the caller's own workspaces,
// offers to create one, and lets one be deleted.

const createForm = type({ name: userName });
type CreateForm = typeof createForm.infer;

export function WorkspacesPage() {
  const workspaces = useListWorkspaces();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  // Second query, used only to pick the empty-state copy (DESIGN §14 screen
  // 2: "если спеки в базе нет вообще — не пустой список, а «спеки ещё нет»
  // со ссылкой на /specs"). It never gates this screen's own four states —
  // workspaces.isPending/isError above still drive those — so a slow or
  // failed specs probe degrades to the generic hint rather than blocking the
  // page a person came here to see.
  const specs = useListSpecs();

  if (workspaces.isPending) {
    return (
      // role on the Text, not the Group: the live region should be the
      // sentence a screen reader announces, not the flex box around it.
      <Group gap="xs">
        <Loader size="sm" />
        <Text size="sm" component="output">
          Загрузка…
        </Text>
      </Group>
    );
  }

  if (workspaces.isError) {
    return (
      <Stack gap="sm" data-testid="workspaces-error">
        <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
          {describeApiFailure(workspaces.error)}
        </Alert>
        <Button
          variant="default"
          w="fit-content"
          onClick={() => void workspaces.refetch()}
          data-testid="workspaces-retry"
        >
          Повторить
        </Button>
      </Stack>
    );
  }

  const list = workspaces.data.status === 200 ? workspaces.data.data : [];
  const isEmpty = list.length === 0;
  // A missing/failed/still-loading specs probe falls through to `false`
  // (specsEmpty), not to the "спеки ещё нет" copy — that message asserts a
  // fact ("there are zero specs in the whole database") this screen has not
  // actually confirmed yet, and asserting it wrongly would send someone to
  // /specs to import a document that already exists.
  const specsEmpty = specs.data?.status === 200 && specs.data.data.length === 0;

  // P4b's import half (2026-09-05, the A4 rule lifted for it — TransferPanel.tsx
  // has the record). The modal's content renders under ModalsProvider, which
  // is OUTSIDE RouterProvider in main.tsx, so the navigation to the new
  // workspace happens here on the page and not inside the form.
  function openImport(): void {
    const modalId = "workspace-import";
    modals.open({
      modalId,
      title: "Импорт воркспейса из файла",
      children: (
        <ImportWorkspaceForm
          onCancel={() => modals.close(modalId)}
          onImported={(view) => {
            modals.close(modalId);
            void queryClient.invalidateQueries({ queryKey: getListWorkspacesQueryKey() });
            void navigate({ to: "/workspaces/$id", params: { id: view.workspace.id } });
          }}
        />
      ),
    });
  }

  return (
    <Stack gap="md">
      <Group justify="space-between" align="center">
        <Title order={1}>Воркспейсы</Title>
        <Button
          variant="default"
          size="xs"
          leftSection={<IconFileImport size={16} />}
          onClick={openImport}
          data-testid="workspace-import"
        >
          Импорт из файла
        </Button>
      </Group>
      {isEmpty ? <Text data-testid="workspaces-empty">У вас пока нет воркспейсов</Text> : null}
      {/* Same position regardless of isEmpty, so this form never unmounts the
          instant the list it just populated stops being empty — losing that
          instance would also lose the "created: <slug>" message the moment the
          create it just reported succeeds. */}
      <CreateWorkspaceForm autoFocusName={isEmpty} />
      {isEmpty ? (
        specsEmpty ? (
          <Text size="xs" c="dimmed" data-testid="workspaces-empty-hint">
            Спеки ещё нет:{" "}
            <Anchor component={Link} to="/specs">
              загрузите её
            </Anchor>{" "}
            или попросите коллегу
          </Text>
        ) : (
          <Text size="xs" c="dimmed" data-testid="workspaces-empty-hint">
            Воркспейс можно создать и без спеки: пока он будет отвечать только на кастомные
            endpoint&apos;ы
          </Text>
        )
      ) : (
        <WorkspaceList workspaces={list} />
      )}
    </Stack>
  );
}

function CreateWorkspaceForm({ autoFocusName }: { autoFocusName: boolean }) {
  const [createdSlug, setCreatedSlug] = useState<string | null>(null);
  const queryClient = useQueryClient();
  const {
    register,
    handleSubmit,
    reset,
    formState: { errors },
  } = useForm<CreateForm>({
    resolver: arktypeResolver(createForm),
    defaultValues: { name: "" },
  });

  const createWorkspace = useCreateWorkspace({
    mutation: {
      onSuccess: (res) => {
        if (res.status !== 201) {
          return;
        }
        // The server derives the slug (and resolves a collision with a
        // deterministic suffix, alex / alex-2) — showing it is the whole point
        // per DESIGN §14 screen 2, never left silent.
        setCreatedSlug(res.data.slug);
        reset({ name: "" });
        void queryClient.invalidateQueries({ queryKey: getListWorkspacesQueryKey() });
      },
    },
  });

  return (
    <Card
      component="form"
      withBorder
      p="md"
      data-testid="workspace-create-form"
      onSubmit={handleSubmit((values) =>
        createWorkspace.mutate({ data: { name: values.name.trim() } }),
      )}
    >
      <Stack gap="sm">
        {createWorkspace.isError ? (
          <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
            {describeApiFailure(createWorkspace.error)}
          </Alert>
        ) : null}
        {createdSlug !== null ? (
          <Text size="sm" data-testid="workspace-created-slug">
            Создан воркспейс со slug&apos;ом «<strong>{createdSlug}</strong>»
          </Text>
        ) : null}
        <TextInput
          label="Название"
          data-autofocus={autoFocusName ? true : undefined}
          // Only when the list is EMPTY, i.e. this field is the single thing
          // there is to do on the screen — the case the rule's usability
          // objection (stealing focus from other content) does not describe.
          // oxlint-disable-next-line jsx-a11y/no-autofocus
          autoFocus={autoFocusName}
          data-testid="workspace-create-name"
          error={errors.name?.message}
          {...register("name")}
        />
        <Button
          type="submit"
          w="fit-content"
          leftSection={<IconPlus size={16} />}
          loading={createWorkspace.isPending}
          data-testid="workspace-create-submit"
        >
          {createWorkspace.isPending ? "Создаём…" : "Создать воркспейс"}
        </Button>
      </Stack>
    </Card>
  );
}

function specStatus(specId: number | null): string {
  return specId === null ? "спека не привязана" : `спека #${specId}`;
}

function WorkspaceList({ workspaces }: { workspaces: WorkspaceView[] }) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  // Named per-row rather than read off deleteWorkspace.error directly: the
  // mutation itself carries no memory of WHICH workspace it was deleting, and
  // the confirmation already names the workspace instead of leaving the person
  // to guess which row a bare error belongs to.
  const [deleteError, setDeleteError] = useState<{ name: string; message: string } | null>(null);

  const deleteWorkspace = useDeleteWorkspace({
    mutation: {
      onSuccess: () => {
        setDeleteError(null);
        void queryClient.invalidateQueries({ queryKey: getListWorkspacesQueryKey() });
      },
    },
  });

  function handleDelete(ws: WorkspaceView): void {
    modals.openConfirmModal({
      title: "Удалить воркспейс",
      children: <Text size="sm">Удалить воркспейс «{ws.name}»? Это действие необратимо.</Text>,
      labels: { confirm: "Удалить", cancel: "Отмена" },
      confirmProps: { color: "red", "data-testid": "workspace-delete-confirm" },
      onConfirm: () => {
        deleteWorkspace.mutate(
          { id: ws.id },
          { onError: (err) => setDeleteError({ name: ws.name, message: describeApiFailure(err) }) },
        );
      },
    });
  }

  return (
    <Stack gap="sm">
      {deleteError !== null ? (
        <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
          Не удалось удалить «{deleteError.name}»: {deleteError.message}
        </Alert>
      ) : null}
      <Card withBorder p={0} data-testid="workspace-list">
        <Stack gap={0}>
          {workspaces.map((ws) => (
            <Group
              key={ws.id}
              justify="space-between"
              wrap="nowrap"
              px="md"
              py="sm"
              style={{ borderTop: "1px solid var(--mantine-color-gray-3)" }}
            >
              <UnstyledButton
                style={{ flex: 1 }}
                data-testid="workspace-row"
                onClick={() => void navigate({ to: "/workspaces/$id", params: { id: ws.id } })}
              >
                <Text size="sm" fw={500}>
                  {ws.name}
                </Text>
                <Text size="xs" c="dimmed">
                  {ws.slug} · ревизия {ws.revision} · {specStatus(ws.specId)}
                </Text>
              </UnstyledButton>
              <Button
                variant="default"
                size="xs"
                color="red"
                leftSection={<IconTrash size={16} />}
                onClick={() => handleDelete(ws)}
                loading={deleteWorkspace.isPending}
                data-testid="workspace-delete"
              >
                Удалить
              </Button>
            </Group>
          ))}
        </Stack>
      </Card>
    </Stack>
  );
}

// readBundle parses the chosen file as the export document. The shape is not
// validated here beyond "a JSON object" — the server is the one reader of a
// mockerBundle document (it refuses a wrong version or a malformed section
// by name, 400 with a sentence), and a second, partial check here would
// only ever disagree with it.
async function readBundle(
  file: File,
): Promise<{ bundle: WorkspaceExportDocument } | { error: string }> {
  let text: string;
  try {
    text = await file.text();
  } catch {
    return { error: "Не удалось прочитать файл" };
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch {
    return { error: "Файл — не JSON" };
  }
  if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) {
    return { error: "В файле должен быть JSON-объект — документ экспорта" };
  }
  return { bundle: parsed as WorkspaceExportDocument };
}

// ImportWorkspaceForm is POST /api/workspaces/import's caller: the file, and
// the three things the operator may override — the name, the slug and the
// spec to bind instead of what the document names. It is its own component
// so the modal owns its mutation (the same reason ForkWorkspaceForm and
// DeclineConfirmedForm are), and a plain <input type="file"> rather than
// Dropzone: one JSON document, chosen, not dragged. The spec list is this
// form's OWN query, not a snapshot passed in: QueryClientProvider wraps
// ModalsProvider (main.tsx), so the hook works here, and a modal opened
// while the page's specs were still loading would otherwise offer nothing.
function ImportWorkspaceForm({
  onCancel,
  onImported,
}: {
  onCancel: () => void;
  onImported: (view: ImportWorkspaceView) => void;
}): ReactElement {
  const specsQuery = useListSpecs();
  const specs: SpecView[] = specsQuery.data?.status === 200 ? specsQuery.data.data : [];
  const [file, setFile] = useState<File | null>(null);
  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [specId, setSpecId] = useState("");
  const [fileError, setFileError] = useState<string | null>(null);

  const importWorkspace = useImportWorkspace({
    mutation: {
      onSuccess: (res) => {
        if (res.status === 201) {
          onImported(res.data);
        }
      },
    },
  });

  async function handleSubmit(): Promise<void> {
    if (file === null) {
      setFileError("Выберите файл экспорта");
      return;
    }
    const read = await readBundle(file);
    if ("error" in read) {
      setFileError(read.error);
      return;
    }
    setFileError(null);
    importWorkspace.mutate({
      data: {
        bundle: read.bundle,
        // Omitted, not sent empty: the server defaults the name to the
        // document's and uniquifies the slug from it.
        name: name.trim() === "" ? undefined : name.trim(),
        slug: slug.trim() === "" ? undefined : slug.trim(),
        specId: specId === "" ? undefined : Number(specId),
      },
    });
  }

  return (
    <Stack gap="sm" data-testid="workspace-import-form">
      <Text size="sm">
        Файл — то, что даёт «Скачать бандл» на обзоре воркспейса (или экспорт агентом). Из него
        создаётся новый воркспейс; спека берётся из файла, если она там есть, иначе — из уже
        загруженных по имени и версии, либо та, что выбрана ниже.
      </Text>
      {importWorkspace.isError ? (
        <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
          {describeApiFailureDetailed(importWorkspace.error)}
        </Alert>
      ) : null}
      <Input.Wrapper label="Файл экспорта (JSON)" error={fileError}>
        <Input
          component="input"
          type="file"
          accept="application/json,.json"
          onChange={(e: ChangeEvent<HTMLInputElement>) => {
            setFile(e.currentTarget.files?.[0] ?? null);
            setFileError(null);
          }}
          data-testid="workspace-import-file"
        />
      </Input.Wrapper>
      <TextInput
        label="Название (необязательно)"
        placeholder="как в файле"
        value={name}
        onChange={(e) => setName(e.currentTarget.value)}
        data-testid="workspace-import-name"
      />
      <TextInput
        label="Слаг (необязательно)"
        placeholder="выберет сервер"
        value={slug}
        onChange={(e) => setSlug(e.currentTarget.value)}
        data-testid="workspace-import-slug"
      />
      <NativeSelect
        label="Привязать спеку (необязательно)"
        value={specId}
        onChange={(e) => setSpecId(e.currentTarget.value)}
        data-testid="workspace-import-spec"
      >
        <option value="">как в файле</option>
        {specs.map((spec) => (
          <option key={spec.id} value={String(spec.id)}>
            {spec.name} (v{spec.version})
          </option>
        ))}
      </NativeSelect>
      <Group justify="flex-end">
        <Button type="button" variant="default" onClick={onCancel}>
          Отмена
        </Button>
        <Button
          type="button"
          loading={importWorkspace.isPending}
          onClick={() => void handleSubmit()}
          data-testid="workspace-import-submit"
        >
          Импортировать
        </Button>
      </Group>
    </Stack>
  );
}
