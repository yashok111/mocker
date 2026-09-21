import { useState } from "react";
import { Alert, Button, Code, Loader, Modal, Stack, Text } from "@mantine/core";
import { useValidateDesignScenario } from "@/api/generated/design-scenarios/design-scenarios";
import { describeApiFailureDetailed } from "@/api/errors";
import type { CanvasDocument } from "./types";

export function ScenarioValidationAction({
  id,
  document,
  disabled = false,
}: {
  id: number;
  document: CanvasDocument;
  disabled?: boolean;
}) {
  const [opened, setOpened] = useState(false);
  const validation = useValidateDesignScenario();
  const stale =
    validation.variables !== undefined &&
    JSON.stringify(validation.variables.data.document) !== JSON.stringify(document);
  const diagnostics = validation.data?.status === 200 ? validation.data.data.diagnostics : [];

  function validate() {
    setOpened(true);
    validation.mutate({ id, data: { document } });
  }

  return (
    <>
      <Button size="sm" variant="default" disabled={disabled} onClick={validate}>
        Проверить сценарий
      </Button>
      <Modal
        opened={opened}
        onClose={() => setOpened(false)}
        title="Проверка сценария"
        onKeyDown={(event) => {
          if ((event.metaKey || event.ctrlKey) && ["z", "y"].includes(event.key.toLowerCase()))
            event.stopPropagation();
        }}
      >
        <Stack gap="sm">
          {stale ? (
            <Alert color="yellow">Сценарий изменился. Запустите проверку ещё раз.</Alert>
          ) : validation.isPending ? (
            <Loader aria-label="Проверяем сценарий" />
          ) : validation.isError ? (
            <Alert color="red">{describeApiFailureDetailed(validation.error)}</Alert>
          ) : validation.isSuccess ? (
            diagnostics.length === 0 ? (
              <Alert color="green">Ошибок и предупреждений нет</Alert>
            ) : (
              diagnostics.map((diagnostic, index) => (
                <Alert
                  key={`${diagnostic.pointer}:${index}`}
                  color={diagnostic.severity === "error" ? "red" : "yellow"}
                  title={diagnostic.severity === "error" ? "Ошибка" : "Предупреждение"}
                >
                  <Text size="sm">{diagnostic.message}</Text>
                  {diagnostic.pointer && <Code>{diagnostic.pointer}</Code>}
                </Alert>
              ))
            )
          ) : null}
          <Text size="xs" c="dimmed">
            Проверка структуры и связей сценария. HTTP-запросы не выполняются.
          </Text>
          <Button
            variant="light"
            disabled={disabled}
            loading={validation.isPending}
            onClick={validate}
          >
            Проверить снова
          </Button>
        </Stack>
      </Modal>
    </>
  );
}
