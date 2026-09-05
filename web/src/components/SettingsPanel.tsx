import { useState } from "react";
import type { ReactElement } from "react";
import {
  Alert,
  Button,
  Divider,
  Group,
  NativeSelect,
  NumberInput,
  PasswordInput,
  Select,
  Stack,
  Switch,
  Text,
  Textarea,
  TextInput,
  Title,
} from "@mantine/core";
import { IconAlertTriangle, IconDeviceFloppy } from "@tabler/icons-react";
import { Controller, useForm } from "react-hook-form";
import { type } from "arktype";
import { modals } from "@mantine/modals";
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

// SettingsPanel renders the fields the serving path actually reads (verified
// by grep against internal/mockplane, §3.8): seed, listSize, nullRate,
// delayMs, envelope and identity feed generation directly; auth feeds the
// JWT recipes the auth preset writes into. A21 (G1, four of five readers of
// the 2026-09-05 UI review) added the four the panel had preserved and never
// shown, which decide where and to whom the mock serves at all: basePath
// (every route lives under it), basePathValues (a {param} basePath serves
// NOTHING until its values are declared, and the screen could not say why),
// cors (the mock plane exists to be called from a browser), notFoundBody
// (what every unmatched route answers), plus identity.id (org is an object
// — id, name, type — and stays the agent's, preserved by the spread).
// settings.validateRequests is declared in internal/domain/settings.go but
// read NOWHERE in internal/mockplane — a control for it would be a lie about
// what the server does, so it is preserved (never dropped) but never shown.
const notFoundBodyField = type("string").narrow((value, ctx) => {
  if (value.trim() === "") {
    return true;
  }
  try {
    JSON.parse(value);
    return true;
  } catch (err) {
    return ctx.reject({
      problem: `JSON невалиден (${err instanceof Error ? err.message : String(err)})`,
    });
  }
});

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
  identityId: "string",
  jwtTtlSec: "number.integer>0",
  alg: "string>0",
  signingKey: "string",
  requireHeader: "boolean",
  basePath: "string",
  // One base tuple per line on screen; Settings.basePathValues on the wire.
  basePathValuesText: "string",
  corsMode: "string",
  corsCredentials: "boolean",
  // JSON text, or empty for "unset" — the server serves its own 404 then.
  notFoundBodyText: notFoundBodyField,
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
    identityId: s.identity.id === undefined || s.identity.id === null ? "" : String(s.identity.id),
    jwtTtlSec: s.auth.jwtTtlSec,
    alg: s.auth.alg,
    signingKey: s.auth.signingKey,
    requireHeader: s.auth.requireHeader,
    basePath: s.basePath,
    basePathValuesText: (s.basePathValues ?? []).join("\n"),
    corsMode: s.cors.mode,
    corsCredentials: s.cors.credentials,
    notFoundBodyText:
      s.notFoundBody === undefined || s.notFoundBody === null
        ? ""
        : JSON.stringify(s.notFoundBody, null, 2),
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
function parseScalar(text: string): unknown {
  try {
    const v: unknown = JSON.parse(text);
    return typeof v === "number" || typeof v === "boolean" ? v : text;
  } catch {
    return text;
  }
}

function buildSettings(current: Settings, values: SettingsForm): Settings {
  const roles = values.identityRoles
    .split(",")
    .map((r) => r.trim())
    .filter((r) => r !== "");
  const basePathValues = values.basePathValuesText
    .split("\n")
    .map((v) => v.trim())
    .filter((v) => v !== "");
  const notFoundBodyText = values.notFoundBodyText.trim();
  // The same normalisation the server applies (domain.NormalizeBasePath):
  // a leading slash, no trailing one, "" for "/" — so the connect panel's
  // origin + basePath never reads "…:8080api/v1".
  const rawBase = values.basePath.trim();
  const basePath =
    rawBase === "" || rawBase === "/"
      ? ""
      : (rawBase.startsWith("/") ? rawBase : `/${rawBase}`).replace(/\/+$/, "");
  // identity.id is "any JSON scalar" on the wire. An UNCHANGED text keeps the
  // stored value byte-for-byte (a number stays a number, "00123" stays the
  // string it was — the second reader of A21 caught the coercion); a changed
  // text is read as a JSON scalar when it is one (42, true) and as the
  // string typed otherwise; empty means unset.
  const idText = values.identityId.trim();
  const storedId = current.identity.id;
  const identityId =
    idText === ""
      ? undefined
      : storedId !== undefined && storedId !== null && idText === String(storedId)
        ? storedId
        : parseScalar(idText);
  return {
    ...current,
    seed: values.seed,
    listSize: values.listSize,
    nullRate: values.nullRate,
    delayMs: values.delayMs,
    envelope: values.envelope.trim() === "" ? null : values.envelope.trim(),
    basePath,
    // Omitted when there are none, and omitted when the path carries no
    // {param}: the box is hidden then, but react-hook-form keeps its value,
    // and the server refuses values beside a param-free path by name.
    basePathValues:
      !basePath.includes("{") || basePathValues.length === 0 ? undefined : basePathValues,
    cors: { ...current.cors, mode: values.corsMode, credentials: values.corsCredentials },
    notFoundBody: notFoundBodyText === "" ? undefined : (JSON.parse(notFoundBodyText) as unknown),
    identity: {
      ...current.identity,
      name: values.identityName.trim(),
      email: values.identityEmail.trim(),
      roles,
      id: identityId,
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
    watch,
    formState: { errors },
  } = useForm<SettingsForm>({
    resolver: arktypeResolver(settingsForm),
    defaultValues: toFormValues(workspace),
  });
  const basePathHasParam = watch("basePath").includes("{");

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
    // A21 (G12): a FIRST bind needs no ceremony; a RE-bind swaps the
    // ground every override and confirmed resource stands on, so it asks —
    // and the success line below points at «Проверить спеку», which is
    // where the consequences show.
    if (workspace.specId === null || workspace.specId === specId) {
      patchWorkspace.mutate({ id: workspace.id, data: { specId, editVersion } });
      return;
    }
    modals.openConfirmModal({
      title: "Сменить спеку",
      children: (
        <Text size="sm">
          Правки операций, которых в новой спеке нет, и подтверждённые ресурсы без семейства
          перестанут действовать, но не удалятся: после смены нажмите «Проверить спеку» на этой же
          странице — там видно, что осталось без основания, и как это починить.
        </Text>
      ),
      labels: { confirm: "Сменить", cancel: "Отмена" },
      confirmProps: { "data-testid": "settings-spec-rebind-confirm" },
      onConfirm: () => patchWorkspace.mutate({ id: workspace.id, data: { specId, editVersion } }),
    });
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
          <TextInput
            label="Идентификатор (необязательно)"
            description="Число или строка — как в спеке. Организация (identity.org) — объект, правится агентом и сохраняется как есть"
            data-testid="settings-identity-id"
            {...register("identityId")}
          />

          <Divider label="Маршрут и доступ" labelPosition="left" />
          <TextInput
            label="Базовый путь"
            description="Префикс всех маршрутов; из спеки при привязке. Может содержать параметр вида /tenants/{t}"
            placeholder="/api/v1"
            data-testid="settings-base-path"
            error={errors.basePath?.message}
            {...register("basePath")}
          />
          {basePathHasParam ? (
            <Textarea
              label="Значения параметров базового пути — по одному на строку"
              description="Без них маршрут с параметром не обслуживается. Для нескольких параметров — значения через «/» в порядке появления"
              rows={3}
              data-testid="settings-base-path-values"
              {...register("basePathValuesText")}
            />
          ) : null}
          <Group grow align="flex-end">
            <NativeSelect label="CORS" data-testid="settings-cors-mode" {...register("corsMode")}>
              <option value="reflect">отражать Origin запроса (любой источник)</option>
              <option value="off">не отвечать CORS-заголовками</option>
              {/* The server knows two modes and reads anything but "off" as
                  "reflect" (internal/domain/settings.go); a stored value
                  outside the two stays selectable so an untouched form
                  resends it byte-for-byte (the wholesale-PATCH invariant),
                  labelled with what the mock plane actually does with it. */}
              {watch("corsMode") !== "reflect" && watch("corsMode") !== "off" ? (
                <option value={watch("corsMode")}>
                  {watch("corsMode")} — сервер читает как «отражать»
                </option>
              ) : null}
            </NativeSelect>
            <Controller
              control={control}
              name="corsCredentials"
              render={({ field }) => (
                <Switch
                  label="Разрешить credentials (cookie, Authorization из браузера)"
                  checked={field.value}
                  onChange={(e) => field.onChange(e.currentTarget.checked)}
                  data-testid="settings-cors-credentials"
                />
              )}
            />
          </Group>
          <Textarea
            label="Тело ответа 404 (JSON, необязательно)"
            description="Пусто — ответ 404 сервера по умолчанию"
            rows={3}
            data-testid="settings-not-found-body"
            error={errors.notFoundBodyText?.message}
            {...register("notFoundBodyText")}
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

      <Divider label="Спека" labelPosition="left" mt="md" id="settings-spec" />
      <Stack gap="xs" mt="xs">
        <Text size="sm" c="dimmed">
          {workspace.specId === null
            ? "Спека не привязана"
            : `Привязана спека: ${specOptions.find((o) => o.value === String(workspace.specId))?.label ?? `#${workspace.specId}`}`}
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
            Спека привязана — проверьте соответствие кнопкой «Проверить спеку» ниже
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
