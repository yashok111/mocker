import { useState } from "react";
import {
  Alert,
  Badge,
  Button,
  Checkbox,
  Code,
  Group,
  Loader,
  Modal,
  NativeSelect,
  NumberInput,
  Stack,
  Table,
  Text,
  TextInput,
} from "@mantine/core";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  clearProxyRecordings,
  deleteProxyRecording,
  getGetWorkspaceProxyQueryKey,
  getListProxyRecordingsQueryKey,
  putWorkspaceProxy,
  useGetWorkspaceProxy,
  useListProxyRecordings,
} from "@/api/generated/proxy/proxy";
import { useListWorkspaceOperations } from "@/api/generated/operations/operations";
import type { ProxyConfig, ProxyRecording } from "@/api/generated/schemas";
import { describeApiFailureDetailed } from "@/api/errors";

const modes = [
  { value: "off", label: "Моки по умолчанию" },
  { value: "passthrough", label: "Прокси" },
  { value: "record", label: "Прокси и запись" },
  { value: "replay", label: "Воспроизведение без сети" },
];
const policies = [
  { value: "default", label: "Режим воркспейса" },
  { value: "mock", label: "Мок" },
  { value: "proxy", label: "Прокси / replay" },
];

export function ProxyPanel({ id, slug }: { id: number; slug: string }) {
  return <WorkspaceProxyPanel key={id} id={id} slug={slug} />;
}

function WorkspaceProxyPanel({ id, slug }: { id: number; slug: string }) {
  const queryClient = useQueryClient();
  const settings = useGetWorkspaceProxy(id);
  const recordings = useListProxyRecordings(id);
  const operations = useListWorkspaceOperations(id);
  const [draft, setDraft] = useState<ProxyConfig | null>(null);
  const [notice, setNotice] = useState("");
  const [operationKey, setOperationKey] = useState("");
  const [selected, setSelected] = useState<ProxyRecording | null>(null);
  const [clearOpen, setClearOpen] = useState(false);
  const [confirmation, setConfirmation] = useState("");
  const current = settings.data?.status === 200 ? settings.data.data : null;
  const form = draft ?? current?.config;
  const refresh = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: getGetWorkspaceProxyQueryKey(id) }),
      queryClient.invalidateQueries({ queryKey: getListProxyRecordingsQueryKey(id) }),
    ]);
  };
  const save = useMutation({
    mutationFn: async (value: ProxyConfig) => {
      const result = await putWorkspaceProxy(id, value);
      if (result.status !== 200) throw new Error("Не удалось сохранить прокси");
      return result;
    },
    onSuccess: (result) => {
      queryClient.setQueryData(getGetWorkspaceProxyQueryKey(id), result);
      setDraft(null);
      setNotice("Настройки прокси сохранены");
    },
  });
  const remove = useMutation({
    mutationFn: async (rid: number) => {
      if (!current) throw new Error("Настройки не загружены");
      const result = await deleteProxyRecording(id, rid, { version: current.config.version });
      if (result.status !== 200) throw new Error("Не удалось удалить запись");
    },
    onSuccess: async () => {
      setSelected(null);
      await refresh();
      setNotice("Запись удалена");
    },
  });
  const clear = useMutation({
    mutationFn: async () => {
      if (!current) throw new Error("Настройки не загружены");
      const result = await clearProxyRecordings(id, {
        version: current.config.version,
        confirmSlug: confirmation,
      });
      if (result.status !== 200) throw new Error("Не удалось очистить записи");
    },
    onSuccess: async () => {
      setClearOpen(false);
      setConfirmation("");
      await refresh();
      setNotice("Записи очищены");
    },
  });
  const busy = save.isPending || remove.isPending || clear.isPending;
  const patch = (value: Partial<ProxyConfig>) => {
    if (form) {
      setDraft({ ...form, ...value });
      setNotice("");
      save.reset();
    }
  };
  const error = save.error ?? remove.error ?? clear.error;
  const rows = recordings.data?.status === 200 ? recordings.data.data.items : [];
  const keys = Array.from(
    new Set([
      ...(operations.data?.status === 200
        ? operations.data.data.map((op) => `${op.method} ${op.path}`)
        : []),
      ...Object.keys(form?.operations ?? {}),
    ]),
  ).sort();
  const discardAndReload = async () => {
    setDraft(null);
    save.reset();
    remove.reset();
    clear.reset();
    await refresh();
  };
  return (
    <Stack data-testid="proxy-panel" gap="md">
      {settings.isPending ? (
        <Loader size="sm" />
      ) : !current || !form ? (
        <>
          <Alert color="red">{describeApiFailureDetailed(settings.error)}</Alert>
          <Button onClick={() => void settings.refetch()}>Повторить загрузку прокси</Button>
        </>
      ) : (
        <>
          <Group>
            <Text fw={600}>Прокси реального API</Text>
            <Badge>{modes.find((m) => m.value === current.config.mode)?.label}</Badge>
            {draft && <Badge color="yellow">Не сохранено</Badge>}
          </Group>
          <Text size="sm" c="dimmed">
            Прокси отправляет запросы реальному серверу. Выбранные для него операции обходят правила
            ответов и диаграммы состояний. Воспроизведение использует записи и не обращается к
            серверу.
          </Text>
          {!current.allowedOrigins.length && (
            <Alert color="yellow">
              Исходящие запросы отключены администратором. Воспроизведение сохранённых ответов
              доступно.
            </Alert>
          )}
          <NativeSelect
            label="Режим прокси"
            data={modes}
            value={form.mode}
            disabled={busy}
            onChange={(e) => patch({ mode: e.currentTarget.value as ProxyConfig["mode"] })}
          />
          <TextInput
            label="Адрес upstream"
            description={
              current.allowedOrigins.length
                ? `Разрешены: ${current.allowedOrigins.join(", ")}. Путь запроса добавляется к этому адресу.`
                : "Адрес исходного API нужен также для выбора записей."
            }
            placeholder="https://api.example.com/v1"
            value={form.upstream}
            maxLength={2048}
            disabled={busy}
            onChange={(e) => patch({ upstream: e.currentTarget.value })}
          />
          <NumberInput
            label="Таймаут, секунды"
            min={1}
            max={120}
            value={form.timeoutSeconds}
            disabled={busy}
            onChange={(v) => patch({ timeoutSeconds: Number(v) || 15 })}
          />
          <Checkbox
            label="Передавать Authorization и X-Api-Key"
            checked={form.forwardAuth}
            disabled={busy}
            onChange={(e) => patch({ forwardAuth: e.currentTarget.checked })}
          />
          <Checkbox
            label="Передавать cookies и принимать Set-Cookie"
            checked={form.forwardCookies}
            disabled={busy}
            onChange={(e) => patch({ forwardCookies: e.currentTarget.checked })}
          />
          <NativeSelect
            label="Повторная запись того же запроса"
            data={[
              { value: "last", label: "Обновлять ответ" },
              { value: "first", label: "Оставлять первый ответ" },
            ]}
            value={form.overwrite}
            disabled={busy}
            onChange={(e) =>
              patch({ overwrite: e.currentTarget.value as ProxyConfig["overwrite"] })
            }
          />
          <Checkbox
            label="Заполнять сущности из записанных GET-ответов"
            description="Только подтверждённые семейства. Списки добавляют и обновляют строки; существующие строки не удаляются."
            checked={form.captureEntities}
            disabled={busy}
            onChange={(e) => patch({ captureEntities: e.currentTarget.checked })}
          />
          <Text size="sm">
            Сохраняются полные JSON-ответы с очисткой секретных полей. Ответы маршрутов авторизации
            не записываются. Путь, параметры, тело запроса и учётные данные участвуют в выборе
            записи; при несовпадении replay вернёт 404.
          </Text>
          <details>
            <summary>Режим отдельных операций ({keys.length})</summary>
            <Stack mt="sm">
              {operations.isError && (
                <Alert color="yellow">
                  Не удалось загрузить операции. Можно добавить правило вручную.
                </Alert>
              )}
              {keys.map((key) => (
                <NativeSelect
                  key={key}
                  label={`Режим ${key}`}
                  data={policies}
                  value={form.operations[key] ?? "default"}
                  disabled={busy}
                  onChange={(e) => {
                    const next = { ...form.operations };
                    if (e.currentTarget.value === "default") delete next[key];
                    else next[key] = e.currentTarget.value as "mock" | "proxy";
                    patch({ operations: next });
                  }}
                />
              ))}
              <Group align="end">
                <TextInput
                  label="Операция вручную"
                  placeholder="GET /users/{id}"
                  value={operationKey}
                  onChange={(e) => setOperationKey(e.currentTarget.value)}
                />
                <Button
                  disabled={
                    busy || !/^(GET|HEAD|POST|PUT|PATCH|DELETE|OPTIONS) \/\S*$/.test(operationKey)
                  }
                  onClick={() => {
                    patch({ operations: { ...form.operations, [operationKey]: "proxy" } });
                    setOperationKey("");
                  }}
                >
                  Добавить правило
                </Button>
              </Group>
            </Stack>
          </details>
          {error && (
            <Alert color="red" role="alert">
              {describeApiFailureDetailed(error)} При конфликте обновите настройки и повторите
              изменение.
            </Alert>
          )}
          {notice && (
            <Text component="output" c="green">
              {notice}
            </Text>
          )}
          <Group>
            <Button
              loading={save.isPending}
              disabled={busy && !save.isPending}
              onClick={() => save.mutate(form)}
            >
              Сохранить прокси
            </Button>
            <Button variant="default" disabled={busy} onClick={() => void discardAndReload()}>
              Сбросить изменения и обновить
            </Button>
          </Group>
          <Text size="sm" c="dimmed">
            Настройки подключения и записи хранятся на этой установке. Экспорт, копирование
            воркспейса и снимки сценариев их не переносят.
          </Text>
          <Group justify="space-between">
            <Text fw={600}>Записанные ответы ({rows.length}/500)</Text>
            <Group>
              <Button
                variant="default"
                size="xs"
                disabled={busy}
                onClick={() => void recordings.refetch()}
              >
                Обновить записи
              </Button>
              <Button
                color="red"
                variant="light"
                size="xs"
                disabled={busy || !!draft || !rows.length}
                onClick={() => setClearOpen(true)}
              >
                Очистить записи
              </Button>
            </Group>
          </Group>
          {draft && (
            <Text size="xs" c="dimmed">
              Сохраните или сбросьте изменения настроек перед удалением записей.
            </Text>
          )}
          {recordings.isError ? (
            <Alert color="red">{describeApiFailureDetailed(recordings.error)}</Alert>
          ) : recordings.isPending ? (
            <Loader size="sm" />
          ) : !rows.length ? (
            <Text c="dimmed">
              Записей пока нет. Включите запись и отправьте запросы на адрес мока.
            </Text>
          ) : (
            <Table.ScrollContainer minWidth={500}>
              <Table>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>Запрос</Table.Th>
                    <Table.Th>Статус</Table.Th>
                    <Table.Th>Записан</Table.Th>
                    <Table.Th />
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {rows.map((row) => (
                    <Table.Tr key={row.id}>
                      <Table.Td>
                        <Code>
                          {row.method} {row.path}
                        </Code>
                        {row.redacted && (
                          <Badge ml="xs" color="yellow">
                            Секреты скрыты
                          </Badge>
                        )}
                      </Table.Td>
                      <Table.Td>{row.status}</Table.Td>
                      <Table.Td>{new Date(row.updatedAt * 1000).toLocaleString()}</Table.Td>
                      <Table.Td>
                        <Button
                          size="xs"
                          variant="subtle"
                          aria-label={`Открыть запись ${row.id}`}
                          onClick={() => setSelected(row)}
                        >
                          Открыть
                        </Button>
                      </Table.Td>
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          )}
        </>
      )}
      <Modal
        opened={selected !== null}
        onClose={() => setSelected(null)}
        title={selected ? `${selected.method} ${selected.path}` : "Запись"}
        size="lg"
      >
        <Stack>
          <Text>
            {selected?.status} · {selected?.contentType}
          </Text>
          <Code block style={{ maxHeight: 400, overflow: "auto" }}>
            {!selected?.bodyText ? "Пустой ответ" : selected.bodyText}
          </Code>
          <Group>
            <Button variant="default" onClick={() => setSelected(null)}>
              Закрыть запись
            </Button>
            <Button
              color="red"
              disabled={busy || !!draft}
              loading={remove.isPending}
              onClick={() => {
                if (selected) remove.mutate(selected.id);
              }}
            >
              Удалить запись
            </Button>
          </Group>
          {remove.error && <Alert color="red">{describeApiFailureDetailed(remove.error)}</Alert>}
        </Stack>
      </Modal>
      <Modal opened={clearOpen} onClose={() => setClearOpen(false)} title="Очистить записи">
        <Stack>
          <Text>
            Все записанные ответы будут удалены. Сущности останутся. Для подтверждения введите{" "}
            {slug}.
          </Text>
          <TextInput
            label="Slug для подтверждения"
            value={confirmation}
            onChange={(e) => setConfirmation(e.currentTarget.value)}
          />
          <Button
            color="red"
            disabled={confirmation !== slug || busy}
            loading={clear.isPending}
            onClick={() => clear.mutate()}
          >
            Подтвердить очистку
          </Button>
          {clear.error && <Alert color="red">{describeApiFailureDetailed(clear.error)}</Alert>}
        </Stack>
      </Modal>
    </Stack>
  );
}
