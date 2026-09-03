import { useState } from "react";
import type { ReactElement } from "react";
import {
  Alert,
  Badge,
  Button,
  Card,
  Checkbox,
  Group,
  Loader,
  SegmentedControl,
  Stack,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { modals } from "@mantine/modals";
import { IconAlertTriangle, IconDeviceFloppy, IconRestore, IconTrash } from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import { useForm } from "react-hook-form";
import { type } from "arktype";
import dayjs from "dayjs";
import {
  getListCheckpointsQueryKey,
  useCreateCheckpoint,
  useDeleteCheckpoint,
  useListCheckpoints,
  useResetOverrides,
  useRollbackWorkspace,
} from "@/api/generated/checkpoints/checkpoints.ts";
import { useResetData } from "@/api/generated/resources/resources.ts";
import { getGetWorkspaceQueryKey, useGetWorkspace } from "@/api/generated/workspaces/workspaces.ts";
import type {
  CheckpointSummaryView,
  ResetDataResult,
  RollbackRequest,
} from "@/api/generated/schemas";
import { ResetMode } from "@/api/generated/schemas";
import { describeApiFailure, describeApiFailureDetailed } from "@/api/errors";
import { arktypeResolver } from "@/validation/resolver";

// HistoryPage is DESIGN §14 screen 10, P2c: the workspace's undo log. A
// checkpoint is a point-in-time snapshot of the WORKSPACE layer only —
// settings, op_overrides, custom_endpoints — never the scenario layer
// composed on top of it at request time (bundle.New always hard-codes an
// empty endpoints slice on a SCENARIO snapshot; a checkpoint is the one
// producer that fills it in afterward, C2/C3 of the P2c context). That is
// why every warning below about a scenario "masking" restored state says
// masking, not loss: nothing this screen does can touch what a scenario
// itself serves (mockplane/scenario.go:113-116 — a key the scenario names
// keeps the scenario's answer, before and after any of these three calls).
//
// Four actions, four different blast radii:
//   - «сохранить точку» never destroys anything and never bumps revision
//     (C12) — pure bookkeeping, no confirmation needed for the act itself.
//   - «откатить» and «сбросить всё к спеке» are genuinely destructive.
//     Both write their OWN pre-destructive checkpoint, server-side, in the
//     same transaction as the destruction (C5/C9/C10) — but that safety net
//     is not what the confirmation copy leads with, because the button's own
//     label already promises "reversible". What a person cannot see coming
//     from the button text is a relocated basePath, an invalidated signing
//     key, or a dropped auth preset — so those are what get named.
//   - «удалить» (P2d, SIG-DELCP) removes one history row outright and writes
//     NO safety-net checkpoint of its own — unlike rollback and reset there
//     is nothing left to undo it with afterward, so unlike those two the
//     confirmation copy leads with exactly that: this one has no undo.
//
// The outermost element carries data-testid="history-page" OUTSIDE every
// state switch below (§I of the P2c context, obs 17): a marker only on the
// success branch would make this screen's reachability depend on whether
// routes.test.tsx happened to mock every query it fires — exactly the gap
// obs 17 exists to close.
export function HistoryPage({ id }: { id: number }): ReactElement {
  const checkpoints = useListCheckpoints(id);
  const list = checkpoints.data?.status === 200 ? checkpoints.data.data.checkpoints : [];

  // Read a SECOND time here rather than threaded down as a prop from
  // WorkspaceLayout: OperationsPage's own A18 banner already reads
  // workspace.scenarioId the same way, for the same reason — it is the one
  // authority on whether a scenario is active, and every screen that needs
  // that fact reads it off this query rather than risk disagreeing with each
  // other about it. staleTime is 30s in production, so by the time this
  // screen mounts under WorkspaceLayout (which already fetched it to render
  // {children} at all) this is a cache hit, not a second round trip.
  const workspace = useGetWorkspace(id);
  const scenarioActive = workspace.data?.status === 200 && workspace.data.data.scenarioId !== null;
  const workspaceSlug = workspace.data?.status === 200 ? workspace.data.data.slug : "";

  return (
    <div data-testid="history-page">
      <Stack gap="md">
        <Title order={1}>История</Title>
        <Text size="sm" c="dimmed" data-testid="history-intro">
          Чекпойнт — снимок слоя воркспейса: настройки, правки операций, кастомные endpoint&apos;ы и
          подтверждённые ресурсы. При откате можно вернуть и сами записи ресурсов — флажком «вернуть
          и данные ресурсов», если эта точка их сохранила. Откат и сброс правок сохраняют свою
          собственную точку прямо перед тем, как что-то стереть, так что их можно отменить откатом
          на неё. Сброс ДАННЫХ ресурсов — нет: он необратим.
        </Text>
        <CreateCheckpointForm id={id} scenarioActive={scenarioActive} />
        <ResetOverridesCard id={id} scenarioActive={scenarioActive} />
        <ResetDataCard id={id} />
        {checkpoints.isPending ? (
          // role on the Text, not the Group: the live region should be the
          // sentence a screen reader announces, not the flex box around it.
          <Group gap="xs">
            <Loader size="sm" />
            <Text size="sm" component="output">
              Загрузка…
            </Text>
          </Group>
        ) : checkpoints.isError ? (
          <Stack gap="sm" data-testid="history-error">
            <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
              {describeApiFailure(checkpoints.error)}
            </Alert>
            <Button
              variant="default"
              w="fit-content"
              onClick={() => void checkpoints.refetch()}
              data-testid="history-retry"
            >
              Повторить
            </Button>
          </Stack>
        ) : checkpoints.data.status !== 200 ? (
          <Alert
            color="red"
            icon={<IconAlertTriangle size={18} />}
            role="alert"
            data-testid="history-error"
          >
            {describeApiFailure(null)}
          </Alert>
        ) : list.length === 0 ? (
          <Text data-testid="history-empty">
            Чекпойнтов пока нет. Сохраните первую точку формой выше — либо сделайте откат или сброс:
            оба тоже пишут точку перед тем, как что-то менять.
          </Text>
        ) : (
          <CheckpointList
            id={id}
            checkpoints={list}
            scenarioActive={scenarioActive}
            workspaceSlug={workspaceSlug}
          />
        )}
      </Stack>
    </div>
  );
}

// createdAt arrives as Unix seconds (internal/admin/checkpoint_handlers.go's
// summary view), the same convention every other screen in this app already
// documents for its own timestamps — dayjs needs telling which, or it reads
// 1970 for every row. Kept local rather than shared: SpecsPage, ScenariosPage
// and CustomEndpointsPage each keep their own three-line copy of exactly
// this, and a fourth copy is cheaper than a shared util two of those three
// would need to be retrofitted to use.
function formatTimestamp(unixSeconds: number): string {
  return dayjs.unix(unixSeconds).format("DD.MM.YYYY HH:mm");
}

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

function CreateCheckpointForm({
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
        <TextInput
          label="Метка точки"
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
      </Stack>
    </Card>
  );
}

function ResetOverridesCard({
  id,
  scenarioActive,
}: {
  id: number;
  scenarioActive: boolean;
}): ReactElement {
  const queryClient = useQueryClient();
  const [result, setResult] = useState<{ changed: boolean } | null>(null);
  const [failure, setFailure] = useState<string | null>(null);

  const resetOverrides = useResetOverrides({
    mutation: {
      onSuccess: (res) => {
        if (res.status !== 200) {
          return;
        }
        setFailure(null);
        setResult({ changed: res.data.changed });
        // C9: the pre-destructive checkpoint (when changed) lands in the
        // list, and revision only bumps when changed is true — but
        // invalidating both unconditionally costs one idle GET on the no-op
        // path and is simpler than this component re-deriving C9's own
        // no-op rule just to decide whether to invalidate.
        void queryClient.invalidateQueries({ queryKey: getListCheckpointsQueryKey(id) });
        void queryClient.invalidateQueries({ queryKey: getGetWorkspaceQueryKey(id) });
      },
      onError: (err) => setFailure(describeApiFailure(err)),
    },
  });

  function handleReset(): void {
    modals.openConfirmModal({
      title: "Сбросить всё к спеке",
      children: (
        <Stack gap="xs">
          <Text size="sm">
            Будут удалены ВСЕ правки операций и ВСЕ кастомные endpoint&apos;ы воркспейса — их нет в
            спеке, поэтому «сбросить всё к спеке» удаляет и то, и другое. В правки операций записан
            и пресет авторизации: он пропадёт вместе с остальными, и фронтенд под тестом перестанет
            логиниться. Settings (seed, basePath, ключ подписи) сброс не трогает.
          </Text>
          {scenarioActive ? (
            <Text size="sm" c="orange" data-testid="reset-scenario-warning">
              Сейчас активен сценарий — часть слоя воркспейса, к которому сброс вернёт спеку,
              по-прежнему останется замаскирована сценарием, пока его не деактивируют.
            </Text>
          ) : null}
          <Text size="sm" c="dimmed">
            Перед сбросом сохраняется точка текущего состояния — действие можно отменить откатом на
            неё.
          </Text>
        </Stack>
      ),
      labels: { confirm: "Сбросить", cancel: "Отмена" },
      confirmProps: { color: "red", "data-testid": "reset-confirm-submit" },
      onConfirm: () => {
        setFailure(null);
        resetOverrides.mutate({ id });
      },
    });
  }

  return (
    <Card withBorder p="md" data-testid="reset-overrides-card">
      <Stack gap="sm">
        {failure !== null ? (
          <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
            {failure}
          </Alert>
        ) : null}
        {result !== null ? (
          <Text size="sm" data-testid="reset-result">
            {result.changed
              ? "Сброшено. Правки операций и кастомные endpoint'ы удалены."
              : "Сбрасывать было нечего — правок и кастомных endpoint'ов уже не было."}
          </Text>
        ) : null}
        <Button
          variant="default"
          color="red"
          w="fit-content"
          leftSection={<IconAlertTriangle size={16} />}
          onClick={handleReset}
          loading={resetOverrides.isPending}
          data-testid="reset-overrides-button"
        >
          Сбросить всё к спеке
        </Button>
      </Stack>
    </Card>
  );
}

// D14.3's four "{routeFamily} — пропущено: …" lines, keyed by
// ResetDataSkippedFamilyReason's own enum values — the only place an
// operator learns why a family's data did not come back.
const SKIP_REASON_TEXT: Record<string, string> = {
  stranded: "семейства нет в текущей спеке",
  over_caps: "не помещается в лимиты",
  population_failed: "не удалось сгенерировать записи",
  group_skipped: "пропущено вместе с родителем или потомком, которого не удалось заполнить",
};

// The RESET half of the resources surface (P3b, D9): a card beside
// ResetOverridesCard above, not a modal — typing the workspace's own slug is
// itself the confirmation step, the same shape the destructive MCP tools use
// (confirmSlug), so there is nothing left for a second dialog to gate.
// Unlike ResetOverridesCard's confirmation, this card has NO undo to promise:
// reset-overrides writes its own pre-destructive checkpoint before it acts,
// and reset-data cannot — a checkpoint's config_snap never carries an entity
// row, so there is nothing this route's own effect could be rolled back
// from (D3, D8). The warning body says exactly that, and it is rendered
// with the card, not gated behind a click.
function ResetDataCard({ id }: { id: number }): ReactElement {
  const [mode, setMode] = useState<ResetMode>(ResetMode.reseed);
  const [slug, setSlug] = useState("");
  const [result, setResult] = useState<ResetDataResult | null>(null);
  const [failure, setFailure] = useState<string | null>(null);

  const resetData = useResetData({
    mutation: {
      onSuccess: (res) => {
        if (res.status !== 200) {
          return;
        }
        setFailure(null);
        setResult(res.data);
      },
      onError: (err) => {
        setResult(null);
        setFailure(describeApiFailureDetailed(err));
      },
    },
  });

  function handleReset(): void {
    setFailure(null);
    resetData.mutate({ id, data: { mode, confirmSlug: slug } });
  }

  function handleCancel(): void {
    setMode(ResetMode.reseed);
    setSlug("");
    setResult(null);
    setFailure(null);
  }

  return (
    <Card withBorder p="md" data-testid="reset-data-card">
      <Stack gap="sm">
        <Title order={3}>Сбросить данные ресурсов</Title>
        <Text size="sm" data-testid="reset-data-warning">
          Это НЕОБРАТИМО: записи, созданные через POST, будут удалены. В отличие от отката и сброса
          правок, «сбросить данные ресурсов» не сохраняет свою собственную точку перед тем, как
          стереть — если не сохранить чекпойнт вручную заранее, восстановить записи будет нечем.
          «Заполнить заново» запишет то, что даёт текущая конфигурация воркспейса, а не то, что было
          при подтверждении, и сбросит счётчик идентификаторов на размер новой популяции —
          идентификатор, который клиент уже получал и удалял, может быть выдан снова. «Очистить»
          оставит коллекции пустыми и НЕ сбросит счётчик — следующая запись получит следующий номер,
          а не первый.
        </Text>
        {failure !== null ? (
          <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
            {failure}
          </Alert>
        ) : null}
        {result !== null ? (
          <Stack gap={4} data-testid="reset-data-result">
            <Text size="sm">
              {result.changed ? `Удалено записей: ${result.deleted}` : "Ничего не изменилось"}
            </Text>
            {result.skipped.map((s) => (
              <Text size="sm" c="dimmed" key={`${s.routeFamily}-${s.reason}`}>
                {s.routeFamily} — пропущено: {SKIP_REASON_TEXT[s.reason]}
              </Text>
            ))}
          </Stack>
        ) : null}
        <SegmentedControl
          value={mode}
          onChange={(value) => setMode(value as ResetMode)}
          data={[
            { label: "Заполнить заново", value: ResetMode.reseed },
            { label: "Очистить", value: ResetMode.clear },
          ]}
          data-testid="reset-data-mode"
        />
        <TextInput
          label="Слаг воркспейса"
          value={slug}
          onChange={(e) => setSlug(e.currentTarget.value)}
          data-testid="reset-data-slug"
        />
        <Group gap="xs">
          <Button
            color="red"
            leftSection={<IconAlertTriangle size={16} />}
            onClick={handleReset}
            loading={resetData.isPending}
            data-testid="reset-data-submit"
          >
            Сбросить
          </Button>
          <Button variant="default" onClick={handleCancel} data-testid="reset-data-cancel">
            Отмена
          </Button>
        </Group>
      </Stack>
    </Card>
  );
}

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
function RollbackModalBody({
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
        <Button variant="default" onClick={onClose}>
          Отмена
        </Button>
        <Button color="red" onClick={handleConfirm} data-testid="checkpoint-rollback-confirm">
          Откатить
        </Button>
      </Group>
    </Stack>
  );
}

function CheckpointList({
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

  const rollback = useRollbackWorkspace({
    mutation: {
      onSuccess: () => {
        setActionError(null);
        void queryClient.invalidateQueries({ queryKey: getListCheckpointsQueryKey(id) });
        void queryClient.invalidateQueries({ queryKey: getGetWorkspaceQueryKey(id) });
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
                  {formatTimestamp(cp.createdAt)}
                  {cp.createdBy !== null ? ` · пользователь #${cp.createdBy}` : ""}
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
