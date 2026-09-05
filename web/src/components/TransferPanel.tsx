import { useState } from "react";
import type { ReactElement } from "react";
import {
  Alert,
  Anchor,
  Button,
  Checkbox,
  Group,
  Stack,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { modals } from "@mantine/modals";
import { IconAlertTriangle, IconCopy, IconDownload } from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import {
  getExportWorkspaceQueryOptions,
  getListWorkspacesQueryKey,
  useForkWorkspace,
} from "@/api/generated/workspaces/workspaces.ts";
import type { WorkspaceView } from "@/api/generated/schemas";
import { describeApiFailureDetailed } from "@/api/errors";

// TransferPanel is P4b's screen half, drawn on 2026-09-05 when the owner
// lifted the A4 rule for it («надо бы доделать страницы», a Russian string
// quoted as data) — the latest such lift, after /guide (A7), the streaming
// screens (P6e), the «Файлы» tab (A10) and the «Контракт» tab (P7b). It sits on the
// overview because both verbs are about the workspace AS A WHOLE:
//
// - «Скачать» is GET .../export answered as a file. The route returns the
//   mockerBundle document as JSON; there is no Content-Disposition on the
//   wire (an agent reads it as a body, not a download), so the file is made
//   here from the parsed document and offered through a Blob URL. It goes
//   through the generated client and not a bare <a href> — a plain anchor
//   would skip client.ts's error envelope and show the browser's JSON
//   viewer on a 404 instead of a sentence.
// - «Копировать» is POST .../fork inside one installation: a modal with the
//   copy's name and slug (both optional on the wire; the server defaults
//   the name to «<name> (копия)» and uniquifies the slug) and the one
//   choice that changes what the copy contains — the entity rows.
//
// The import half is on the workspaces list (WorkspacesPage.tsx): a NEW
// workspace comes out of it, so its home is where workspaces are created.
export function TransferPanel({ workspace }: { workspace: WorkspaceView }): ReactElement {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [includeData, setIncludeData] = useState(false);
  const [includeSpec, setIncludeSpec] = useState(false);
  const [downloading, setDownloading] = useState(false);
  const [downloadError, setDownloadError] = useState<string | null>(null);

  async function download(): Promise<void> {
    setDownloading(true);
    setDownloadError(null);
    try {
      // fetchQuery with the generated options, not useQuery: a download is
      // an action, and a query mounted on the panel would fire the export
      // on every visit to the overview.
      const res = await queryClient.fetchQuery(
        getExportWorkspaceQueryOptions(workspace.id, {
          includeData: includeData || undefined,
          includeSpec: includeSpec || undefined,
        }),
      );
      if (res.status !== 200) {
        setDownloadError(describeApiFailureDetailed(null));
        return;
      }
      saveJSON(`mocker-${workspace.slug}.json`, res.data);
    } catch (err) {
      setDownloadError(describeApiFailureDetailed(err));
    } finally {
      setDownloading(false);
    }
  }

  function openFork(): void {
    const modalId = `workspace-fork-${workspace.id}`;
    modals.open({
      modalId,
      title: `Копировать «${workspace.name}»`,
      children: (
        <ForkWorkspaceForm
          workspace={workspace}
          onCancel={() => modals.close(modalId)}
          // The navigation happens HERE, not in the form: modals.open renders
          // its children under ModalsProvider, which sits OUTSIDE
          // RouterProvider in main.tsx, so a useNavigate inside the modal
          // has no router to navigate with. The panel is inside the route.
          onForked={(copy) => {
            modals.close(modalId);
            void queryClient.invalidateQueries({ queryKey: getListWorkspacesQueryKey() });
            void navigate({ to: "/workspaces/$id", params: { id: copy.id } });
          }}
        />
      ),
    });
  }

  return (
    <div data-testid="transfer-panel">
      <Title order={2}>Перенос</Title>
      <Stack gap="sm" mt="sm">
        <Text size="sm" c="dimmed">
          Бандл — конфигурация воркспейса одним JSON-файлом: настройки, правки операций, кастомные
          endpoint&apos;ы, решения по ресурсам. Из него на этой или другой установке создаётся новый
          воркспейс (
          <Anchor
            href="/"
            onClick={(e) => {
              e.preventDefault();
              void navigate({ to: "/" });
            }}
            data-testid="transfer-import-link"
          >
            «Импорт из файла» в списке воркспейсов
          </Anchor>
          ). Сценарии и файлы (вкладка «Файлы») в бандл не входят: сценарий экспортируют,
          активировав его; файлы после импорта загружают заново. Копия внутри этой установки (кнопка
          справа) сценарии и файлы переносит.
        </Text>
        {downloadError !== null ? (
          <Alert
            color="red"
            icon={<IconAlertTriangle size={18} />}
            role="alert"
            data-testid="transfer-export-error"
          >
            {downloadError}
          </Alert>
        ) : null}
        <Group gap="md" align="center">
          <Checkbox
            label="со строками ресурсов"
            checked={includeData}
            onChange={(e) => setIncludeData(e.currentTarget.checked)}
            data-testid="transfer-export-data"
          />
          <Checkbox
            label="со спекой внутри"
            checked={includeSpec}
            onChange={(e) => setIncludeSpec(e.currentTarget.checked)}
            data-testid="transfer-export-spec"
          />
        </Group>
        <Group gap="xs">
          <Button
            variant="default"
            leftSection={<IconDownload size={16} />}
            loading={downloading}
            onClick={() => void download()}
            data-testid="transfer-export"
          >
            Скачать бандл
          </Button>
          <Button
            variant="default"
            leftSection={<IconCopy size={16} />}
            onClick={openFork}
            data-testid="transfer-fork"
          >
            Копировать воркспейс
          </Button>
        </Group>
      </Stack>
    </div>
  );
}

// saveJSON hands the document to the browser as a download. Exported for the
// test, which stubs URL.createObjectURL (jsdom has none) and asserts the
// name and the bytes rather than a click it cannot observe.
export function saveJSON(filename: string, document: unknown): void {
  const blob = new Blob([JSON.stringify(document, null, 2)], { type: "application/json" });
  const url = URL.createObjectURL(blob);
  const a = window.document.createElement("a");
  a.href = url;
  a.download = filename;
  // Attached for the click (Safari ignores a click on a detached anchor)
  // and revoked LATER, not in a finally: Firefox starts the download
  // asynchronously and a URL revoked on the same tick can cancel it.
  window.document.body.appendChild(a);
  a.click();
  a.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 60_000);
}

// ForkWorkspaceForm is its own component for the reason DeclineConfirmedForm
// (ResourcesPage.tsx) is: the modal wants a mutation instance of its own so
// its pending/error state is not the panel's. On success the panel goes to
// the copy — the whole point of forking is to work in it next.
function ForkWorkspaceForm({
  workspace,
  onCancel,
  onForked,
}: {
  workspace: WorkspaceView;
  onCancel: () => void;
  onForked: (copy: WorkspaceView) => void;
}): ReactElement {
  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  // Checked by default because the SERVER's default is true (an omitted
  // includeData copies the rows — internal/admin/transfer_handlers.go), and
  // sent as an explicit boolean either way: an unchecked box that sent
  // nothing would copy the rows it promised not to.
  const [includeData, setIncludeData] = useState(true);

  const forkWorkspace = useForkWorkspace({
    mutation: {
      onSuccess: (res) => {
        if (res.status !== 201) {
          return;
        }
        onForked(res.data);
      },
    },
  });

  function handleSubmit(): void {
    forkWorkspace.mutate({
      id: workspace.id,
      data: {
        // Omitted, not sent empty: the server's own defaults — the source's
        // name plus « (копия)», a slug uniquified from the name — are what
        // happens on omission, and an empty string would be a 400 instead.
        name: name.trim() === "" ? undefined : name.trim(),
        slug: slug.trim() === "" ? undefined : slug.trim(),
        includeData,
      },
    });
  }

  return (
    <Stack gap="sm" data-testid="workspace-fork-form">
      <Text size="sm">
        Копия получает всю конфигурацию, сценарии и файлы; адрес у неё свой. Строки ресурсов
        копируются, пока стоит галочка ниже.
      </Text>
      {forkWorkspace.isError ? (
        <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
          {describeApiFailureDetailed(forkWorkspace.error)}
        </Alert>
      ) : null}
      <TextInput
        label="Название (необязательно)"
        placeholder={`${workspace.name} (копия)`}
        value={name}
        onChange={(e) => setName(e.currentTarget.value)}
        data-testid="workspace-fork-name"
      />
      <TextInput
        label="Слаг (необязательно)"
        placeholder="выберет сервер"
        value={slug}
        onChange={(e) => setSlug(e.currentTarget.value)}
        data-testid="workspace-fork-slug"
      />
      <Checkbox
        label="со строками ресурсов"
        checked={includeData}
        onChange={(e) => setIncludeData(e.currentTarget.checked)}
        data-testid="workspace-fork-data"
      />
      <Group justify="flex-end">
        <Button type="button" variant="default" onClick={onCancel}>
          Отмена
        </Button>
        <Button
          type="button"
          loading={forkWorkspace.isPending}
          onClick={handleSubmit}
          data-testid="workspace-fork-submit"
        >
          Копировать
        </Button>
      </Group>
    </Stack>
  );
}
