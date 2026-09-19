import { useState } from "react";
import type { ReactElement } from "react";
import { Alert, Button, Card, Stack, Text, TextInput } from "@mantine/core";
import { IconAlertTriangle, IconDeviceFloppy } from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import { useForm } from "react-hook-form";
import { type } from "arktype";
import {
  getListCheckpointsQueryKey,
  useCreateCheckpoint,
} from "@/api/generated/checkpoints/checkpoints.ts";
import { describeApiFailureDetailed } from "@/api/errors";
import { arktypeResolver } from "@/validation/resolver";
import classes from "../WorkspaceTools.module.css";

const labelField = type("string").narrow((value, ctx) => {
  const trimmed = value.trim();
  if (trimmed === "") {
    return ctx.reject({ problem: "Укажите метку точки" });
  }
  // Counted in Unicode code points via the spread, matching Go's
  // utf8.RuneCountInString (C14's own cap) rather than .length's UTF-16
  // code units — a label built from astral characters would otherwise pass
  // here and still be refused by the server's own rune count.
  if ([...trimmed].length > 200) {
    return ctx.reject({ problem: "Не длиннее 200 символов" });
  }
  return true;
});

const createForm = type({ label: labelField });
type CreateForm = typeof createForm.infer;

const EMPTY_FORM: CreateForm = { label: "" };

export function CreateCheckpointForm({
  id,
  scenarioActive,
}: {
  id: number;
  scenarioActive: boolean;
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

  const createCheckpoint = useCreateCheckpoint({
    mutation: {
      onSuccess: (res) => {
        if (res.status !== 201) {
          return;
        }
        setCreated(res.data.label);
        reset(EMPTY_FORM);
        // C12: a manual checkpoint never bumps revision, so — unlike every
        // destructive action below — the workspace query has nothing in it
        // to go stale. Only the list this checkpoint now appears in does.
        void queryClient.invalidateQueries({ queryKey: getListCheckpointsQueryKey(id) });
      },
    },
  });

  function onSubmit(values: CreateForm): void {
    setCreated(null);
    createCheckpoint.mutate({ id, data: { label: values.label.trim() } });
  }

  return (
    <Card
      component="form"
      withBorder
      p="md"
      data-testid="checkpoint-create-form"
      onSubmit={handleSubmit(onSubmit)}
    >
      <Stack gap="sm">
        {scenarioActive ? (
          // C8: the stored row carries no flag for this — checkpoints has no
          // column for it, and the bundle format may not invent one to hold
          // it — so this banner at the moment of pressing IS the whole of
          // the warning; there is nowhere else it could live. Rendered
          // unconditionally while a scenario is active (not gated behind a
          // click), so it is on screen well before the request it warns
          // about — obs 17's own requirement.
          <Alert
            color="yellow"
            icon={<IconAlertTriangle size={18} />}
            data-testid="checkpoint-create-scenario-warning"
          >
            Активен сценарий. Чекпойнт фиксирует только слой воркспейса — то, что сейчас
            отображается благодаря сценарию, в снимок не попадёт.
          </Alert>
        ) : null}
        {createCheckpoint.isError ? (
          <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
            {describeApiFailureDetailed(createCheckpoint.error)}
          </Alert>
        ) : null}
        {created !== null ? (
          <Text size="sm" data-testid="checkpoint-created">
            Сохранена точка «<strong>{created}</strong>»
          </Text>
        ) : null}
        <div className={classes.inlineForm}>
          <TextInput
            label="Метка точки"
            placeholder="Например, перед изменением ответов"
            data-testid="checkpoint-create-label"
            error={errors.label?.message}
            {...register("label")}
          />
          <Button
            type="submit"
            w="fit-content"
            leftSection={<IconDeviceFloppy size={16} />}
            loading={createCheckpoint.isPending}
            data-testid="checkpoint-create-submit"
          >
            Сохранить точку
          </Button>
        </div>
      </Stack>
    </Card>
  );
}
