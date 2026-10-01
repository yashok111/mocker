import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Alert,
  Badge,
  Button,
  Group,
  NativeSelect,
  Paper,
  Stack,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { listBackendSavedViews } from "@/api/generated/backend-projects/backend-projects";
import type { BackendSavedViewSession } from "./useBackendSavedViewSession";
import type { BackendSourcePin } from "./backendFlowReads";
import { useDatabaseCancellation } from "./backendDatabaseReads";
export function BackendSavedViews({
  projectId,
  session,
  onOpen,
}: {
  projectId: string;
  session: BackendSavedViewSession;
  onOpen: (pin: BackendSourcePin) => void;
}) {
  const [selected, setSelected] = useState("");
  const [cursors, setCursors] = useState([""]);
  const cursor = cursors.at(-1) ?? "";
  const key = ["backend-saved-views", projectId, cursor];
  useDatabaseCancellation(key);
  const query = useQuery({
    queryKey: key,
    retry: false,
    queryFn: async ({ signal }) => {
      const response = await listBackendSavedViews(
        projectId,
        { limit: 50, ...(cursor ? { cursor } : {}) },
        { signal },
      );
      signal.throwIfAborted();
      if (response.status !== 200) throw new Error("Не удалось загрузить сохранённые виды");
      return response.data;
    },
  });
  const unavailable =
    session.busy || !!session.pending || session.preview || !session.state || !session.name.trim();
  return (
    <Paper withBorder p="md">
      <Stack aria-label="Сохранённые виды">
        <Title order={2}>Сохранённые виды</Title>
        <NativeSelect
          label="Сохранённый вид"
          value={selected}
          onChange={(e) => setSelected(e.currentTarget.value)}
          data={[
            { value: "", label: "Выберите вид" },
            ...(query.data?.items ?? []).map((item) => ({
              value: item.id,
              label: `${item.kind === "flow" ? "Flow" : "База данных"} · ${item.name} · версия ${item.version}`,
            })),
          ]}
        />
        <Group>
          <Button
            variant="default"
            disabled={!selected || session.busy || !!session.pending}
            onClick={() => {
              const item = query.data?.items.find((i) => i.id === selected);
              if (item) onOpen({ viewId: item.id, viewVersion: item.version });
            }}
          >
            Открыть
          </Button>
          <Button
            variant="subtle"
            disabled={!selected || session.busy || !!session.pending}
            onClick={() => onOpen({ viewId: selected })}
          >
            Открыть последний вид
          </Button>
        </Group>
        {query.isError && (
          <Alert color="red" role="alert">
            Ошибка списка видов
            <Button onClick={() => void query.refetch()}>Повторить список видов</Button>
          </Alert>
        )}
        {(cursors.length > 1 || query.data?.nextCursor) && (
          <Group>
            <Button
              disabled={cursors.length === 1 || query.isFetching}
              onClick={() => setCursors((v) => v.slice(0, -1))}
            >
              Предыдущие виды
            </Button>
            <Button
              disabled={!query.data?.nextCursor || query.isFetching}
              onClick={() => {
                if (query.data?.nextCursor) setCursors((v) => [...v, query.data!.nextCursor]);
              }}
            >
              Следующие виды
            </Button>
          </Group>
        )}
        {session.state ? (
          <Badge>
            Текущий вид: {session.state.kind === "flow" ? "Flow" : "База данных"}
            {session.saved ? ` · версия ${session.saved.version}` : " · новый"}
            {session.dirty ? " · есть изменения" : ""}
          </Badge>
        ) : (
          <Text size="sm">
            Выберите «Сохранить этот Flow» или «Сохранить эту базу данных» в рабочей области.
          </Text>
        )}
        <TextInput
          label="Название вида"
          value={session.name}
          maxLength={200}
          onChange={(e) => session.setName(e.currentTarget.value)}
        />
        {session.error && (
          <Alert color="red" role="alert">
            {session.error}
          </Alert>
        )}
        {session.pending && !session.busy && (
          <Text component="output">
            Результат сохранения неизвестен. Повтор отправит тот же запрос и ключ.
          </Text>
        )}
        {session.conflict && (
          <Text component="output">
            Версия изменилась. Локальные изменения сохранены; загрузите текущий вид или сохраните
            новый.
          </Text>
        )}
        <Group>
          <Button
            disabled={unavailable || session.conflict || (!!session.saved && !session.bound)}
            onClick={() => void session.save()}
          >
            Сохранить
          </Button>
          <Button variant="default" disabled={unavailable} onClick={() => void session.save(true)}>
            Сохранить как новый
          </Button>
          {session.pending && !session.busy && (
            <Button disabled={session.preview} onClick={() => void session.retry()}>
              Повторить сохранение
            </Button>
          )}
          {session.conflict && (
            <Button variant="default" disabled={session.busy} onClick={() => void session.reload()}>
              Загрузить текущий вид
            </Button>
          )}
        </Group>
        <Text size="xs" c="dimmed">
          Восстановление начинается с первой страницы. Координаты других страниц применятся при их
          открытии. Незавершённый текст поиска не сохраняется.
        </Text>
      </Stack>
    </Paper>
  );
}

export function SavedViewLayoutControls({
  layout,
  label,
  names,
}: {
  names?: Map<string, string>;
  layout: import("./backendSavedViewLayout").SavedViewLayoutController;
  label: string;
}) {
  const [selected, setSelected] = useState("");
  const nodes = layout.display?.nodes ?? [];
  const node = nodes.find((n) => n.id === selected) ?? nodes[0];
  return (
    <Stack gap="xs" aria-label={`Расположение ${label}`}>
      <Group>
        <Button
          variant="default"
          disabled={!layout.display || layout.preview}
          onClick={layout.startPreview}
        >
          Предпросмотр автораскладки
        </Button>
        {layout.preview && (
          <>
            <Button onClick={layout.apply}>Применить расположение</Button>
            <Button variant="default" onClick={layout.cancelPreview}>
              Отменить предпросмотр
            </Button>
          </>
        )}
        <Button variant="subtle" disabled={!layout.canUndo || layout.preview} onClick={layout.undo}>
          Отменить расположение
        </Button>
        <Button variant="subtle" disabled={layout.preview} onClick={layout.clear}>
          Очистить координаты
        </Button>
      </Group>
      <NativeSelect
        label={`Карточка ${label} для перемещения`}
        value={node?.id ?? ""}
        data={nodes.map((n) => ({ value: n.id, label: names?.get(n.id) ?? n.id }))}
        onChange={(e) => setSelected(e.currentTarget.value)}
      />
      {node && (
        <>
          <Text size="sm" component="output">
            Координаты карточки: x {node.x}, y {node.y}
          </Text>
          <Group>
            {[
              { text: "Влево", x: -20, y: 0 },
              { text: "Вправо", x: 20, y: 0 },
              { text: "Вверх", x: 0, y: -20 },
              { text: "Вниз", x: 0, y: 20 },
            ].map((move) => (
              <Button
                key={move.text}
                variant="default"
                disabled={layout.preview}
                aria-label={`${move.text} карточку ${label}`}
                onClick={() => layout.move(node.id, node.x + move.x, node.y + move.y)}
              >
                {move.text}
              </Button>
            ))}
          </Group>
        </>
      )}
      {layout.preview && (
        <Text component="output">Предпросмотр расположения: сохранение временно недоступно.</Text>
      )}
      {layout.error && (
        <Alert color="red" role="alert">
          {layout.error}
        </Alert>
      )}
    </Stack>
  );
}
