import { useState } from "react";
import type { ReactElement } from "react";
import { Button, Checkbox, Group, Stack, Text, TextInput } from "@mantine/core";
import type { CheckpointSummaryView, RollbackRequest } from "@/api/generated/schemas";

// RollbackModalBody is the self-contained controlled child D8 asks for: it
// owns the checkbox, the confirmSlug field and the submit/cancel buttons,
// and reads only its OWN local state to build the request — see the comment
// on handleRollback in CheckpointList for why that is the requirement, not
// a style choice. confirmSlug deliberately starts empty rather than
// pre-filled from workspaceSlug (D8/D11 property 8): the slug exists to
// stop a call aimed at the wrong workspace, and a pre-filled field would
// make every such call succeed. The client-side match against
// workspaceSlug below is a courtesy — instant feedback instead of a round
// trip — not a replacement for the server's own check inside the write
// transaction, which is authoritative.
export function RollbackModalBody({
  cp,
  scenarioActive,
  workspaceSlug,
  onSubmit,
  onClose,
}: {
  cp: CheckpointSummaryView;
  scenarioActive: boolean;
  workspaceSlug: string;
  onSubmit: (body: RollbackRequest) => void;
  onClose: () => void;
}): ReactElement {
  const [restoreData, setRestoreData] = useState(false);
  const [confirmSlug, setConfirmSlug] = useState("");
  const [slugError, setSlugError] = useState<string | null>(null);

  function handleConfirm(): void {
    if (!restoreData) {
      onSubmit({ restoreData: false });
      onClose();
      return;
    }
    const trimmed = confirmSlug.trim();
    if (trimmed === "") {
      setSlugError("Укажите слаг воркспейса");
      return;
    }
    if (trimmed !== workspaceSlug) {
      setSlugError("Слаг не совпадает со слагом этого воркспейса");
      return;
    }
    onSubmit({ restoreData: true, confirmSlug: trimmed });
    onClose();
  }

  return (
    <Stack gap="xs">
      <Text size="sm">
        Откат восстанавливает НАСТРОЙКИ воркспейса целиком, а не только правки операций — включая
        basePath (маршрут воркспейса может переехать на другой префикс, если точка снята при другом
        basePath) и ключ подписи auth.signingKey (восстановленный ключ сделает недействительными все
        токены, которые сейчас держит фронтенд под тестом).
      </Text>
      <Text size="sm" data-testid="rollback-resources-warning">
        Откат всегда возвращает КОНФИГУРАЦИЮ ресурсов, записанную в этой точке: какие семейства
        подтверждены и как они настроены. С флажком «вернуть и данные ресурсов» он восстанавливает и
        сами записи из этой точки — семейство, отклонённое после неё, вернётся подтверждённым и
        заполненным. Без флажка записи он не трогает — ни возвращает, ни удаляет: подтверждённое
        после точки семейство останется подтверждённым, а отклонённое после неё вернётся
        подтверждённым, но пустым.
      </Text>
      {scenarioActive ? (
        <Text size="sm" c="orange" data-testid="rollback-scenario-warning">
          Сейчас активен сценарий — часть восстановленного слоя воркспейса по-прежнему останется
          замаскирована сценарием, пока его не деактивируют.
        </Text>
      ) : null}
      <Text size="sm" c="dimmed" data-testid="rollback-undo-note">
        Перед откатом сохраняется точка текущего состояния — настройки, правки, endpoint&apos;ы и
        записи ресурсов можно вернуть, откатившись на неё с тем же флажком. Ресурс, который этот
        откат сконфигурировал заново, останется подтверждённым: убрать его можно только отклонением.
      </Text>
      <Checkbox
        label="вернуть и данные ресурсов"
        checked={restoreData}
        disabled={!cp.hasData}
        onChange={(event) => {
          setRestoreData(event.currentTarget.checked);
          setSlugError(null);
        }}
        data-testid="rollback-restore-data"
      />
      {!cp.hasData ? (
        <Text size="xs" c="dimmed" data-testid="rollback-restore-data-hint">
          У этой точки нет сохранённых записей ресурсов — восстанавливать нечего.
        </Text>
      ) : null}
      {restoreData ? (
        <TextInput
          label="Слаг воркспейса"
          value={confirmSlug}
          onChange={(event) => {
            setConfirmSlug(event.currentTarget.value);
            setSlugError(null);
          }}
          error={slugError}
          data-testid="rollback-confirm-slug"
        />
      ) : null}
      <Group justify="flex-end">
        {/* The one hand-built dialog body in this tree: it carries the same
            data-testid Mantine's own confirm-modal cancel gets from
            cancelProps, so src/test/dialog.ts's cancelDialog works on it too. */}
        <Button variant="default" onClick={onClose} data-testid="dialog-cancel">
          Отмена
        </Button>
        <Button color="red" onClick={handleConfirm} data-testid="checkpoint-rollback-confirm">
          Откатить
        </Button>
      </Group>
    </Stack>
  );
}
