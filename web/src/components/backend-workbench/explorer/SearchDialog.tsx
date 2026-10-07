import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useDebouncedValue } from "@mantine/hooks";
import { Modal, TextInput, Text, Button, Loader, Group } from "@mantine/core";
import { IconSearch } from "@tabler/icons-react";
import type { BackendReadTarget } from "@/api/generated/schemas";
import { backendReadTargetKey } from "../backendReadTargets";
import { readExplore } from "./reads";
import { kindName, sourceNode, nodeSubtitle, type MapNode } from "./model";
import styles from "./Explorer.module.css";
export function SearchDialog({
  projectId,
  target,
  opened,
  query,
  onQuery,
  onClose,
  onOpen,
}: {
  projectId: string;
  target: BackendReadTarget;
  opened: boolean;
  query: string;
  onQuery: (q: string) => void;
  onClose: () => void;
  onOpen: (n: MapNode) => void;
}) {
  const [text] = useDebouncedValue(query, 200);
  const [paging, setPaging] = useState({ query: text, cursor: "" });
  const cursor = paging.query === text ? paging.cursor : "";
  const setCursor = (cursor: string) => setPaging({ query: text, cursor });
  const result = useQuery({
    queryKey: ["workbench-search", projectId, backendReadTargetKey(target), text, cursor],
    enabled: opened && text.trim().length > 0,
    retry: false,
    queryFn: ({ signal }) =>
      readExplore(projectId, { target, mode: "objects", search: text, limit: 30, cursor }, signal),
  });
  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title="Поиск по всему проекту"
      size="lg"
      closeButtonProps={{ "aria-label": "Закрыть поиск" }}
    >
      <TextInput
        data-autofocus
        aria-label="Название, путь или символ"
        placeholder="Название, путь API, таблица, поле…"
        value={query}
        onChange={(e) => onQuery(e.currentTarget.value)}
        leftSection={<IconSearch size={17} />}
      />
      <Text size="xs" c="dimmed" mt="sm" mb="md">
        Все объекты выбранной версии, включая нераспределённые на карте.
      </Text>
      {result.isFetching ? <Loader size="sm" aria-label="Ищем объекты" /> : null}
      {result.isError ? (
        <Text c="red" role="alert">
          Не удалось выполнить поиск.{" "}
          <Button variant="subtle" onClick={() => void result.refetch()}>
            Повторить
          </Button>
        </Text>
      ) : null}
      {result.data?.nodes.map((raw) => {
        const n = sourceNode(raw);
        return (
          <button key={n.id} className={styles.listRow} onClick={() => onOpen(n)}>
            <span>
              <strong>{n.name}</strong>
              <span className={styles.subtitle} style={{ display: "block" }}>
                {nodeSubtitle(n)}
              </span>
            </span>
            <Text size="xs" c="dimmed">
              {kindName(n.kind)}
            </Text>
          </button>
        );
      })}
      {result.data && (
        <Group justify="space-between" mt="md">
          <Text size="xs" c="dimmed">
            Найдено: {result.data.total}
          </Text>
          {cursor && (
            <Button size="compact-xs" variant="subtle" onClick={() => setCursor("")}>
              К началу
            </Button>
          )}
          {result.data.nextCursor && (
            <Button
              variant="subtle"
              size="compact-xs"
              onClick={() => setCursor(result.data!.nextCursor)}
            >
              Дальше
            </Button>
          )}
        </Group>
      )}
    </Modal>
  );
}
