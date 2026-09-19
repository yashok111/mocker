import { useState } from "react";
import type { ReactElement } from "react";
import { Alert, Button, Card, Stack, Text, TextInput } from "@mantine/core";
import { IconAlertTriangle, IconDeviceFloppy } from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import { useForm } from "react-hook-form";
import { invalidateWorkspace } from "@/api/cachePolicy";
import {
  getListScenariosQueryKey,
  useCreateScenario,
  useDeactivateScenario,
} from "@/api/generated/scenarios/scenarios.ts";
import type { ScenarioSummaryView } from "@/api/generated/schemas";
import { describeApiFailureDetailed } from "@/api/errors";
import { arktypeResolver } from "@/validation/resolver";
import { EMPTY_FORM, createForm, type CreateForm } from "./shared";
import classes from "../WorkspaceTools.module.css";

export function CreateScenarioForm({
  id,
  activeScenario,
}: {
  id: number;
  activeScenario: ScenarioSummaryView | undefined;
}): ReactElement {
  const queryClient = useQueryClient();
  const [created, setCreated] = useState<string | null>(null);
  const {
    register,
    handleSubmit,
    reset,
    formState: { errors },
  } = useForm<CreateForm>({
    resolver: arktypeResolver(createForm),
    defaultValues: EMPTY_FORM,
  });

  const createScenario = useCreateScenario({
    mutation: {
      onSuccess: (res) => {
        if (res.status !== 201) {
          return;
        }
        setCreated(res.data.name);
        reset(EMPTY_FORM);
        void queryClient.invalidateQueries({ queryKey: getListScenariosQueryKey(id) });
      },
    },
  });

  const deactivateScenario = useDeactivateScenario({
    mutation: {
      onSuccess: () => {
        // A10's own reasoning: deactivating first is what makes the
        // snapshot that follows capture the WORKSPACE's layer rather than
        // the composed view the active scenario was serving a moment ago —
        // baking that composed view into a new snapshot is exactly the
        // "reading that lies" the gate rejected in favour of this route.
        // The workspace query carries scenario_id and revision, both of
        // which this write just changed. The scenario LIST is untouched by a
        // deactivate — no row appears, disappears or is renamed — so this is
        // invalidateWorkspace and not invalidateScenarioChange.
        invalidateWorkspace(queryClient, id);
      },
    },
  });

  function onSubmit(values: CreateForm): void {
    setCreated(null);
    const name = values.name.trim();
    if (activeScenario) {
      // A10, narrowed by P2d's `from` field (SIG-CLONE): POST .../scenarios
      // answers 409 outright while a scenario is active ONLY for a request
      // with no `from` — a snapshot of the workspace's OWN layer, which is
      // exactly what this form sends. `from` bypasses that refusal by never
      // reading the workspace layer at all, but that is the clone action
      // below (CloneScenarioForm), a different operation, not a second way
      // to drive this button. For THIS request shape there is still no way
      // to save-from-current-state AND deactivate at once, so a checkbox
      // promising both here would still have to perform this exact
      // two-step sequence underneath. Naming the sequence as one button
      // ("деактивировать и сохранить") is the gate's own resolution, not a
      // UI convenience on top of it.
      deactivateScenario.mutate(
        { id },
        { onSuccess: () => createScenario.mutate({ id, data: { name } }) },
      );
    } else {
      createScenario.mutate({ id, data: { name } });
    }
  }

  const pending = createScenario.isPending || deactivateScenario.isPending;
  // Deactivate's own failure is shown here too, not just create's: it is
  // the first half of the chained action this form triggers when a
  // scenario is active, and a person watching this form has no other place
  // to learn that half failed.
  const failure = createScenario.isError
    ? createScenario.error
    : deactivateScenario.isError
      ? deactivateScenario.error
      : null;

  return (
    <Card
      component="form"
      withBorder
      p="md"
      data-testid="scenario-create-form"
      onSubmit={handleSubmit(onSubmit)}
    >
      <Stack gap="sm">
        {activeScenario ? (
          <Text size="sm" c="dimmed" data-testid="scenario-create-active-note">
            Сейчас активен сценарий «{activeScenario.name}». Сохранение сначала деактивирует его —
            новый сценарий снимается с состояния воркспейса, а не с того, что сценарий сейчас
            подменяет.
          </Text>
        ) : null}
        {failure ? (
          <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
            {/* describeApiFailureDetailed, not describeApiFailure: a 409 here
                names either the scenario that is already active or the name
                that already exists (A10/UNIQUE), and that sentence is the
                actionable content — the same reasoning CustomEndpointsPage's
                own create form already applies to its 409. */}
            {describeApiFailureDetailed(failure)}
          </Alert>
        ) : null}
        {created !== null ? (
          <Text size="sm" data-testid="scenario-created">
            Создан сценарий «<strong>{created}</strong>»
          </Text>
        ) : null}
        <div className={classes.inlineForm}>
          <TextInput
            label="Имя сценария"
            placeholder="Например, ошибка оплаты"
            data-testid="scenario-create-name"
            error={errors.name?.message}
            {...register("name")}
          />
          <Button
            type="submit"
            w="fit-content"
            leftSection={<IconDeviceFloppy size={16} />}
            loading={pending}
            data-testid="scenario-create-submit"
          >
            {activeScenario
              ? "Деактивировать и сохранить"
              : "Сохранить настройки и правки операций"}
          </Button>
        </div>
      </Stack>
    </Card>
  );
}
