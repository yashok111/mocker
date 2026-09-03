import { useState } from "react";
import type { ReactElement } from "react";
import {
  Alert,
  Badge,
  Button,
  Card,
  Group,
  Loader,
  Stack,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { modals } from "@mantine/modals";
import {
  IconAlertTriangle,
  IconCopy,
  IconDeviceFloppy,
  IconEdit,
  IconPlayerPlay,
  IconPlayerStop,
  IconTrash,
} from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import { useForm } from "react-hook-form";
import { type } from "arktype";
import dayjs from "dayjs";
import {
  getListScenariosQueryKey,
  useActivateScenario,
  useCreateScenario,
  useDeactivateScenario,
  useDeleteScenario,
  useListScenarios,
  useRenameScenario,
} from "@/api/generated/scenarios/scenarios.ts";
import { getGetWorkspaceQueryKey } from "@/api/generated/workspaces/workspaces.ts";
import type {
  EditConflictTombstone,
  ScenarioConflictDetails,
  ScenarioSummaryView,
} from "@/api/generated/schemas";
import { ApiFailure } from "@/api/client";
import { describeApiFailure, describeApiFailureDetailed, isGoneTombstone } from "@/api/errors";
import { arktypeResolver } from "@/validation/resolver";

// ScenariosPage is DESIGN §14 screen 9, P2b: a named snapshot of the
// workspace's SETTINGS and op_overrides rows, composed at request time OVER
// the workspace's own layer (never restored over it — §0/§A1 of the P2b
// context) and switched on with one click. There is no scenario EDITOR by
// design: the only way to produce a snapshot's CONTENTS is to put the
// workspace into the state it should capture and save it from there — P2d's
// clone and rename do not relax that: clone copies an existing snapshot's
// bytes verbatim under a new name, and rename touches only the `name`
// column, never `snapshot`. That is why this screen offers seven actions —
// list, save-from-current-state, clone, rename, activate, deactivate,
// delete — and still nothing that edits a scenario's own contents.
//
// The save button says «сохранить настройки и правки операций», never
// «сохранить текущее состояние»: DESIGN §14:905 wrote the latter before the
// bundle format had a shape, and a scenario does not carry the custom
// endpoints the «Кастомные» tab shows (§0 — endpoints have no place in a
// snapshot keyed by op_overrides rows) — a button promising «состояние»
// would promise those too.
//
// The outermost element carries data-testid="scenarios-page" OUTSIDE every
// state switch below (§I): a marker only on the success branch would make
// this screen's reachability depend on whether routes.test.tsx happened to
// mock every query it fires.
export function ScenariosPage({ id }: { id: number }): ReactElement {
  const scenarios = useListScenarios(id);
  const list = scenarios.data?.status === 200 ? scenarios.data.data.scenarios : [];
  const activeScenario = list.find((sc) => sc.isActive);

  return (
    <div data-testid="scenarios-page">
      <Stack gap="md">
        <Title order={1}>Сценарии</Title>
        <Text size="sm" c="dimmed">
          Сценарий — именованный снимок настроек и правок операций воркспейса, который подставляется
          поверх собственного слоя воркспейса при каждом запросе, а не переписывает его: деактивация
          возвращает всё как было. Кастомные endpoint&apos;ы сценарий не захватывает — редактируются
          они, как обычно, на вкладке «Кастомные».
        </Text>
        <CreateScenarioForm id={id} activeScenario={activeScenario} />
        {scenarios.isPending ? (
          // role on the Text, not the Group: the live region should be the
          // sentence a screen reader announces, not the flex box around it.
          <Group gap="xs">
            <Loader size="sm" />
            <Text size="sm" component="output">
              Загрузка…
            </Text>
          </Group>
        ) : scenarios.isError ? (
          <Stack gap="sm" data-testid="scenarios-error">
            <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
              {describeApiFailure(scenarios.error)}
            </Alert>
            <Button
              variant="default"
              w="fit-content"
              onClick={() => void scenarios.refetch()}
              data-testid="scenarios-retry"
            >
              Повторить
            </Button>
          </Stack>
        ) : scenarios.data.status !== 200 ? (
          <Alert
            color="red"
            icon={<IconAlertTriangle size={18} />}
            role="alert"
            data-testid="scenarios-error"
          >
            {describeApiFailure(null)}
          </Alert>
        ) : list.length === 0 ? (
          <Text data-testid="scenarios-empty">
            Сценариев пока нет. Настройте воркспейс и правки операций как нужно и сохраните первый
            сценарий формой выше.
          </Text>
        ) : (
          <ScenarioList id={id} scenarios={list} />
        )}
      </Stack>
    </div>
  );
}

const nameField = type("string").narrow((value, ctx) => {
  const trimmed = value.trim();
  if (trimmed === "") {
    return ctx.reject({ problem: "Укажите имя сценария" });
  }
  return true;
});

const createForm = type({ name: nameField });
type CreateForm = typeof createForm.infer;

const EMPTY_FORM: CreateForm = { name: "" };

// createdAt arrives as Unix seconds (internal/admin/scenario_handlers.go's
// newScenarioSummaryView writes sc.CreatedAt.Unix()), the same convention
// every other screen in this app already documents for its own timestamps —
// dayjs needs telling which, or it reads 1970 for every row.
function formatTimestamp(unixSeconds: number): string {
  return dayjs.unix(unixSeconds).format("DD.MM.YYYY HH:mm");
}

function CreateScenarioForm({
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
        // which this write just changed.
        void queryClient.invalidateQueries({ queryKey: getGetWorkspaceQueryKey(id) });
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
        <TextInput
          label="Имя сценария"
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
          {activeScenario ? "Деактивировать и сохранить" : "Сохранить настройки и правки операций"}
        </Button>
      </Stack>
    </Card>
  );
}

function ScenarioList({
  id,
  scenarios,
}: {
  id: number;
  scenarios: ScenarioSummaryView[];
}): ReactElement {
  const queryClient = useQueryClient();
  // Named per-row rather than read off one mutation's own .error: three
  // different mutations (activate/deactivate/delete) share this one alert,
  // and none of them remembers on its own WHICH scenario it was acting on.
  const [actionError, setActionError] = useState<{ label: string; message: string } | null>(null);

  function invalidateAfterWrite(): void {
    void queryClient.invalidateQueries({ queryKey: getListScenariosQueryKey(id) });
    // Activate, deactivate AND delete (when the deleted scenario was
    // active, A9) all move workspace.scenarioId and bump revision — the tab
    // bar's own "ревизия N" text and screen 5's A18 banner both read that
    // straight off the workspace query, so every write here has to
    // invalidate it too, not just the scenario list.
    void queryClient.invalidateQueries({ queryKey: getGetWorkspaceQueryKey(id) });
  }

  const activateScenario = useActivateScenario({
    mutation: {
      onSuccess: () => {
        setActionError(null);
        invalidateAfterWrite();
      },
    },
  });
  const deactivateScenario = useDeactivateScenario({
    mutation: {
      onSuccess: () => {
        setActionError(null);
        invalidateAfterWrite();
      },
    },
  });
  const deleteScenario = useDeleteScenario({
    mutation: {
      onSuccess: () => {
        setActionError(null);
        invalidateAfterWrite();
      },
    },
  });

  function handleActivate(sc: ScenarioSummaryView): void {
    activateScenario.mutate(
      { id, sid: sc.id },
      { onError: (err) => setActionError({ label: sc.name, message: describeApiFailure(err) }) },
    );
  }

  function handleDeactivate(sc: ScenarioSummaryView): void {
    deactivateScenario.mutate(
      { id },
      { onError: (err) => setActionError({ label: sc.name, message: describeApiFailure(err) }) },
    );
  }

  function handleDelete(sc: ScenarioSummaryView): void {
    modals.openConfirmModal({
      title: "Удалить сценарий",
      children: (
        <Text size="sm">
          Удалить «{sc.name}»? Это действие необратимо.
          {sc.isActive
            ? " Сценарий сейчас активен — удаление деактивирует его, и воркспейс вернётся к своему собственному слою."
            : ""}
        </Text>
      ),
      labels: { confirm: "Удалить", cancel: "Отмена" },
      confirmProps: { color: "red", "data-testid": "scenario-delete-confirm" },
      onConfirm: () => {
        deleteScenario.mutate(
          { id, sid: sc.id },
          {
            onError: (err) => setActionError({ label: sc.name, message: describeApiFailure(err) }),
          },
        );
      },
    });
  }

  // Clone and rename each get their own modal rather than
  // modals.openConfirmModal's static children: both need a live TextInput
  // plus a mutation with its own pending/error state, and openConfirmModal
  // has no seam for either — closeModal(modalId) is what lets the form
  // component itself decide when the round trip is done, instead of the
  // modal auto-closing the instant "confirm" is clicked.
  function handleClone(sc: ScenarioSummaryView): void {
    const modalId = `scenario-clone-${sc.id}`;
    modals.open({
      modalId,
      title: `Клонировать «${sc.name}»`,
      children: (
        <CloneScenarioForm
          id={id}
          source={sc}
          onCancel={() => modals.close(modalId)}
          onCloned={() => {
            modals.close(modalId);
            invalidateAfterWrite();
          }}
        />
      ),
    });
  }

  function handleRename(sc: ScenarioSummaryView): void {
    const modalId = `scenario-rename-${sc.id}`;
    modals.open({
      modalId,
      title: `Переименовать «${sc.name}»`,
      children: (
        <RenameScenarioForm
          id={id}
          source={sc}
          onCancel={() => modals.close(modalId)}
          onRenamed={() => {
            modals.close(modalId);
            invalidateAfterWrite();
          }}
        />
      ),
    });
  }

  return (
    <Stack gap="sm">
      {actionError !== null ? (
        <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
          «{actionError.label}»: {actionError.message}
        </Alert>
      ) : null}
      <Card withBorder p={0} data-testid="scenario-list">
        <Stack gap={0}>
          {scenarios.map((sc) => (
            <Group
              key={sc.id}
              justify="space-between"
              wrap="nowrap"
              px="md"
              py="sm"
              data-testid="scenario-row"
              style={{ borderTop: "1px solid var(--mantine-color-gray-3)" }}
            >
              <div>
                <Group gap="xs">
                  <Text size="sm" fw={500}>
                    {sc.name}
                  </Text>
                  {sc.isActive ? (
                    <Badge color="green" size="sm" data-testid="scenario-active-badge">
                      активен
                    </Badge>
                  ) : null}
                </Group>
                <Text size="xs" c="dimmed">
                  создан {formatTimestamp(sc.createdAt)}
                </Text>
              </div>
              <Group gap="xs" wrap="nowrap">
                <Button
                  variant="default"
                  size="xs"
                  leftSection={<IconCopy size={16} />}
                  onClick={() => handleClone(sc)}
                  data-testid="scenario-clone"
                >
                  Клонировать
                </Button>
                <Button
                  variant="default"
                  size="xs"
                  leftSection={<IconEdit size={16} />}
                  onClick={() => handleRename(sc)}
                  data-testid="scenario-rename"
                >
                  Переименовать
                </Button>
                {sc.isActive ? (
                  <Button
                    variant="default"
                    size="xs"
                    leftSection={<IconPlayerStop size={16} />}
                    onClick={() => handleDeactivate(sc)}
                    loading={deactivateScenario.isPending}
                    data-testid="scenario-deactivate"
                  >
                    Деактивировать
                  </Button>
                ) : (
                  <Button
                    variant="default"
                    size="xs"
                    leftSection={<IconPlayerPlay size={16} />}
                    onClick={() => handleActivate(sc)}
                    loading={activateScenario.isPending}
                    data-testid="scenario-activate"
                  >
                    Активировать
                  </Button>
                )}
                <Button
                  variant="default"
                  size="xs"
                  color="red"
                  leftSection={<IconTrash size={16} />}
                  onClick={() => handleDelete(sc)}
                  loading={deleteScenario.isPending}
                  data-testid="scenario-delete"
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

// describeMutationFailure picks between the two Russian renderers the same
// way the create form above already does: a 409 here names either the
// TAKEN name (both routes share the create path's ErrDuplicateName → 409)
// or, for clone, a source scenario that vanished from under the request —
// either way the server's own sentence is the actionable content, not
// incidental detail. Anything else (400 on a blank/invalid name that somehow
// reached the wire, 404 on a source or a scenario deleted from another tab)
// gets the generic summary instead, same as every other screen's fallback.
function describeMutationFailure(err: unknown): string {
  return err instanceof ApiFailure && err.status === 409
    ? describeApiFailureDetailed(err)
    : describeApiFailure(err);
}

// CloneScenarioForm is its own component, not inline JSX in handleClone,
// because it needs hooks: a form (for the new name) and a mutation instance
// SEPARATE from the top create form's own useCreateScenario (§ this file's
// header) — sharing one instance would tangle the top form's pending/error
// state with whichever row's clone happens to be open. SIG-CLONE: `from`
// always accompanies this request, and that alone is what makes the server
// bypass A10's active-scenario refusal — the request never reads the
// workspace's own layer, so it succeeds whether or not a scenario is
// currently active, unlike the create form's own POST just above.
function CloneScenarioForm({
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
        <Button type="button" variant="default" onClick={onCancel}>
          Отмена
        </Button>
        <Button type="submit" loading={cloneScenario.isPending} data-testid="scenario-clone-submit">
          Клонировать
        </Button>
      </Group>
    </Stack>
  );
}

// RenameScenarioForm: PUT changes only the `name` column (SIG-RENAME) — no
// revision bump, because no runtime cache key contains a scenario's name
// (§4 of the P2d context). The one place a name IS a live key is the mock
// plane's own POST {prefix}/state {"scenario":"<name>"}, which resolves the
// name per request — so renaming a scenario that a running test suite
// switches to by that route is a real breaking change for that suite, and
// this screen is the only place an operator will learn it before it bites.
function RenameScenarioForm({
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
        const conflict =
          renameScenario.isError &&
          renameScenario.error instanceof ApiFailure &&
          renameScenario.error.code === "edit_conflict"
            ? renameScenario.error
            : null;
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
        <Button type="button" variant="default" onClick={onCancel}>
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
