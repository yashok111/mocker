import { useState, type ReactElement } from "react";
import { Alert, Button, Group, Loader, Modal, NativeSelect, Stack, Text } from "@mantine/core";
import { getApiDesign, useListApiDesigns } from "@/api/generated/api-designs/api-designs";
import { describeApiFailureDetailed } from "@/api/errors";
import { parseBrowserSafeJson } from "@/api/preciseJson";
import { isRecord } from "../api-designer/documentModel";
import { importContract } from "./canvasModel";
import type { CanvasContract } from "./types";

export function CanvasApiPicker({
  onClose,
  onImport,
  onImportDesign,
}: {
  onClose: () => void;
  onImport?: (contract: CanvasContract) => void;
  onImportDesign?: (input: { designId: number; mode: "copy" | "linked" }) => void;
}): ReactElement {
  const query = useListApiDesigns();
  const [id, setId] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [mode, setMode] = useState<"copy" | "linked">("copy");
  const rows = query.data?.status === 200 ? query.data.data.designs : [];

  async function load(): Promise<void> {
    if (id === "" || loading) return;
    setLoading(true);
    setError("");
    try {
      if (onImportDesign) {
        onImportDesign({ designId: Number(id), mode });
        onClose();
        return;
      }
      const response = await getApiDesign(Number(id));
      if (response.status !== 200) throw new Error("Проект недоступен");
      const { design, draft } = response.data;
      const document = parseBrowserSafeJson(draft.document);
      if (!isRecord(document)) throw new Error("Корень OpenAPI должен быть объектом");
      onImport?.(
        importContract(design.name, document, {
          designId: design.id,
          revisionId: draft.id,
        }),
      );
      onClose();
    } catch (cause) {
      setError(describeApiFailureDetailed(cause));
    } finally {
      setLoading(false);
    }
  }

  return (
    <Modal
      opened
      onClose={loading ? () => {} : onClose}
      title="Добавить существующий API"
      closeButtonProps={{ "aria-label": "Закрыть выбор API", disabled: loading }}
    >
      <Stack gap="md">
        <Text size="sm" c="dimmed">
          {onImportDesign
            ? "Копия редактируется независимо. Связанный режим сохраняет правки в общий API-проект."
            : "В сценарии сохранится локальная копия черновика выбранного API. Изменения копии останутся в этом браузере."}
        </Text>
        {query.isPending ? <Loader aria-label="Загружаем проекты API" /> : null}
        {query.isError ? (
          <Alert color="red" role="alert">
            {describeApiFailureDetailed(query.error)}
          </Alert>
        ) : null}
        <NativeSelect
          label="Проект API"
          value={id}
          onChange={(event) => setId(event.currentTarget.value)}
          disabled={loading || query.isPending}
          data={[
            { value: "", label: rows.length ? "Выберите проект" : "Нет доступных проектов" },
            ...rows.map((design) => ({ value: String(design.id), label: design.name })),
          ]}
        />
        {onImportDesign ? (
          <NativeSelect
            label="Режим контракта"
            value={mode}
            onChange={(event) => setMode(event.currentTarget.value as "copy" | "linked")}
            data={[
              { value: "copy", label: "Независимая копия" },
              { value: "linked", label: "Связанный общий API" },
            ]}
          />
        ) : null}
        {error ? (
          <Alert color="red" role="alert">
            {error}
          </Alert>
        ) : null}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose} disabled={loading}>
            Отмена
          </Button>
          <Button onClick={() => void load()} disabled={!id} loading={loading}>
            {onImportDesign
              ? mode === "linked"
                ? "Добавить связанный API"
                : "Добавить независимую копию"
              : "Добавить локальную копию"}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}
