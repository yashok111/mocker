import { useState, type ReactElement } from "react";
import { Alert, Anchor, Badge, Button, Group, Modal, Stack, Text } from "@mantine/core";
import { Link } from "@tanstack/react-router";
import { useListApiDesigns } from "@/api/generated/api-designs/api-designs";
import type { DesignScenarioCommand, DesignScenarioContractUpdate } from "@/api/generated/schemas";
import type { CanvasDocument } from "./types";
import { CanvasApiPicker } from "./CanvasApiPicker";
import { CanvasContractConversionModal } from "./CanvasContractConversionModal";

export function ScenarioContractsPanel({
  opened,
  onClose,
  document,
  version,
  updates,
  dirty,
  pendingForms,
  pending,
  error,
  onCommand,
  onCreateFromSchema,
}: {
  opened: boolean;
  onClose: () => void;
  document: CanvasDocument;
  version: number;
  updates: DesignScenarioContractUpdate[];
  dirty: boolean;
  pendingForms: boolean;
  pending: boolean;
  error?: string;
  onCommand: (commands: DesignScenarioCommand[], summary: string) => void;
  onCreateFromSchema: (commands: DesignScenarioCommand[], expectedVersion: number) => Promise<void>;
}): ReactElement {
  const [pickerOpened, setPickerOpened] = useState(false);
  const [conversion, setConversion] = useState<{
    document: CanvasDocument;
    version: number;
  } | null>(null);
  const designsQuery = useListApiDesigns({ query: { enabled: opened } });
  const designs = designsQuery.data?.status === 200 ? designsQuery.data.data.designs : [];
  return (
    <>
      <Modal
        opened={opened && conversion === null}
        onClose={pending ? () => {} : onClose}
        closeButtonProps={{ disabled: pending }}
        title="Контракты API"
        size="lg"
      >
        <Stack gap="md">
          {dirty ? (
            <Alert color="yellow">
              Операции с контрактами доступны после сохранения изменений.
            </Alert>
          ) : null}
          {pendingForms ? (
            <Alert color="yellow">
              Завершите редактирование полей API перед созданием контракта по схеме.
            </Alert>
          ) : null}
          <Button
            variant="light"
            disabled={dirty || pendingForms || pending}
            onClick={() => setConversion({ document: structuredClone(document), version })}
          >
            Создать контракт по всей схеме
          </Button>
          {error ? (
            <Alert color="red" role="alert">
              {error}
            </Alert>
          ) : null}
          {document.contracts.length === 0 ? (
            <Text c="dimmed">В сценарии пока нет контрактов API.</Text>
          ) : null}
          {document.contracts.map((contract) => {
            const linked = contract.mode === "linked";
            const update = updates.find((item) => item.contractId === contract.id);
            const source = contract.source;
            const sourceDesign = source
              ? designs.find((item) => item.id === source.designId)
              : undefined;
            return (
              <Stack
                key={contract.id}
                gap="xs"
                p="sm"
                style={{ border: "1px solid var(--mocker-border)" }}
              >
                <Group justify="space-between">
                  <Text fw={650}>{contract.name}</Text>
                  <Badge color={linked ? "teal" : "gray"}>
                    {linked ? "Общий API" : "Независимая копия"}
                  </Badge>
                </Group>
                {source ? (
                  <Group gap="md">
                    <Anchor
                      renderRoot={(props) => (
                        <Link {...props} to="/designs/$id" params={{ id: source.designId }} />
                      )}
                      c="var(--mocker-accent)"
                      size="sm"
                      fw={500}
                      underline="hover"
                    >
                      Открыть API Designer
                    </Anchor>
                    {sourceDesign?.draftUrl ? (
                      <Anchor
                        href={sourceDesign.draftUrl}
                        target="_blank"
                        rel="noreferrer"
                        c="var(--mocker-accent)"
                        size="sm"
                        fw={500}
                        underline="hover"
                      >
                        Открыть draft mock
                      </Anchor>
                    ) : null}
                    <Text size="xs" c="dimmed">
                      revision {source.revisionId} · v{source.version}
                    </Text>
                  </Group>
                ) : null}
                <Group gap="xs">
                  {update ? (
                    <Button
                      size="xs"
                      variant="light"
                      disabled={dirty}
                      loading={pending}
                      onClick={() =>
                        onCommand(
                          [{ type: "refresh_contract", contractId: contract.id }],
                          `Обновлён API ${contract.name}`,
                        )
                      }
                    >
                      Обновить до версии {update.version}
                    </Button>
                  ) : null}
                  {linked ? (
                    <Button
                      size="xs"
                      variant="default"
                      disabled={dirty}
                      loading={pending}
                      onClick={() =>
                        onCommand(
                          [{ type: "detach_contract", contractId: contract.id }],
                          `Создана независимая копия ${contract.name}`,
                        )
                      }
                    >
                      Отделить копию
                    </Button>
                  ) : (
                    <Button
                      size="xs"
                      disabled={dirty}
                      loading={pending}
                      aria-label={`Создать API-проект ${contract.name}`}
                      onClick={() =>
                        onCommand(
                          [{ type: "materialize_contract", contractId: contract.id }],
                          `Создан API-проект ${contract.name}`,
                        )
                      }
                    >
                      Создать API-проект
                    </Button>
                  )}
                </Group>
              </Stack>
            );
          })}
          <Group justify="flex-end">
            <Button disabled={dirty || pending} onClick={() => setPickerOpened(true)}>
              Добавить API
            </Button>
            <Button variant="default" disabled={pending} onClick={onClose}>
              Закрыть
            </Button>
          </Group>
        </Stack>
      </Modal>
      {conversion ? (
        <CanvasContractConversionModal
          document={conversion.document}
          expectedVersion={conversion.version}
          onClose={() => setConversion(null)}
          onCreate={onCreateFromSchema}
        />
      ) : null}
      {pickerOpened ? (
        <CanvasApiPicker
          onClose={() => setPickerOpened(false)}
          onImportDesign={({ designId, mode }) =>
            onCommand(
              [{ type: "import_contract", designId, mode }],
              mode === "linked" ? "Добавлен связанный API" : "Добавлена копия API",
            )
          }
        />
      ) : null}
    </>
  );
}
