import { useState } from "react";
import type { ReactElement } from "react";
import { Alert, Button, Group, Stack, Text, TextInput } from "@mantine/core";
import { IconAlertTriangle } from "@tabler/icons-react";
import { useForm } from "react-hook-form";
import { useRenameScenario } from "@/api/generated/scenarios/scenarios.ts";
import type {
  EditConflictTombstone,
  ScenarioConflictDetails,
  ScenarioSummaryView,
} from "@/api/generated/schemas";
import { conflictOf, describeApiFailureDetailed, isGoneTombstone } from "@/api/errors";
import { arktypeResolver } from "@/validation/resolver";
import { createForm, describeMutationFailure, type CreateForm } from "./shared";

// RenameScenarioForm: PUT changes only the `name` column (SIG-RENAME) — no
// revision bump, because no runtime cache key contains a scenario's name
// (§4 of the P2d context). The one place a name IS a live key is the mock
// plane's own POST {prefix}/state {"scenario":"<name>"}, which resolves the
// name per request — so renaming a scenario that a running test suite
// switches to by that route is a real breaking change for that suite, and
// this screen is the only place an operator will learn it before it bites.
export function RenameScenarioForm({
  id,
  source,
  onCancel,
  onRenamed,
}: {
  id: number;
  source: ScenarioSummaryView;
  onCancel: () => void;
  onRenamed: () => void;
}): ReactElement {
  const {
    register,
    handleSubmit,
    reset,
    formState: { errors },
  } = useForm<CreateForm>({
    resolver: arktypeResolver(createForm),
    defaultValues: { name: source.name },
  });

  // A3: `source` is this row's own preceding read (ScenarioList's own
  // useListScenarios), and editVersion travels with it, sent back as-is on
  // submit — never re-fetched. conflictEditVersion overrides it only in the
  // window after a 409, before the list's own invalidateAfterWrite refetch
  // (fired by the parent on success, not on a conflict) lands.
  const [conflictEditVersion, setConflictEditVersion] = useState<number | null>(null);

  const renameScenario = useRenameScenario({
    mutation: {
      onSuccess: (res) => {
        if (res.status === 200) {
          onRenamed();
        }
      },
    },
  });

  function onSubmit(values: CreateForm): void {
    renameScenario.mutate({
      id,
      sid: source.id,
      data: { name: values.name.trim(), editVersion: conflictEditVersion ?? source.editVersion },
    });
  }

  // handleConflictReload is D10's per-screen affordance. A scenario
  // addressed by {sid} always already exists (RenameScenarioRequest's own
  // description — 0 is refused here, unlike the operation PUT), so a
  // gone-tombstone means the row was deleted mid-edit; there is no name left
  // to rename, so this only closes the modal rather than pretending a reload
  // fixed anything (the list's own next render shows it gone).
  function handleConflictReload(details: ScenarioConflictDetails | EditConflictTombstone): void {
    if (isGoneTombstone(details)) {
      onCancel();
      return;
    }
    reset({ name: details.name });
    setConflictEditVersion(details.editVersion);
  }

  return (
    <Stack gap="sm" component="form" onSubmit={handleSubmit(onSubmit)}>
      <Text size="sm" c="dimmed" data-testid="scenario-rename-warning">
        {`Тестовый набор, который переключается на этот сценарий через {"scenario":"…"} на мок-плоскости, после переименования придётся поправить: старое имя эту запись больше не найдёт.`}
      </Text>
      {(() => {
        const conflict = conflictOf(renameScenario);
        if (conflict !== null) {
          return (
            <Alert
              color="orange"
              icon={<IconAlertTriangle size={18} />}
              role="alert"
              data-testid="scenario-rename-conflict"
            >
              <Text size="sm">{describeApiFailureDetailed(conflict)}</Text>
              <Button
                variant="light"
                size="xs"
                mt="xs"
                onClick={() =>
                  handleConflictReload(
                    conflict.details as ScenarioConflictDetails | EditConflictTombstone,
                  )
                }
                data-testid="scenario-rename-conflict-reload"
              >
                Загрузить актуальную версию
              </Button>
            </Alert>
          );
        }
        return renameScenario.isError ? (
          <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
            {describeMutationFailure(renameScenario.error)}
          </Alert>
        ) : null;
      })()}
      <TextInput
        label="Новое имя"
        data-testid="scenario-rename-name"
        error={errors.name?.message}
        {...register("name")}
      />
      <Group justify="flex-end">
        <Button type="button" variant="default" onClick={onCancel} data-testid="dialog-cancel">
          Отмена
        </Button>
        <Button
          type="submit"
          loading={renameScenario.isPending}
          data-testid="scenario-rename-submit"
        >
          Переименовать
        </Button>
      </Group>
    </Stack>
  );
}
