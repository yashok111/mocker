import { useState } from "react";
import type { ReactElement } from "react";
import {
  Alert,
  Button,
  Divider,
  Group,
  NumberInput,
  PasswordInput,
  Select,
  Stack,
  Switch,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { IconAlertTriangle, IconDeviceFloppy } from "@tabler/icons-react";
import { Controller, useForm } from "react-hook-form";
import { type } from "arktype";
import { useQueryClient } from "@tanstack/react-query";
import {
  getGetWorkspaceQueryKey,
  usePatchWorkspace,
} from "@/api/generated/workspaces/workspaces.ts";
import {
  getGetAuthPresetQueryKey,
  getListWorkspaceOperationsQueryKey,
} from "@/api/generated/operations/operations.ts";
import { useListSpecs } from "@/api/generated/specs/specs.ts";
import type {
  EditConflictTombstone,
  Settings,
  WorkspaceConflictDetails,
  WorkspaceView,
} from "@/api/generated/schemas";
import { ApiFailure } from "@/api/client";
import { describeApiFailure, describeApiFailureDetailed, isGoneTombstone } from "@/api/errors";
import { arktypeResolver } from "@/validation/resolver";
import { userName } from "@/validation/name";

// SettingsPanel renders ONLY the fields the serving path actually reads
// (verified by grep against internal/mockplane, §3.8): seed, listSize,
// nullRate, delayMs, envelope and identity (name/email/roles) feed
// generation directly; auth feeds the JWT recipes the auth preset writes
// into. settings.validateRequests is declared in internal/domain/settings.go
// but read NOWHERE in internal/mockplane — a control for it would be a lie
// about what the server does, so it is preserved (never dropped) but never
// shown.
const settingsForm = type({
  name: userName,
  seed: "number.integer",
  listSize: "number.integer>=0",
  nullRate: "0<=number<=1",
  delayMs: "number.integer>=0",
  envelope: "string",
  identityName: "string>0",
  identityEmail: "string.email",
  // Comma-separated on screen, split into Settings.identity.roles on submit —
  // arktype validates the raw text field here; splitting/trimming happens at
  // submit time, same shape as WorkspacesPage's name.trim().
  identityRoles: "string",
  jwtTtlSec: "number.integer>0",
  alg: "string>0",
  signingKey: "string",
  requireHeader: "boolean",
});
type SettingsForm = typeof settingsForm.infer;

// Pick<WorkspaceView, "name" | "settings">, not the full WorkspaceView: this
// also seeds the form from a 409's WorkspaceConflictDetails (A3/D6), which
// carries exactly those two writable fields (plus specId/editVersion, which
// this form does not render) and nothing else WorkspaceView has.
function toFormValues(workspace: Pick<WorkspaceView, "name" | "settings">): SettingsForm {
  const s = workspace.settings;
  return {
    name: workspace.name,
    seed: s.seed,
    listSize: s.listSize,
    nullRate: s.nullRate,
    delayMs: s.delayMs,
    envelope: s.envelope ?? "",
    identityName: s.identity.name,
    identityEmail: s.identity.email,
    identityRoles: s.identity.roles.join(", "),
    jwtTtlSec: s.auth.jwtTtlSec,
    alg: s.auth.alg,
    signingKey: s.auth.signingKey,
    requireHeader: s.auth.requireHeader,
  };
}

// buildSettings is the ONLY place a Settings object gets constructed for the
// wire, and it always starts from the current one. PATCH replaces `settings`
// WHOLESALE (§3.8) — this is the third instance in this codebase of that
// data-loss class, and the easiest of the three to get wrong, because the
// eleven-field struct has only six fields on screen. Building from `{...}`
// instead of from `current` would silently wipe basePath, cors,
// notFoundBody, identity.id, identity.org and validateRequests, and a wiped
// auth.signingKey breaks the JWT recipes exactly like the other two
// instances. Spreading `current`, then `current.identity`, then
// `current.auth` before overriding only the edited leaves is what keeps
// every field this panel does not render byte-identical on the way out.
function buildSettings(current: Settings, values: SettingsForm): Settings {
  const roles = values.identityRoles
    .split(",")
    .map((r) => r.trim())
    .filter((r) => r !== "");
  return {
    ...current,
    seed: values.seed,
    listSize: values.listSize,
    nullRate: values.nullRate,
    delayMs: values.delayMs,
    envelope: values.envelope.trim() === "" ? null : values.envelope.trim(),
    identity: {
      ...current.identity,
      name: values.identityName.trim(),
      email: values.identityEmail.trim(),
      roles,
    },
    auth: {
      ...current.auth,
      jwtTtlSec: values.jwtTtlSec,
      alg: values.alg.trim(),
      signingKey: values.signingKey,
      requireHeader: values.requireHeader,
    },
  };
}

export function SettingsPanel({ workspace }: { workspace: WorkspaceView }): ReactElement {
  const queryClient = useQueryClient();
  const specs = useListSpecs();
  const {
    control,
    register,
    handleSubmit,
    reset,
    formState: { errors },
  } = useForm<SettingsForm>({
    resolver: arktypeResolver(settingsForm),
    defaultValues: toFormValues(workspace),
  });

  // editVersion (A3): the value to SEND is the one sitting beside the
  // document this screen is looking at right now. `pendingEditVersion`
  // starts null (nothing sent yet, fall back to the `workspace` prop below)
  // and is updated from two places: a write's own onSuccess adopts
  // `res.data.editVersion` (the same "adopt the response's own token"
  // OperationEditor.tsx does with setEditVersion), and a 409 adopts the
  // conflict's `details.editVersion` (D6). Both are needed: `workspace` is a
  // prop from the parent's useGetWorkspace read, which only updates once
  // this write's own invalidateQueries below has triggered an ASYNC refetch
  // — an operator who saves and immediately saves again, before that
  // refetch resolves, would otherwise send the pre-write token and get a
  // false edit_conflict against their own just-completed write.
  const [pendingEditVersion, setPendingEditVersion] = useState<number | null>(null);
  const editVersion = pendingEditVersion ?? workspace.editVersion;
  // The settings document the NEXT write is built on. buildSettings spreads
  // the fields this form has no control for (basePath, basePathValues,
  // cors, notFoundBody, identity.id/org) from its base; after a 409 that
  // base must be the conflict's own `details.settings`, or the retry would
  // resend the pre-conflict values of exactly the fields A3 exists to
  // protect — a reader of the 2026-09-05 UI review found it.
  const [conflictSettings, setConflictSettings] = useState<Settings | null>(null);

  // One mutation instance backs both this form's submit AND the spec-attach
  // control below: the branching on `variables.data.specId` in onSuccess is
  // what lets a single handler honour §3.9's two different invalidation
  // rows ("workspace" alone vs. "also operations list and auth-preset when
  // specId changed") without duplicating the mutation.
  const patchWorkspace = usePatchWorkspace({
    mutation: {
      onSuccess: (res, variables) => {
        if (res.status !== 200) {
          return;
        }
        setPendingEditVersion(res.data.editVersion);
        setConflictSettings(null);
        void queryClient.invalidateQueries({ queryKey: getGetWorkspaceQueryKey(workspace.id) });
        if (variables.data.specId !== undefined) {
          void queryClient.invalidateQueries({
            queryKey: getListWorkspaceOperationsQueryKey(workspace.id),
          });
          void queryClient.invalidateQueries({
            queryKey: getGetAuthPresetQueryKey(workspace.id),
          });
        }
      },
    },
  });

  function onSubmit(values: SettingsForm): void {
    const settings = buildSettings(conflictSettings ?? workspace.settings, values);
    patchWorkspace.mutate({
      id: workspace.id,
      data: { name: values.name.trim(), settings, editVersion },
    });
  }

  function attachSpec(specIdText: string | null): void {
    if (specIdText === null) {
      return;
    }
    const specId = Number(specIdText);
    if (!Number.isInteger(specId)) {
      return;
    }
    patchWorkspace.mutate({ id: workspace.id, data: { specId, editVersion } });
  }

  // handleConflictReload is D10's per-screen affordance: adopt what the 409
  // already carries (the current name/settings and its editVersion, or the
  // gone-tombstone) rather than issuing a second GET — D6's `details` IS the
  // retry material. A workspace tombstone means this workspace itself was
  // deleted from under the operator; there is no document to seed the form
  // from and no meaningful retry, so this only resets `pendingEditVersion`
  // to null (falling back to the — now certainly stale — prop) and lets the
  // generic error alert stand instead of pretending a reload fixed anything.
  function handleConflictReload(details: WorkspaceConflictDetails | EditConflictTombstone): void {
    if (isGoneTombstone(details)) {
      setPendingEditVersion(null);
      return;
    }
    reset(toFormValues(details));
    setPendingEditVersion(details.editVersion);
    setConflictSettings(details.settings);
  }

  const specOptions =
    specs.data?.status === 200
      ? specs.data.data.map((spec) => ({
          value: String(spec.id),
          label: `${spec.name} (v${spec.version})`,
        }))
      : [];

  return (
    <div data-testid="settings-panel">
      <Title order={2}>Настройки «{workspace.name}»</Title>
      <form onSubmit={handleSubmit(onSubmit)}>
        <Stack gap="sm" mt="sm">
          {(() => {
            const conflict =
              patchWorkspace.isError &&
              patchWorkspace.error instanceof ApiFailure &&
              patchWorkspace.error.code === "edit_conflict"
                ? patchWorkspace.error
                : null;
            if (conflict !== null) {
              return (
                <Alert
                  color="orange"
                  icon={<IconAlertTriangle size={18} />}
                  role="alert"
                  data-testid="settings-edit-conflict"
                >
                  <Text size="sm">{describeApiFailureDetailed(conflict)}</Text>
                  <Button
                    variant="light"
                    size="xs"
                    mt="xs"
                    onClick={() =>
                      handleConflictReload(
                        conflict.details as WorkspaceConflictDetails | EditConflictTombstone,
                      )
                    }
                    data-testid="settings-conflict-reload"
                  >
                    Загрузить актуальную версию
                  </Button>
                </Alert>
              );
            }
            return patchWorkspace.isError ? (
              <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
                {describeApiFailure(patchWorkspace.error)}
              </Alert>
            ) : null;
          })()}
          {patchWorkspace.isSuccess && patchWorkspace.variables?.data.settings !== undefined ? (
            <Text size="sm" c="teal" data-testid="settings-saved">
              Сохранено
            </Text>
          ) : null}

          <TextInput
            label="Название"
            data-testid="settings-name"
            error={errors.name?.message}
            {...register("name")}
          />

          <Divider label="Генерация" labelPosition="left" />
          <Group grow>
            <Controller
              control={control}
              name="seed"
              render={({ field }) => (
                <NumberInput
                  label="Seed"
                  description="Ключ детерминизма: тот же seed и та же спека дают побайтово одинаковые тела"
                  data-testid="settings-seed"
                  value={field.value}
                  onChange={(v) => field.onChange(typeof v === "number" ? v : Number(v))}
                  error={errors.seed?.message}
                />
              )}
            />
            <Controller
              control={control}
              name="listSize"
              render={({ field }) => (
                <NumberInput
                  label="Размер списков"
                  min={0}
                  data-testid="settings-list-size"
                  value={field.value}
                  onChange={(v) => field.onChange(typeof v === "number" ? v : Number(v))}
                  error={errors.listSize?.message}
                />
              )}
            />
          </Group>
          <Group grow>
            <Controller
              control={control}
              name="nullRate"
              render={({ field }) => (
                <NumberInput
                  label="Доля null-полей"
                  min={0}
                  max={1}
                  step={0.05}
                  data-testid="settings-null-rate"
                  value={field.value}
                  onChange={(v) => field.onChange(typeof v === "number" ? v : Number(v))}
                  error={errors.nullRate?.message}
                />
              )}
            />
            <Controller
              control={control}
              name="delayMs"
              render={({ field }) => (
                <NumberInput
                  label="Задержка ответа, мс"
                  min={0}
                  data-testid="settings-delay-ms"
                  value={field.value}
                  onChange={(v) => field.onChange(typeof v === "number" ? v : Number(v))}
                  error={errors.delayMs?.message}
                />
              )}
            />
          </Group>
          <TextInput
            label="Конверт ответа"
            description="Пусто — без конверта"
            data-testid="settings-envelope"
            error={errors.envelope?.message}
            {...register("envelope")}
          />

          <Divider label="Личность" labelPosition="left" />
          <Group grow>
            <TextInput
              label="Имя"
              data-testid="settings-identity-name"
              error={errors.identityName?.message}
              {...register("identityName")}
            />
            <TextInput
              label="E-mail"
              data-testid="settings-identity-email"
              error={errors.identityEmail?.message}
              {...register("identityEmail")}
            />
          </Group>
          <TextInput
            label="Роли"
            description="Через запятую"
            data-testid="settings-identity-roles"
            error={errors.identityRoles?.message}
            {...register("identityRoles")}
          />

          <Divider label="Авторизация" labelPosition="left" />
          <PasswordInput
            label="Ключ подписи JWT"
            data-testid="settings-signing-key"
            error={errors.signingKey?.message}
            {...register("signingKey")}
          />
          <Group grow>
            <Controller
              control={control}
              name="jwtTtlSec"
              render={({ field }) => (
                <NumberInput
                  label="TTL токена, сек"
                  min={1}
                  data-testid="settings-jwt-ttl"
                  value={field.value}
                  onChange={(v) => field.onChange(typeof v === "number" ? v : Number(v))}
                  error={errors.jwtTtlSec?.message}
                />
              )}
            />
            <TextInput
              label="Алгоритм"
              data-testid="settings-alg"
              error={errors.alg?.message}
              {...register("alg")}
            />
          </Group>
          <Controller
            control={control}
            name="requireHeader"
            render={({ field }) => (
              <Switch
                label="Требовать заголовок Authorization"
                data-testid="settings-require-header"
                checked={field.value}
                onChange={(e) => field.onChange(e.currentTarget.checked)}
              />
            )}
          />

          <Button
            type="submit"
            w="fit-content"
            leftSection={<IconDeviceFloppy size={16} />}
            loading={patchWorkspace.isPending}
            data-testid="settings-submit"
          >
            Сохранить
          </Button>
        </Stack>
      </form>

      <Divider label="Спека" labelPosition="left" mt="md" />
      <Stack gap="xs" mt="xs">
        <Text size="sm" c="dimmed">
          {workspace.specId === null
            ? "Спека не привязана"
            : `Привязана спека #${workspace.specId}`}
        </Text>
        <Group align="flex-end">
          <Select
            label="Привязать спеку"
            placeholder="Выберите спеку"
            data={specOptions}
            searchable
            value={workspace.specId === null ? null : String(workspace.specId)}
            onChange={attachSpec}
            data-testid="settings-spec-select"
          />
        </Group>
        {patchWorkspace.isSuccess && patchWorkspace.variables?.data.specId !== undefined ? (
          <Text size="sm" c="teal" data-testid="settings-spec-attached">
            Спека привязана
          </Text>
        ) : null}
        {/* Отвязать нельзя: PatchWorkspaceRequest.specId decodes into a *int64,
            so an explicit null and an omitted field are indistinguishable —
            a button here would silently do nothing, which is worse than no
            button at all (§3.8). */}
        <Text size="xs" c="dimmed">
          Отвязать спеку через этот экран пока нельзя.
        </Text>
      </Stack>
    </div>
  );
}
