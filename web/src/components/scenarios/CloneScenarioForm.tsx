import type { ReactElement } from "react";
import { Alert, Button, Group, Stack, Text, TextInput } from "@mantine/core";
import { IconAlertTriangle } from "@tabler/icons-react";
import { useForm } from "react-hook-form";
import { useCreateScenario } from "@/api/generated/scenarios/scenarios.ts";
import type { ScenarioSummaryView } from "@/api/generated/schemas";
import { arktypeResolver } from "@/validation/resolver";
import { EMPTY_FORM, createForm, describeMutationFailure, type CreateForm } from "./shared";

// CloneScenarioForm is its own component, not inline JSX in handleClone,
// because it needs hooks: a form (for the new name) and a mutation instance
// SEPARATE from the top create form's own useCreateScenario (§ this file's
// header) — sharing one instance would tangle the top form's pending/error
// state with whichever row's clone happens to be open. SIG-CLONE: `from`
// always accompanies this request, and that alone is what makes the server
// bypass A10's active-scenario refusal — the request never reads the
// workspace's own layer, so it succeeds whether or not a scenario is
// currently active, unlike the create form's own POST just above.
export function CloneScenarioForm({
  id,
  source,
  onCancel,
  onCloned,
}: {
  id: number;
  source: ScenarioSummaryView;
  onCancel: () => void;
  onCloned: () => void;
}): ReactElement {
  const {
    register,
    handleSubmit,
    formState: { errors },
  } = useForm<CreateForm>({
    resolver: arktypeResolver(createForm),
    defaultValues: EMPTY_FORM,
  });

  const cloneScenario = useCreateScenario({
    mutation: {
      onSuccess: (res) => {
        if (res.status === 201) {
          onCloned();
        }
      },
    },
  });

  function onSubmit(values: CreateForm): void {
    cloneScenario.mutate({ id, data: { name: values.name.trim(), from: source.id } });
  }

  return (
    <Stack gap="sm" component="form" onSubmit={handleSubmit(onSubmit)}>
      <Text size="sm" c="dimmed">
        Снимок «{source.name}» будет скопирован под новым именем. Доступно и пока «{source.name}», и
        пока любой другой сценарий активен — клон копирует сохранённый снимок, а не собственный слой
        воркспейса, поэтому отказ, которым отвечает форма выше, на клонирование не распространяется.
      </Text>
      {cloneScenario.isError ? (
        <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
          {describeMutationFailure(cloneScenario.error)}
        </Alert>
      ) : null}
      <TextInput
        label="Имя нового сценария"
        data-testid="scenario-clone-name"
        error={errors.name?.message}
        {...register("name")}
      />
      <Group justify="flex-end">
        <Button type="button" variant="default" onClick={onCancel} data-testid="dialog-cancel">
          Отмена
        </Button>
        <Button type="submit" loading={cloneScenario.isPending} data-testid="scenario-clone-submit">
          Клонировать
        </Button>
      </Group>
    </Stack>
  );
}
