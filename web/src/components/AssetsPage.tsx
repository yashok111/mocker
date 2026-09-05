import type { InputHTMLAttributes, ReactElement } from "react";
import { useState } from "react";
import {
  Alert,
  Anchor,
  Button,
  Card,
  Group,
  Loader,
  Progress,
  Stack,
  Table,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { modals } from "@mantine/modals";
import { Dropzone, type FileWithPath } from "@mantine/dropzone";
import { IconAlertTriangle, IconTrash, IconUpload } from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import dayjs from "dayjs";
import {
  getListAssetsQueryKey,
  useDeleteAsset,
  useListAssets,
  useUploadAsset,
} from "@/api/generated/assets/assets.ts";
import { getGetWorkspaceQueryKey } from "@/api/generated/workspaces/workspaces.ts";
import type { AssetView } from "@/api/generated/schemas";
import { describeApiFailure, describeApiFailureDetailed } from "@/api/errors";

// AssetsPage is the ninth workspace tab, «Файлы» (A10): the files a mock can
// serve — DESIGN §32, shipped by A6 with three MCP tools and no screen under
// the A4 rule, and given a screen on the owner's word («сделай дешевые»,
// 2026-09-02). Three verbs, all existing routes: list, a raw-body PUT from a
// dropzone, a DELETE behind the workspace slug.
//
// The upload is the one place in web/src where a request body is a FILE and
// not JSON: api/client.ts's customFetch sets Content-Type: application/json
// on every non-GET, which would make the server store a JPEG as JSON (the
// media type is taken from the header). customFetch now keeps a Blob body's
// own type instead — see its comment — and the generated hook passes the
// File through as that Blob.

// assetNameRe mirrors assets.ValidName (internal/assets): what a name may
// be is decided there; this only pre-empts the 400 for the operator.
const ASSET_NAME_RE = /^[A-Za-z0-9._-]{1,128}$/;

/** suggestName turns a dropped file's name into one the server accepts:
 * spaces and anything outside the alphabet become "-", collapsed. */
export function suggestName(fileName: string): string {
  const cleaned = fileName
    .replace(/[^A-Za-z0-9._-]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 128);
  return cleaned === "" || cleaned === "." || cleaned === ".." ? "file" : cleaned;
}

function formatBytes(n: number): string {
  if (n >= 1024 * 1024) {
    return `${(n / (1024 * 1024)).toFixed(1)} МБ`;
  }
  if (n >= 1024) {
    return `${(n / 1024).toFixed(1)} КБ`;
  }
  return `${n} Б`;
}

export function AssetsPage({ id }: { id: number }): ReactElement {
  const assets = useListAssets(id);

  return (
    <div data-testid="assets-page">
      <Stack gap="md">
        <Title order={1}>Файлы</Title>
        <Text size="sm" c="dimmed">
          Файлы, которые мок отдаёт как есть: картинки, PDF, архивы — всё, что браузер не исполняет.
          Каждый доступен по своему адресу на хосте воркспейса, а в ответ попадает через{" "}
          <code>bodyRef: asset:имя</code> на закреплённом варианте или рецептом{" "}
          <code>asset_url</code>. Загрузка под уже занятым именем заменяет файл; ссылки на удалённый
          файл продолжают работать и отдают пустое тело с пометкой в трафике.
        </Text>
        <UploadCard
          id={id}
          existingNames={
            assets.data?.status === 200 ? assets.data.data.assets.map((a) => a.name) : []
          }
        />
        {assets.isPending ? (
          <Group gap="xs">
            <Loader size="sm" />
            <Text size="sm" component="output">
              Загрузка…
            </Text>
          </Group>
        ) : assets.isError ? (
          <Stack gap="sm" data-testid="assets-error">
            <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
              {describeApiFailure(assets.error)}
            </Alert>
            <Button
              variant="default"
              w="fit-content"
              onClick={() => void assets.refetch()}
              data-testid="assets-retry"
            >
              Повторить
            </Button>
          </Stack>
        ) : assets.data.status !== 200 ? (
          <Alert
            color="red"
            icon={<IconAlertTriangle size={18} />}
            role="alert"
            data-testid="assets-error"
          >
            {describeApiFailure(null)}
          </Alert>
        ) : (
          <AssetList
            id={id}
            rows={assets.data.data.assets}
            totalBytes={assets.data.data.totalBytes}
            maxAssetBytes={assets.data.data.maxAssetBytes}
            maxTotalBytes={assets.data.data.maxTotalBytes}
          />
        )}
      </Stack>
    </div>
  );
}

function UploadCard({ id, existingNames }: { id: number; existingNames: string[] }): ReactElement {
  const queryClient = useQueryClient();
  const [file, setFile] = useState<File | null>(null);
  const [name, setName] = useState("");
  const [uploaded, setUploaded] = useState<AssetView | null>(null);

  const upload = useUploadAsset({
    mutation: {
      onSuccess: (res) => {
        if (res.status !== 201 && res.status !== 200) {
          return;
        }
        setUploaded(res.data);
        setFile(null);
        setName("");
        void queryClient.invalidateQueries({ queryKey: getListAssetsQueryKey(id) });
        // An upload bumps the workspace revision (DESIGN §32.5).
        void queryClient.invalidateQueries({ queryKey: getGetWorkspaceQueryKey(id) });
      },
    },
  });

  function handleDrop(files: FileWithPath[]): void {
    const dropped = files[0];
    if (!dropped) {
      return;
    }
    setUploaded(null);
    setFile(dropped);
    setName(suggestName(dropped.name));
  }

  const nameError =
    name !== "" && !ASSET_NAME_RE.test(name) ? "Только латиница, цифры, . _ -" : null;
  // A21 (G13): an upload under an existing name replaces the bytes with no
  // checkpoint to come back to — the form knows the list and says so.
  const replaces = name !== "" && existingNames.includes(name);

  function submit(): void {
    if (!file || nameError || name === "") {
      return;
    }
    upload.mutate({ id, name, data: file });
  }

  return (
    <Card withBorder p="md" data-testid="asset-upload-form">
      <Stack gap="sm">
        <Dropzone
          onDrop={handleDrop}
          multiple={false}
          data-testid="asset-dropzone"
          inputProps={
            { "data-testid": "asset-file-input" } as InputHTMLAttributes<HTMLInputElement>
          }
        >
          <Group gap="xs" justify="center" py="md">
            <IconUpload size={20} />
            <Text size="sm">Перетащите файл сюда или нажмите, чтобы выбрать</Text>
          </Group>
        </Dropzone>
        {upload.isError ? (
          <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
            {describeApiFailureDetailed(upload.error)}
          </Alert>
        ) : null}
        {replaces ? (
          <Alert color="yellow" data-testid="asset-replace-warning">
            Файл «{name}» уже есть — загрузка заменит его содержимое; вернуть прежние байты будет
            нечем.
          </Alert>
        ) : null}
        {uploaded ? (
          <Text size="sm" data-testid="asset-uploaded">
            Загружен «<strong>{uploaded.name}</strong>» ({uploaded.mediaType},{" "}
            {formatBytes(uploaded.sizeBytes)}) —{" "}
            <Anchor href={uploaded.url} target="_blank" rel="noreferrer">
              {uploaded.url}
            </Anchor>
          </Text>
        ) : null}
        {file ? (
          <Group align="flex-end" gap="sm">
            <TextInput
              label="Имя файла на моке"
              description={`${file.type || "тип не определён браузером"}, ${formatBytes(file.size)}`}
              value={name}
              error={nameError}
              onChange={(e) => setName(e.currentTarget.value)}
              data-testid="asset-name"
              style={{ flex: 1 }}
            />
            <Button
              leftSection={<IconUpload size={16} />}
              loading={upload.isPending}
              disabled={nameError !== null || name === ""}
              onClick={submit}
              data-testid="asset-upload-submit"
            >
              {upload.isPending ? "Загружаем…" : "Загрузить"}
            </Button>
          </Group>
        ) : null}
      </Stack>
    </Card>
  );
}

function AssetList({
  id,
  rows,
  totalBytes,
  maxAssetBytes,
  maxTotalBytes,
}: {
  id: number;
  rows: AssetView[];
  totalBytes: number;
  maxAssetBytes: number;
  maxTotalBytes: number;
}): ReactElement {
  const queryClient = useQueryClient();
  const [deleteError, setDeleteError] = useState<{ name: string; message: string } | null>(null);

  const deleteAsset = useDeleteAsset({
    mutation: {
      onSuccess: () => {
        setDeleteError(null);
        void queryClient.invalidateQueries({ queryKey: getListAssetsQueryKey(id) });
        void queryClient.invalidateQueries({ queryKey: getGetWorkspaceQueryKey(id) });
      },
    },
  });

  function handleDelete(asset: AssetView): void {
    modals.open({
      title: "Удалить файл",
      children: (
        <DeleteAssetPrompt
          asset={asset}
          onCancel={() => modals.closeAll()}
          onConfirm={(slug) => {
            modals.closeAll();
            deleteAsset.mutate(
              { id, name: asset.name, data: { confirmSlug: slug } },
              {
                onError: (err) =>
                  setDeleteError({ name: asset.name, message: describeApiFailureDetailed(err) }),
              },
            );
          }}
        />
      ),
    });
  }

  const share = maxTotalBytes > 0 ? Math.min(100, (totalBytes / maxTotalBytes) * 100) : 0;

  return (
    <Stack gap="sm">
      <Group gap="md" align="center">
        <Text size="sm" data-testid="assets-usage">
          Занято {formatBytes(totalBytes)} из {formatBytes(maxTotalBytes)} · один файл до{" "}
          {formatBytes(maxAssetBytes)}
        </Text>
        <Progress value={share} w={160} size="sm" aria-label="доля занятого места" />
      </Group>
      {deleteError !== null ? (
        <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
          Не удалось удалить «{deleteError.name}»: {deleteError.message}
        </Alert>
      ) : null}
      {rows.length === 0 ? (
        <Text data-testid="assets-empty">
          Файлов пока нет. Загрузите первый через форму выше — и он сразу доступен по адресу на
          хосте воркспейса.
        </Text>
      ) : (
        <Card withBorder p={0} data-testid="asset-list">
          <Table fz="sm">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>имя</Table.Th>
                <Table.Th>тип</Table.Th>
                <Table.Th>размер</Table.Th>
                <Table.Th>обновлён</Table.Th>
                <Table.Th>sha256</Table.Th>
                <Table.Th>адрес</Table.Th>
                <Table.Th />
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {rows.map((asset) => (
                <Table.Tr key={asset.name} data-testid="asset-row">
                  <Table.Td>
                    <Text size="sm" fw={500}>
                      {asset.name}
                    </Text>
                  </Table.Td>
                  <Table.Td>{asset.mediaType}</Table.Td>
                  <Table.Td>{formatBytes(asset.sizeBytes)}</Table.Td>
                  <Table.Td>{dayjs.unix(asset.updatedAt).format("DD.MM.YYYY HH:mm")}</Table.Td>
                  <Table.Td>
                    <Text size="xs" ff="monospace" title={asset.sha256} data-testid="asset-sha">
                      {asset.sha256.slice(0, 12)}…
                    </Text>
                  </Table.Td>
                  <Table.Td>
                    <Anchor href={asset.url} target="_blank" rel="noreferrer" size="sm">
                      {asset.url}
                    </Anchor>
                  </Table.Td>
                  <Table.Td>
                    <Button
                      variant="default"
                      size="xs"
                      color="red"
                      leftSection={<IconTrash size={14} />}
                      loading={deleteAsset.isPending}
                      onClick={() => handleDelete(asset)}
                      data-testid="asset-delete"
                    >
                      Удалить
                    </Button>
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Card>
      )}
    </Stack>
  );
}

// DeleteAssetPrompt asks for the workspace slug, the same confirmation the
// admin route demands (DELETE .../assets/{name} takes confirmSlug and checks
// it inside its own transaction). The slug is typed, never pre-filled: a
// pre-filled confirmation confirms nothing.
function DeleteAssetPrompt({
  asset,
  onCancel,
  onConfirm,
}: {
  asset: AssetView;
  onCancel: () => void;
  onConfirm: (slug: string) => void;
}): ReactElement {
  const [slug, setSlug] = useState("");
  return (
    <Stack gap="sm">
      <Text size="sm">
        Удалить «{asset.name}»? Ни один чекпойнт не хранит файлы — вернуть его можно только новой
        загрузкой. Ссылки <code>bodyRef</code> и рецепты <code>asset_url</code>, которые его
        называют, останутся и будут отдавать пустое тело с пометкой <code>asset_missing</code>.
        Чтобы подтвердить, введите слаг воркспейса.
      </Text>
      <TextInput
        label="Слаг воркспейса"
        value={slug}
        onChange={(e) => setSlug(e.currentTarget.value)}
        data-testid="asset-delete-slug"
      />
      <Group gap="xs">
        <Button
          color="red"
          size="xs"
          disabled={slug.trim() === ""}
          onClick={() => onConfirm(slug.trim())}
          data-testid="asset-delete-confirm"
        >
          Удалить
        </Button>
        <Button variant="default" size="xs" onClick={onCancel} data-testid="asset-delete-cancel">
          Отмена
        </Button>
      </Group>
    </Stack>
  );
}
