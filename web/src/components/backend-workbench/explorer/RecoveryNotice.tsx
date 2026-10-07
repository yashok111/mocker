import { useCallback, useEffect, useState } from "react";
import { Alert, Button, Group, Modal, Stack, Text } from "@mantine/core";
import styles from "./Explorer.module.css";
type Retained = { key: string; raw: string; storage: "local" | "session" };
export function retainedRequests(projectId: string): Retained[] {
  const result: Retained[] = [];
  for (const name of ["local", "session"] as const) {
    try {
      const storage = name === "local" ? localStorage : sessionStorage;
      for (let i = 0; i < storage.length; i++) {
        const key = storage.key(i);
        if (
          !key ||
          !key.split(":").includes(projectId) ||
          !/^(backend-(?:analysis-attempt|change-|proposal-|annotation|finding-review)|mocker:(?:diagram-pending|backend-import-recovery|backend-replay)|mocker-(?:materialization|portable-import|portable-export)-v1)/.test(
            key,
          )
        )
          continue;
        const raw = storage.getItem(key);
        if (raw) result.push({ key, raw, storage: name });
      }
    } catch {
      /* The notice must not silently erase a record when browser storage is blocked. */
    }
  }
  return result;
}
export function RecoveryNotice({ projectId }: { projectId: string }) {
  const [requests, setRequests] = useState(() => retainedRequests(projectId));
  const [opened, setOpened] = useState(false);
  const reload = useCallback(() => setRequests(retainedRequests(projectId)), [projectId]);
  useEffect(() => {
    window.addEventListener("storage", reload);
    window.addEventListener("focus", reload);
    return () => {
      window.removeEventListener("storage", reload);
      window.removeEventListener("focus", reload);
    };
  }, [reload]);
  if (!requests.length) return null;
  function download(request: Retained) {
    const url = URL.createObjectURL(
      new Blob(
        [
          JSON.stringify(
            {
              format: "backend-browser-recovery-v1",
              projectId,
              storageKey: request.key,
              storage: request.storage,
              requestJson: request.raw,
            },
            null,
            2,
          ),
        ],
        { type: "application/json" },
      ),
    );
    const link = document.createElement("a");
    link.href = url;
    link.download = "backend-original-request.json";
    link.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }
  return (
    <>
      <Alert color="yellow" className={styles.notice}>
        <Group justify="space-between">
          <Text size="sm">
            Сохранены незавершённые запросы прежнего интерфейса: {requests.length}. Их результат
            требует сверки.
          </Text>
          <Button size="compact-xs" variant="subtle" onClick={() => setOpened(true)}>
            Восстановление
          </Button>
        </Group>
      </Alert>
      <Modal
        opened={opened}
        onClose={() => setOpened(false)}
        title="Восстановление прежних запросов"
        size="lg"
      >
        <Stack>
          <Text size="sm">
            Исходные записи сохранены в этом браузере. Передайте агенту файл нужного запроса для
            сверки через MCP. При повторе агент должен использовать исходные параметры и ключ
            идемпотентности. Закрытие окна не удаляет записи и не повторяет операции.
          </Text>
          {requests.map((r, i) => (
            <Group key={`${r.storage}:${r.key}`} justify="space-between">
              <div>
                <Text size="sm">Запрос {i + 1}</Text>
                <Text size="xs" c="dimmed">
                  {r.key.split(":")[0]}
                </Text>
              </div>
              <Button variant="subtle" onClick={() => download(r)}>
                Скачать исходный запрос
              </Button>
            </Group>
          ))}
          <Text size="xs" c="dimmed">
            Файл содержит исходный запрос целиком. Он сохраняется локально; ссылка на карту этих
            данных не содержит.
          </Text>
          <Button variant="subtle" onClick={reload}>
            Перечитать состояние
          </Button>
        </Stack>
      </Modal>
    </>
  );
}
