import { useState } from "react";
import type { ReactElement } from "react";
import { Alert, Badge, Button, Card, Group, Stack, Text } from "@mantine/core";
import { modals } from "@mantine/modals";
import { IconAlertTriangle, IconRestore, IconTrash } from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import { invalidateCheckpointChange } from "@/api/cachePolicy";
import {
  getListCheckpointsQueryKey,
  useDeleteCheckpoint,
  useRollbackWorkspace,
} from "@/api/generated/checkpoints/checkpoints.ts";
import type { CheckpointSummaryView } from "@/api/generated/schemas";
import { describeApiFailure, describeApiFailureDetailed } from "@/api/errors";
import { formatTimestamp } from "@/format";
import { RollbackModalBody } from "./RollbackModalBody";

export function CheckpointList({
  id,
  checkpoints,
  scenarioActive,
  workspaceSlug,
}: {
  id: number;
  checkpoints: CheckpointSummaryView[];
  scenarioActive: boolean;
  workspaceSlug: string;
}): ReactElement {
  const queryClient = useQueryClient();
  // Named per-row rather than read off the mutation's own .error: every row
  // shares this one rollback mutation, and it does not remember on its own
  // WHICH row it was acting on — the same shape ScenariosPage's actionError
  // uses for its own three shared per-row mutations.
  const [actionError, setActionError] = useState<{ label: string; message: string } | null>(null);

  // A21 (G7): RollbackResponseView.dataRestored — P3d's explicit "the data
  // half restored nothing" signal — was never read; the operator ticked
  // «вернуть и данные», the call 200'd and the screen said nothing.
  const [rollbackNote, setRollbackNote] = useState<string | null>(null);
  const rollback = useRollbackWorkspace({
    mutation: {
      onMutate: () => {
        // A previous rollback's green line must not stand beside this one's
        // error (the second reader of A21).
        setRollbackNote(null);
      },
      onSuccess: (res) => {
        setActionError(null);
        if (res.status === 200) {
          setRollbackNote(
            `Откат выполнен, ревизия ${res.data.revision}: ${
              res.data.dataRestored
                ? "данные ресурсов восстановлены из точки"
                : "данные ресурсов не трогались (галочка не стояла, точка их не содержала, или семейство из точки больше не подтверждено)"
            }${res.data.scenarioActive ? "; активный сценарий по-прежнему маскирует часть слоя" : ""}`,
          );
        }
        invalidateCheckpointChange(queryClient, id);
      },
    },
  });

  const deleteCheckpoint = useDeleteCheckpoint({
    mutation: {
      onSuccess: () => {
        setActionError(null);
        // SIG-DELCP: delete bumps no revision — only the list this row sat
        // in has anything stale to invalidate, unlike rollback above.
        void queryClient.invalidateQueries({ queryKey: getListCheckpointsQueryKey(id) });
      },
    },
  });

  // D8: modals.openConfirmModal dispatches OPEN and Mantine's ModalsProvider
  // stores the `children`/`onConfirm` PROPS in its own reducer at that
  // moment — a later re-render of CheckpointList does not replace what the
  // provider is holding. A component-level useState here would look like it
  // works and would not: the stored onConfirm closure would still read the
  // click-time (unchecked, empty-slug) value of that state. So the modal
  // body below is a SELF-CONTAINED controlled child (RollbackModalBody) that
  // owns the checkbox, the slug field AND the submit button itself — it
  // computes the whole request body from its own local state at the moment
  // of its own click and hands it to onSubmit synchronously, so nothing
  // about how many times CheckpointList itself has re-rendered in between
  // can make it stale.
  function handleRollback(cp: CheckpointSummaryView): void {
    const modalId = `rollback-confirm-${cp.id}`;
    modals.open({
      modalId,
      title: `Откатить к точке «${cp.label}»`,
      children: (
        <RollbackModalBody
          cp={cp}
          scenarioActive={scenarioActive}
          workspaceSlug={workspaceSlug}
          onClose={() => modals.close(modalId)}
          onSubmit={(body) => {
            rollback.mutate(
              { id, cid: cp.id, data: body },
              {
                onError: (err) =>
                  setActionError({ label: cp.label, message: describeApiFailureDetailed(err) }),
              },
            );
          }}
        />
      ),
    });
  }

  function handleDelete(cp: CheckpointSummaryView): void {
    modals.openConfirmModal({
      title: `Удалить точку «${cp.label}»`,
      children: (
        <Text size="sm">
          Удалить эту точку истории безвозвратно? У удаления нет отмены — в отличие от отката и
          сброса, оно не оставляет за собой свою собственную точку, на которую можно было бы
          вернуться.
        </Text>
      ),
      labels: { confirm: "Удалить", cancel: "Отмена" },
      confirmProps: { color: "red", "data-testid": "checkpoint-delete-confirm" },
      cancelProps: { "data-testid": "dialog-cancel" },
      onConfirm: () => {
        deleteCheckpoint.mutate(
          { id, cid: cp.id },
          {
            onError: (err) => setActionError({ label: cp.label, message: describeApiFailure(err) }),
          },
        );
      },
    });
  }

  return (
    <Stack gap="sm">
      {actionError !== null ? (
        <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
          «{actionError.label}»: {actionError.message}
        </Alert>
      ) : null}
      {rollbackNote !== null ? (
        <Alert color="teal" data-testid="rollback-result">
          {rollbackNote}
        </Alert>
      ) : null}
      <Card withBorder p={0} data-testid="checkpoint-list">
        <Stack gap={0}>
          {checkpoints.map((cp) => (
            <Group
              key={cp.id}
              justify="space-between"
              wrap="nowrap"
              px="md"
              py="sm"
              data-testid="checkpoint-row"
              style={{ borderTop: "1px solid var(--mantine-color-gray-3)" }}
            >
              <div>
                <Group gap="xs">
                  <Badge
                    color={
                      cp.kind === "manual"
                        ? "blue"
                        : cp.kind === "pre-destructive"
                          ? "orange"
                          : "gray"
                    }
                    size="sm"
                    data-testid="checkpoint-kind"
                  >
                    {cp.kind === "manual"
                      ? "ручной"
                      : cp.kind === "pre-destructive"
                        ? "перед действием"
                        : // "auto" (SIG-AUTO's debounce trigger, P2d) is the third
                          // legal value the column has always accepted — the raw
                          // fallback below existed for it before anything ever
                          // wrote it. A row of this kind is live from this slice
                          // on, so it needs a word, not the bare enum string.
                          cp.kind === "auto"
                          ? "авто"
                          : cp.kind}
                  </Badge>
                  <Text size="sm" fw={500}>
                    {cp.label}
                  </Text>
                </Group>
                <Text size="xs" c="dimmed">
                  #{cp.id} · {formatTimestamp(cp.createdAt)}
                  {cp.createdBy !== null ? ` · пользователь #${cp.createdBy}` : ""}
                  {cp.hasData ? " · с данными ресурсов" : ""}
                </Text>
              </div>
              <Group gap="xs" wrap="nowrap">
                <Button
                  variant="default"
                  size="xs"
                  leftSection={<IconRestore size={16} />}
                  onClick={() => handleRollback(cp)}
                  loading={rollback.isPending}
                  data-testid="checkpoint-rollback"
                >
                  Откатить
                </Button>
                <Button
                  variant="default"
                  size="xs"
                  color="red"
                  leftSection={<IconTrash size={16} />}
                  onClick={() => handleDelete(cp)}
                  loading={deleteCheckpoint.isPending}
                  data-testid="checkpoint-delete"
                >
                  Удалить
                </Button>
              </Group>
            </Group>
          ))}
        </Stack>
      </Card>
    </Stack>
  );
}
