import { useState } from "react";
import { Alert, Button, Stack, Text } from "@mantine/core";

export type BackendSVGPin = { projectId: string; viewId: string; viewVersion: number };
type Props = {
  pin: BackendSVGPin;
  name: string;
  scope: string;
  dirty?: boolean;
  /** A's generated-client adapter downloads the attachment; never render its XML. */
  exportFile: (pin: BackendSVGPin) => Promise<void>;
};
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const validID = (id: string) => uuid.test(id) && id !== "00000000-0000-0000-0000-000000000000";

export function BackendViewSVGExport(props: Props) {
  return (
    <SavedExport
      key={`${props.pin.projectId}/${props.pin.viewId}/${props.pin.viewVersion}`}
      {...props}
    />
  );
}
function SavedExport({ pin, name, scope, dirty = false, exportFile }: Props) {
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState(false);
  const valid =
    validID(pin.projectId) &&
    validID(pin.viewId) &&
    Number.isSafeInteger(pin.viewVersion) &&
    pin.viewVersion > 0;
  const filename = `backend-view-${pin.viewId}-v${pin.viewVersion}.svg`;
  return (
    <Stack gap="xs" aria-label="Экспорт сохранённого вида">
      <Text size="sm">
        {name} · {scope} · версия {pin.viewVersion}
      </Text>
      <Text size="xs">{filename}</Text>
      {dirty && (
        <Text size="sm">
          Экспортируется сохранённая версия. Несохранённые изменения в файл не попадут.
        </Text>
      )}
      <Button
        type="button"
        variant="default"
        disabled={!valid || pending}
        loading={pending}
        onClick={async () => {
          setPending(true);
          setError("");
          setDone(false);
          try {
            await exportFile({ ...pin });
            setDone(true);
          } catch (cause) {
            setError(cause instanceof Error ? cause.message : "Не удалось скачать SVG");
          } finally {
            setPending(false);
          }
        }}
      >
        Скачать SVG
      </Button>
      {error && (
        <Alert color="red" title="Ошибка экспорта">
          {error}
        </Alert>
      )}
      <Text component="output" size="sm" aria-live="polite">
        {pending ? "Подготовка SVG…" : done ? "Файл передан для скачивания" : ""}
      </Text>
    </Stack>
  );
}
