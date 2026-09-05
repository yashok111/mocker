import { useEffect, useRef, useState } from "react";
import type { ReactElement } from "react";
import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Card,
  Code,
  Divider,
  Group,
  Loader,
  NativeSelect,
  NumberInput,
  Stack,
  Switch,
  Tabs,
  Text,
  Textarea,
  TextInput,
  Title,
} from "@mantine/core";
import { modals } from "@mantine/modals";
import {
  IconAlertTriangle,
  IconDeviceFloppy,
  IconPlus,
  IconRestore,
  IconTrash,
} from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import { ApiFailure } from "@/api/client";
import {
  getGetOperationOverrideQueryKey,
  getListWorkspaceOperationsQueryKey,
  useDeleteOperationOverride,
  useGetOperationOverride,
  usePreviewOperation,
  usePutOperationOverride,
} from "@/api/generated/operations/operations.ts";
import { getGetWorkspaceQueryKey } from "@/api/generated/workspaces/workspaces.ts";
import type {
  Condition,
  EditConflictTombstone,
  MergedStatusView,
  OverrideConflictDetails,
  OverrideMutableFields,
  PreviewResultView,
  Variant,
} from "@/api/generated/schemas";
import {
  describeApiFailureDetailed,
  describePreviewRefusalReason,
  isGoneTombstone,
} from "@/api/errors";

// OperationEditor is the right-hand pane of DESIGN §14 screen 5. It is built
// around ONE invariant, spelled out in the phase brief (§3.3 of the phase
// context) and worth repeating here because it is what this whole file
// exists to protect:
//
//   PUT /api/workspaces/{id}/operations/{opKey} is a FULL REPLACEMENT.
//   internal/admin/override_handlers.go assigns cur.Responses = body.Responses
//   wholesale, and the auth preset writes its JWT/identity recipes into
//   those same variants. A PUT that omits a status, or rebuilds a variant
//   from only the fields this editor happens to render, SILENTLY deletes
//   recipes that make the mocked login return a token.
//
// The fix is structural, not a checklist: `fields` below is the WHOLE
// OverrideMutableFields document, seeded once from the GET, and every
// control mutates it through `updateVariant`/`setFields`, which always
// spread the previous value first. Nothing in this file ever constructs a
// Variant or OverrideMutableFields from scratch out of the fields it shows.

// jsonLocation turns JSON.parse's own SyntaxError into "строка N, столбец M"
// — a byte offset into a multi-line textarea is not something a person can
// use without counting characters by hand. Small enough, and specific enough
// to this file's Textarea, that it is not worth promoting into a shared
// module across the three files this phase's "operations" slice owns.
function jsonLocation(text: string, err: unknown): string {
  const message = err instanceof Error ? err.message : String(err);
  const match = /position (\d+)/.exec(message);
  if (!match) {
    return message;
  }
  const pos = Number(match[1]);
  const before = text.slice(0, pos);
  const line = before.split("\n").length;
  const column = pos - before.lastIndexOf("\n");
  return `строка ${line}, столбец ${column}`;
}

function emptyDocument(): OverrideMutableFields {
  // The baseline for "nothing overridden yet" (§3.3: a 404 on the GET is the
  // normal answer, not an error). listSize/delayMs/failDirective/validateReq
  // are all left unset rather than defaulted here — sending them as explicit
  // zeros would claim credit for a choice this screen never made.
  return { overrideOn: false, routeOff: false, responses: {} };
}

// selectorSort puts numeric statuses in ascending order first (200, 201, 404,
// 500…), then wildcard/default selectors ("2XX", "default") alphabetically
// after them — the order an operator scans a status list in.
function selectorSort(a: string, b: string): number {
  const na = Number(a);
  const nb = Number(b);
  const aNum = Number.isFinite(na);
  const bNum = Number.isFinite(nb);
  if (aNum && bNum) return na - nb;
  if (aNum) return -1;
  if (bNum) return 1;
  return a.localeCompare(b);
}

// declaredPathParams is D10/R1's own derivation, restated on the frontend so
// the preview panel's required inputs match what the server will actually
// require: the {param} segments of basePath + the operation's path, NEVER
// of the operation's path alone. A workspace whose basePath carries a
// parameter (e.g. "/orgs/{orgId}") has a real segment in every route it
// serves, and a panel that only looked at the operation's own path would
// render no input for it — every preview it could issue would then be a
// guaranteed 400 missing_path_param.
function declaredPathParams(basePath: string, operationPath: string): string[] {
  const full = `${basePath}${operationPath}`;
  const names = new Set<string>();
  for (const match of full.matchAll(/\{([^}]+)\}/g)) {
    const name = match[1];
    if (name !== undefined) {
      names.add(name);
    }
  }
  return Array.from(names);
}

const DEFAULT_CONDITION: Condition = { in: "query", name: "", op: "equals", value: "" };

const IN_OPTIONS: { value: Condition["in"]; label: string }[] = [
  { value: "query", label: "query-параметр" },
  { value: "header", label: "заголовок" },
  { value: "body", label: "тело" },
];

const OP_OPTIONS: { value: Condition["op"]; label: string }[] = [
  { value: "equals", label: "равно" },
  { value: "contains", label: "содержит" },
  { value: "exists", label: "присутствует" },
];

export function OperationEditor({
  workspaceId,
  opKey,
  statuses,
  path = "",
  basePath = "",
}: {
  workspaceId: number;
  opKey: string;
  statuses: MergedStatusView[];
  // path and basePath exist ONLY to derive the preview panel's required
  // path-parameter inputs (R1) — OperationsPage already holds both from its
  // own useGetWorkspace/useListWorkspaceOperations, so they arrive as props
  // rather than this component running a second query for them. Optional
  // (default "") rather than required: this file owns the props, but every
  // existing call site in OperationEditor.test.tsx belongs to a section
  // this slice does not touch (HARD RULE 6), and none of them needs the
  // preview panel's path-parameter inputs to exercise the save/reset/status
  // flows they actually test — an operation with no {param} segments at all
  // renders the panel with zero required inputs either way.
  path?: string;
  basePath?: string;
}): ReactElement {
  // retry: false (§3.3/§3.1): a 404 here means "nothing is overridden yet",
  // the normal answer, not worth a second automatic round trip.
  const override = useGetOperationOverride(workspaceId, opKey, { query: { retry: false } });
  const is404 =
    override.isError && override.error instanceof ApiFailure && override.error.status === 404;

  const [fields, setFields] = useState<OverrideMutableFields | null>(null);
  // editVersion (A3): the compare-and-swap expectation this screen will send
  // on its next PUT — always the value sitting BESIDE the document currently
  // on screen (this read's own `doc.editVersion`, or 0 for "no row yet" on a
  // 404 — property 3), never re-fetched at submit time. That is D10's own
  // wiring: the vacuous implementation adds a request, the correct one adds
  // a field. Kept as a sibling of `fields` rather than folded into it
  // because OverrideMutableFields (and PutOperationRequest's own allOf) has
  // no room for it — it travels beside the PUT body, not inside it.
  const [editVersion, setEditVersion] = useState<number | null>(null);
  const [savedNote, setSavedNote] = useState<string | null>(null);
  // Per-selector "this status's body textarea currently holds invalid JSON".
  // StatusPanel keeps the actual error text local (it needs it for its own
  // Textarea `error` prop), but the SAVE BUTTON lives here, in the parent —
  // without this, a status with unparseable JSON in its draft would still
  // let the operator click «Сохранить», which sends the last successfully
  // parsed body while the screen keeps showing the broken text.
  const [bodyErrors, setBodyErrors] = useState<Record<string, boolean>>({});
  const hasBodyError = Object.values(bodyErrors).some(Boolean);

  // Preview (P2f): a WINDOW onto what the draft above would produce once
  // saved, not another editor — the panel below only ever reads `fields`,
  // never writes to it. Query/pathParams are the panel's own inputs; the
  // request also carries the CURRENT draft and the tab currently open, so
  // "Показать пример" always renders the status the operator is looking at.
  const previewParamNames = declaredPathParams(basePath, path);
  const [previewQuery, setPreviewQuery] = useState("");
  const [previewPathParams, setPreviewPathParams] = useState<Record<string, string>>({});
  const preview = usePreviewOperation();

  useEffect(() => {
    // Only ever initializes ONCE per mount. OperationsPage renders this
    // component with key={opKey}, so a different operation is a fresh
    // mount — this effect is not what resets state on selection change, and
    // must not also fire again after a save's own invalidation refetches
    // the same query with a moved revision, which would stomp local edits
    // that are already the source of truth at that point.
    //
    // react/set-state-in-effect says to derive this during render instead —
    // `edits ?? serverDoc` — and that was tried and reverted. It reintroduces
    // exactly the re-entry this guard exists to stop, through a path the guard
    // cannot see: typing INVALID JSON into a status body moves StatusPanel's
    // own bodyDraft but NOT this state, because handleBodyChange only commits
    // on a successful parse. A derived document then follows the server on the
    // next refetch, StatusPanel's reconciliation effect sees a changed
    // serverBodyText and clears the draft — the operator's half-written body
    // disappears. refetchOnWindowFocus is off (main.tsx), but refetchOnReconnect
    // is not, so a dropped connection is enough. Freezing at first load is the
    // behaviour this screen wants; an effect is how you freeze on data that
    // arrives asynchronously.
    if (fields !== null) {
      return;
    }
    if (override.isSuccess && override.data.status === 200) {
      const doc = override.data.data;
      // Copied field-by-field, not spread wholesale: doc also carries
      // method/path/opKey/updatedAt, which OverrideMutableFields (and the
      // PUT body) has no room for.
      // oxlint-disable-next-line react/set-state-in-effect -- see the block comment above: deriving during render loses an in-progress body draft
      setFields({
        overrideOn: doc.overrideOn,
        routeOff: doc.routeOff,
        activeStatus: doc.activeStatus,
        responses: doc.responses,
        listSize: doc.listSize,
        delayMs: doc.delayMs,
        failDirective: doc.failDirective,
        validateReq: doc.validateReq,
      });
      // oxlint-disable-next-line react/set-state-in-effect -- same reason as above
      setEditVersion(doc.editVersion);
    } else if (is404) {
      // oxlint-disable-next-line react/set-state-in-effect -- same reason as above
      setFields(emptyDocument());
      // 0 means "I expect no row" (property 3) — a 404 here is the normal
      // no-override-yet answer, and the first PUT from this screen is
      // therefore a CREATE, not a stale-token retry.
      // oxlint-disable-next-line react/set-state-in-effect -- same reason as above
      setEditVersion(0);
    }
  }, [fields, override.isSuccess, override.data, is404]);

  // updateVariant is the ONE place a status's Variant is ever written.
  // Every caller passes an updater that spreads its `v` argument first, so
  // whatever this editor is not currently showing for that status —
  // recipes, schemaPatch, headers, bodyEncoding — survives untouched, and
  // every OTHER status key in `responses` is preserved by the outer spread
  // below regardless of what this call touches.
  function updateVariant(selector: string, updater: (v: Variant) => Variant): void {
    setFields((prev) => {
      if (prev === null) {
        return prev;
      }
      const current = prev.responses[selector] ?? { mode: "generated" };
      return { ...prev, responses: { ...prev.responses, [selector]: updater(current) } };
    });
  }

  const queryClient = useQueryClient();

  function invalidateAfterWrite(): void {
    // §3.9: usePutOperationOverride/useDeleteOperationOverride must
    // invalidate the workspace, the operations list, and this override doc.
    void queryClient.invalidateQueries({ queryKey: getGetWorkspaceQueryKey(workspaceId) });
    void queryClient.invalidateQueries({
      queryKey: getListWorkspaceOperationsQueryKey(workspaceId),
    });
    void queryClient.invalidateQueries({
      queryKey: getGetOperationOverrideQueryKey(workspaceId, opKey),
    });
  }

  const putOverride = usePutOperationOverride({
    mutation: {
      onSuccess: (res) => {
        if (res.status !== 200) {
          return;
        }
        setSavedNote(`Сохранено, ревизия воркспейса ${res.data.revision}`);
        // Adopt the token the write itself returned (property 8/D10's 1c):
        // without this, a second save on the same screen right after the
        // first would send the now-stale editVersion it seeded with and
        // conflict against its OWN prior write.
        setEditVersion(res.data.editVersion);
        invalidateAfterWrite();
      },
    },
  });

  const deleteOverride = useDeleteOperationOverride({
    mutation: {
      onSuccess: (res) => {
        // 200 = a row was actually removed (revision moved); 204 = there
        // was nothing to remove. Both are success (§3.3) — a UI that reads
        // res.data.revision unconditionally crashes on the 204, where
        // customFetch (§3.1) leaves `data` undefined.
        setSavedNote(
          res.status === 200
            ? `Сброшено, ревизия воркспейса ${res.data.revision}`
            : "Сбрасывать было нечего — переопределения не было",
        );
        setFields(emptyDocument());
        // The row is gone either way (a real delete or "nothing to
        // remove"), so the next PUT from this screen expects no row — same
        // as the 404 seeding case above.
        setEditVersion(0);
        invalidateAfterWrite();
      },
    },
  });

  function handleSave(): void {
    if (fields === null || editVersion === null) {
      return;
    }
    setSavedNote(null);
    putOverride.mutate({ id: workspaceId, opKey, data: { ...fields, editVersion } });
  }

  // handleConflictReload is A3/D10's per-screen affordance for the four
  // single-object routes: adopt what the 409 ITSELF already carries — the
  // current document, or the gone-tombstone — rather than issuing a second
  // GET, since D6's whole point is that `details` already IS the retry
  // material the caller needs.
  function handleConflictReload(details: OverrideConflictDetails | EditConflictTombstone): void {
    if (isGoneTombstone(details)) {
      setFields(emptyDocument());
      setEditVersion(0);
      setSavedNote(null);
      return;
    }
    setFields({
      overrideOn: details.overrideOn,
      routeOff: details.routeOff,
      activeStatus: details.activeStatus,
      responses: details.responses,
      listSize: details.listSize,
      delayMs: details.delayMs,
      failDirective: details.failDirective,
      validateReq: details.validateReq,
    });
    setEditVersion(details.editVersion);
    setSavedNote(null);
  }

  function handleReset(): void {
    modals.openConfirmModal({
      title: "Сбросить переопределение",
      children: (
        <Text size="sm">
          Убрать переопределение этой операции и вернуться к тому, что генерирует спека?
          Закреплённые тела и условия для всех статусов будут потеряны, включая то, что подставляет
          значения в тело.
        </Text>
      ),
      labels: { confirm: "Сбросить", cancel: "Отмена" },
      confirmProps: { color: "red", "data-testid": "operation-reset-confirm" },
      onConfirm: () => {
        setSavedNote(null);
        deleteOverride.mutate({ id: workspaceId, opKey });
      },
    });
  }

  const allSelectors = fields
    ? Array.from(
        new Set([...statuses.map((s) => s.selector), ...Object.keys(fields.responses)]),
      ).sort(selectorSort)
    : [];
  const [activeTab, setActiveTab] = useState<string | null>(null);
  const currentTab =
    activeTab !== null && allSelectors.includes(activeTab) ? activeTab : (allSelectors[0] ?? null);

  // Not just `statuses` (the spec-declared variants): a session/traffic-driven
  // pin can carry a status the spec never declares — e.g. `to-override` pins
  // responses["500"] for an observed 500 on an operation whose spec only
  // lists 200/404 (internal/mockplane/respond.go's activeStatus is explicitly
  // allowed to name such a status). allSelectors already unions the spec's
  // statuses with fields.responses' keys, so pull numeric candidates from
  // there too, plus the document's own activeStatus in case it names a
  // status with no variant at all — otherwise a document that already
  // carries such an activeStatus would render as "не задан", the opposite of
  // what is stored.
  const numericStatuses = Array.from(
    new Set(
      [
        ...statuses.map((s) => s.httpStatus),
        ...allSelectors.map((s) => Number(s)),
        ...(fields?.activeStatus !== undefined ? [fields.activeStatus] : []),
      ].filter((n) => Number.isInteger(n)),
    ),
  ).sort((a, b) => a - b);

  // handlePreview sends the CURRENT draft (`fields`, unsaved) plus whichever
  // status tab is open. currentTab can be a wildcard selector ("2XX",
  // "default") that the wire's `status` field cannot express — D10 pins it
  // as a 3-digit status code — so a non-numeric tab is sent as omitted and
  // the full precedence (when[], active_status, document default) runs
  // instead, exactly as it would once this draft is actually saved.
  function handlePreview(): void {
    if (fields === null) {
      return;
    }
    preview.mutate({
      id: workspaceId,
      data: {
        opKey,
        draft: fields,
        status: currentTab !== null && /^\d{3}$/.test(currentTab) ? currentTab : undefined,
        query: previewQuery === "" ? undefined : previewQuery,
        pathParams: previewPathParams,
      },
    });
  }

  return (
    <div data-testid="operation-editor">
      <Title order={3} mb="xs">
        {opKey.split("%20").length > 1 ? decodeURIComponent(opKey) : opKey}
      </Title>
      {override.isPending ? (
        <Group gap="xs">
          <Loader size="sm" />
          <Text size="sm" component="output">
            Загрузка…
          </Text>
        </Group>
      ) : override.isError && !is404 ? (
        <Stack gap="sm">
          <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
            {describeApiFailureDetailed(override.error)}
          </Alert>
          <Button
            variant="default"
            w="fit-content"
            onClick={() => void override.refetch()}
            data-testid="operation-editor-retry"
          >
            Повторить
          </Button>
        </Stack>
      ) : override.isSuccess && override.data.status !== 200 ? (
        <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
          {describeApiFailureDetailed(null)}
        </Alert>
      ) : fields === null ? (
        // A one-tick gap between the query settling and the seeding effect
        // above committing; renders as a beat of "Загрузка…" rather than
        // reading fields.overrideOn on a still-null value.
        <Group gap="xs">
          <Loader size="sm" />
          <Text size="sm" component="output">
            Загрузка…
          </Text>
        </Group>
      ) : (
        <Stack gap="md">
          {(() => {
            const conflict =
              putOverride.isError &&
              putOverride.error instanceof ApiFailure &&
              putOverride.error.code === "edit_conflict"
                ? putOverride.error
                : null;
            if (conflict !== null) {
              return (
                <Alert
                  color="orange"
                  icon={<IconAlertTriangle size={18} />}
                  role="alert"
                  data-testid="operation-edit-conflict"
                >
                  <Text size="sm">{describeApiFailureDetailed(conflict)}</Text>
                  <Button
                    variant="light"
                    size="xs"
                    mt="xs"
                    onClick={() =>
                      handleConflictReload(
                        conflict.details as OverrideConflictDetails | EditConflictTombstone,
                      )
                    }
                    data-testid="operation-conflict-reload"
                  >
                    Загрузить актуальную версию
                  </Button>
                </Alert>
              );
            }
            return putOverride.isError ? (
              <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
                {describeApiFailureDetailed(putOverride.error)}
              </Alert>
            ) : null;
          })()}
          {deleteOverride.isError ? (
            <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
              {describeApiFailureDetailed(deleteOverride.error)}
            </Alert>
          ) : null}
          {savedNote !== null ? (
            <Text size="sm" data-testid="operation-editor-saved">
              {savedNote}
            </Text>
          ) : null}

          <Group>
            <Switch
              label="Переопределение включено"
              checked={fields.overrideOn}
              onChange={(e) =>
                setFields((prev) =>
                  prev === null ? prev : { ...prev, overrideOn: e.currentTarget.checked },
                )
              }
              data-testid="operation-override-on"
            />
            <Switch
              label="Операция выключена — мок перестаёт на неё отвечать"
              color="red"
              checked={fields.routeOff}
              onChange={(e) =>
                setFields((prev) =>
                  prev === null ? prev : { ...prev, routeOff: e.currentTarget.checked },
                )
              }
              data-testid="operation-route-off"
            />
          </Group>

          <Group grow align="flex-start">
            <NativeSelect
              label="Активный статус"
              description="Какой статус реально отдавать"
              data-testid="operation-active-status"
              value={fields.activeStatus?.toString() ?? ""}
              onChange={(e) =>
                setFields((prev) =>
                  prev === null
                    ? prev
                    : {
                        ...prev,
                        activeStatus:
                          e.currentTarget.value === "" ? undefined : Number(e.currentTarget.value),
                      },
                )
              }
            >
              <option value="">не задан — выбирает спека</option>
              {numericStatuses.map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </NativeSelect>
            <NumberInput
              label="Задержка ответа, мс"
              min={0}
              data-testid="operation-delay-ms"
              value={fields.delayMs ?? 0}
              onChange={(v) =>
                setFields((prev) =>
                  prev === null
                    ? prev
                    : { ...prev, delayMs: typeof v === "number" ? v : Number(v) },
                )
              }
            />
          </Group>

          <Group grow align="flex-start">
            <NumberInput
              label="Размер списков, минимум"
              min={0}
              data-testid="operation-list-size-min"
              value={fields.listSize?.min ?? ""}
              onChange={(v) =>
                setFields((prev) => {
                  if (prev === null) return prev;
                  const min = typeof v === "number" ? v : Number(v) || 0;
                  return { ...prev, listSize: { min, max: prev.listSize?.max ?? min } };
                })
              }
            />
            <NumberInput
              label="Размер списков, максимум"
              min={0}
              data-testid="operation-list-size-max"
              value={fields.listSize?.max ?? ""}
              onChange={(v) =>
                setFields((prev) => {
                  if (prev === null) return prev;
                  const max = typeof v === "number" ? v : Number(v) || 0;
                  return { ...prev, listSize: { min: prev.listSize?.min ?? max, max } };
                })
              }
            />
          </Group>

          <Divider label="Ответы по статусам" labelPosition="left" />

          {allSelectors.length === 0 ? (
            <Text size="sm" c="dimmed">
              У операции нет ни одного объявленного статуса
            </Text>
          ) : (
            <Tabs value={currentTab} onChange={setActiveTab}>
              <Tabs.List>
                {allSelectors.map((selector) => (
                  <Tabs.Tab
                    key={selector}
                    value={selector}
                    data-testid={`operation-status-tab-${selector}`}
                  >
                    {selector}
                  </Tabs.Tab>
                ))}
              </Tabs.List>
              {allSelectors.map((selector) => (
                <Tabs.Panel key={selector} value={selector} pt="sm">
                  <StatusPanel
                    selector={selector}
                    variant={fields.responses[selector]}
                    updateVariant={(updater) => updateVariant(selector, updater)}
                    onBodyErrorChange={(hasError) =>
                      setBodyErrors((prev) =>
                        prev[selector] === hasError ? prev : { ...prev, [selector]: hasError },
                      )
                    }
                  />
                </Tabs.Panel>
              ))}
            </Tabs>
          )}

          <Group>
            <Button
              leftSection={<IconDeviceFloppy size={16} />}
              loading={putOverride.isPending}
              disabled={hasBodyError}
              onClick={handleSave}
              data-testid="operation-save"
            >
              Сохранить
            </Button>
            <Button
              variant="default"
              color="red"
              leftSection={<IconRestore size={16} />}
              loading={deleteOverride.isPending}
              onClick={handleReset}
              data-testid="operation-reset"
            >
              Сбросить к спеке
            </Button>
          </Group>

          <Divider label="Предпросмотр" labelPosition="left" />
          <Text size="xs" c="dimmed">
            Показывает, что мок-плоскость отдала бы для этого черновика, если его сохранить. Ничего
            не сохраняет и не бампает ревизию.
          </Text>
          <Group grow align="flex-end" wrap="wrap">
            {previewParamNames.map((name) => (
              <TextInput
                key={name}
                label={`Параметр пути: ${name}`}
                required
                value={previewPathParams[name] ?? ""}
                onChange={(e) =>
                  setPreviewPathParams((prev) => ({ ...prev, [name]: e.currentTarget.value }))
                }
                data-testid={`operation-preview-path-param-${name}`}
              />
            ))}
            <TextInput
              label="Query-строка"
              placeholder="page=2&archived=true"
              value={previewQuery}
              onChange={(e) => setPreviewQuery(e.currentTarget.value)}
              data-testid="operation-preview-query"
            />
            <Button
              variant="light"
              loading={preview.isPending}
              onClick={handlePreview}
              data-testid="operation-preview-run"
            >
              Показать пример
            </Button>
          </Group>

          {preview.isError ? (
            <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
              {describeApiFailureDetailed(preview.error)}
            </Alert>
          ) : preview.isSuccess && preview.data.status === 200 ? (
            <PreviewPanel result={preview.data.data} />
          ) : null}
        </Stack>
      )}
    </div>
  );
}

// PreviewPanel renders D5's result document, field for field. It is a
// WINDOW, not a workbench (§H): every value here is read-only, nothing it
// shows can be edited from this panel, and `shadowedBy` is rendered
// whenever it is non-null — a preview that silently supplied a scenario's
// row instead of the draft just edited is exactly the confusion this field
// exists to prevent, so it cannot be the one field this panel drops.
function PreviewPanel({ result }: { result: PreviewResultView }): ReactElement {
  const statusSourceLabel: Record<PreviewResultView["statusSource"], string> = {
    requested: "запрошен явно",
    when: "выбран условием when[]",
    active: "активный статус операции",
    default: "статус по умолчанию из спеки",
  };

  return (
    <Stack gap="xs" data-testid="operation-preview-result">
      <Group gap="xs">
        <Badge size="lg" variant="filled" color={result.status < 400 ? "green" : "orange"}>
          {result.status}
        </Badge>
        <Text size="xs" c="dimmed">
          {statusSourceLabel[result.statusSource]}
        </Text>
      </Group>

      {result.shadowedBy !== null ? (
        <Alert
          color="yellow"
          icon={<IconAlertTriangle size={18} />}
          data-testid="operation-preview-shadowed-by"
        >
          Строка взята из активного сценария «{result.shadowedBy}», а не из этого черновика — пока
          сценарий активен, показанное здесь — не то, что задаёт эта форма.
        </Alert>
      ) : null}

      {result.routeOff ? (
        <Text size="sm" c="dimmed" data-testid="operation-preview-route-off">
          Операция выключена (`routeOff`) — мок ответил бы отказом маршрута, а не телом.
        </Text>
      ) : result.refused !== null ? (
        <Alert
          color="red"
          icon={<IconAlertTriangle size={18} />}
          data-testid="operation-preview-refused"
        >
          <Text size="sm">{describePreviewRefusalReason(result.refused.reason)}</Text>
          <Text size="xs" c="dimmed">
            {result.refused.detail}
          </Text>
        </Alert>
      ) : result.noBody ? (
        <Text size="sm" c="dimmed" data-testid="operation-preview-no-body">
          Без тела (204/205, Degraded или у операции вообще нет объявленного варианта ответа).
        </Text>
      ) : (
        <>
          <Text size="xs" c="dimmed">
            {result.mediaType} · {result.encoding}
          </Text>
          <Code block data-testid="operation-preview-body">
            {result.body}
          </Code>
        </>
      )}

      <Group gap="md">
        <Text size="xs" c="dimmed">
          schemaPatch: {result.schemaPatchApplied ? "применён" : "нет"}
        </Text>
        <Text size="xs" c="dimmed">
          рецептов: {result.recipesBound}
        </Text>
        <Text size="xs" c="dimmed">
          задержка: {result.delayMs} мс
        </Text>
      </Group>
    </Stack>
  );
}

function StatusPanel({
  selector,
  variant,
  updateVariant,
  onBodyErrorChange,
}: {
  selector: string;
  variant: Variant | undefined;
  updateVariant: (updater: (v: Variant) => Variant) => void;
  onBodyErrorChange: (hasError: boolean) => void;
}): ReactElement {
  // The select shows THREE producers, the wire has two modes plus a
  // function (A18: legal on a spec-operation override too, "both writers"):
  // a variant carrying `function` reads as «функция» whatever its mode
  // says. A20 gave the custom-endpoint forms this box and left this one
  // showing a function-bearing 200 as «сгенерированный» with «Тело
  // строится по схеме спеки» — false; the 2026-09-05 UI review found it.
  // `function` present — even as "" right after the select, before a
  // keystroke — IS the function producer: a stored row never carries "" (Go
  // omits it), so the empty string only ever means "chosen, not typed yet",
  // and functionEmpty below keeps that from being saved.
  const mode = variant?.function !== undefined ? "function" : (variant?.mode ?? "generated");
  const functionText = variant?.function ?? "";
  const functionEmpty = mode === "function" && functionText.trim() === "";
  const when = variant?.when ?? [];
  const recipes = variant?.recipes ?? {};
  const recipeEntries = Object.entries(recipes);

  // The body textarea keeps its own draft text so a JSON parse error mid-edit
  // never overwrites the last VALID body sitting in `fields` — only a
  // successful parse ever calls updateVariant.
  const [bodyDraft, setBodyDraft] = useState<string | null>(null);
  const [bodyError, setBodyError] = useState<string | null>(null);
  const serverBodyText = JSON.stringify(variant?.body ?? {}, null, 2);
  const bodyText = bodyDraft ?? serverBodyText;

  // lastCommittedBodyRef holds the serverBodyText we expect to see NEXT, as a
  // result of our OWN most recent successful edit — initialised from the
  // variant's body at mount, so the first render never looks like an
  // external change. Whenever serverBodyText disagrees with it, the variant's
  // body changed from OUTSIDE this panel (a save/GET round trip that altered
  // formatting is not the case that matters here — it is "Сбросить к спеке",
  // which sets body back to undefined without ever unmounting this panel,
  // since Tabs.Panel keeps every status keyed by the same `selector` for the
  // life of the tab list). The stale draft from before the reset would
  // otherwise sit in the textarea forever, and a save from that state would
  // submit no body at all while the screen shows the old one.
  const lastCommittedBodyRef = useRef<string>(serverBodyText);
  useEffect(() => {
    if (serverBodyText !== lastCommittedBodyRef.current) {
      lastCommittedBodyRef.current = serverBodyText;
      setBodyDraft(null);
      setBodyError(null);
    }
  }, [serverBodyText]);

  // Report this panel's body-validity up to the parent, which owns the
  // «Сохранить» button — see the comment on `bodyErrors` in OperationEditor.
  // Read the callback through a ref rather than depending on it directly:
  // the parent passes a fresh closure every render, and depending on it
  // would re-fire (and re-clean-up) this effect on every keystroke elsewhere
  // in the document, not just when this panel's own error state changes.
  const onBodyErrorChangeRef = useRef(onBodyErrorChange);
  // Refreshed in an effect rather than during render: a render can be thrown
  // away and re-run, so assigning there mutates state React has not committed
  // to. No dependency array, so it runs after EVERY render; declared before
  // the two effects below that read the ref, and effects fire in declaration
  // order, so those always see the callback from the render that queued them.
  useEffect(() => {
    onBodyErrorChangeRef.current = onBodyErrorChange;
  });
  useEffect(() => {
    // An empty Lua box counts as a body error: the wire reads "" as no
    // function, and a save would silently land a generated variant.
    onBodyErrorChangeRef.current(bodyError !== null || functionEmpty);
  }, [bodyError, functionEmpty]);
  useEffect(() => {
    return () => onBodyErrorChangeRef.current(false);
  }, []);

  function handleModeChange(next: string): void {
    if (next === "function") {
      // A18 D5: one producer per variant — body, encoding, file, media
      // type, recipes and schemaPatch all go; when[] and headers stay (a
      // function keeps its selection and its headers). The Lua box starts
      // empty rather than seeded with anything, and the empty box blocks
      // «Сохранить» (functionEmpty above) until something is typed. Mode is
      // the neutral one, as A18's own rows leave it.
      updateVariant((v) => ({
        ...v,
        mode: "generated",
        body: undefined,
        bodyEncoding: undefined,
        bodyRef: undefined,
        mediaType: undefined,
        recipes: undefined,
        schemaPatch: undefined,
        function: v.function ?? "",
      }));
      return;
    }
    updateVariant((v) => ({
      ...v,
      mode: next === "pinned" ? "pinned" : "generated",
      function: undefined,
    }));
  }

  function handleFunctionChange(text: string): void {
    updateVariant((v) => ({ ...v, function: text }));
  }

  // A6: a bodyRef variant serves the uploaded file verbatim; the body box
  // must not render for it — its `{}` fallback was the first keystroke's
  // `body` beside `bodyRef`, a 400 naming a field the form never showed.
  function clearBodyRef(): void {
    updateVariant((v) => ({ ...v, bodyRef: undefined }));
  }

  function handleBodyChange(text: string): void {
    setBodyDraft(text);
    try {
      const parsed: unknown = JSON.parse(text);
      setBodyError(null);
      // What serverBodyText will read as once this commit round-trips back
      // down through `fields` — matching it here (rather than leaving the
      // ref at its old value) is what lets the reconciliation effect above
      // tell "this is my own edit landing" apart from a genuine external
      // change, so it does not stomp the operator's own raw text/formatting.
      lastCommittedBodyRef.current = JSON.stringify(parsed, null, 2);
      updateVariant((v) => ({ ...v, body: parsed }));
    } catch (err) {
      setBodyError(`JSON невалиден (${jsonLocation(text, err)})`);
    }
  }

  function handleMediaTypeChange(mediaType: string): void {
    updateVariant((v) => ({ ...v, mediaType }));
  }

  function addCondition(): void {
    updateVariant((v) => ({ ...v, when: [...(v.when ?? []), DEFAULT_CONDITION] }));
  }

  function patchCondition(index: number, patch: Partial<Condition>): void {
    updateVariant((v) => ({
      ...v,
      when: (v.when ?? []).map((c, i) => (i === index ? { ...c, ...patch } : c)),
    }));
  }

  function removeCondition(index: number): void {
    updateVariant((v) => ({ ...v, when: (v.when ?? []).filter((_, i) => i !== index) }));
  }

  return (
    <Stack gap="sm">
      <NativeSelect
        label="Режим"
        data-testid={`operation-status-mode-${selector}`}
        value={mode}
        onChange={(e) => handleModeChange(e.currentTarget.value)}
      >
        <option value="generated">сгенерированный</option>
        <option value="pinned">закреплённый</option>
        <option value="function">функция (Lua)</option>
      </NativeSelect>

      {mode === "function" ? (
        <Textarea
          label="Функция (Lua) — над аргументом req, возвращает status, body, headers"
          description="Раздел «Функции» в руководстве. Компилируется при сохранении: синтаксическая ошибка — отказ со словами парсера. Заменяет тело, файл и подстановки этого статуса; условия ниже остаются."
          rows={6}
          styles={{ input: { fontFamily: "var(--mantine-font-family-monospace)" } }}
          data-testid={`operation-status-function-${selector}`}
          error={functionEmpty ? "Функция пуста" : undefined}
          value={functionText}
          onChange={(e) => handleFunctionChange(e.currentTarget.value)}
        />
      ) : mode === "pinned" && variant?.bodyRef !== undefined ? (
        <Group gap="xs" data-testid={`operation-status-body-ref-${selector}`}>
          <Text size="sm">
            Ответ — файл «{variant.bodyRef.replace(/^asset:/, "")}» (вкладка «Файлы»), со своим
            media type.
          </Text>
          <Button variant="default" size="xs" onClick={clearBodyRef}>
            Убрать файл и задать тело
          </Button>
        </Group>
      ) : mode === "pinned" ? (
        <>
          <TextInput
            label="Media type"
            placeholder="application/json"
            data-testid={`operation-status-media-type-${selector}`}
            value={variant?.mediaType ?? ""}
            onChange={(e) => handleMediaTypeChange(e.currentTarget.value)}
          />
          <Textarea
            label="Тело ответа, JSON"
            rows={6}
            data-testid={`operation-status-body-${selector}`}
            error={bodyError}
            value={bodyText}
            onChange={(e) => handleBodyChange(e.currentTarget.value)}
          />
        </>
      ) : (
        <Text size="sm" c="dimmed">
          Тело строится по схеме спеки — переключите на «закреплённый», чтобы задать его вручную
        </Text>
      )}

      {recipeEntries.length > 0 ? (
        <Card withBorder p="xs" data-testid={`operation-status-recipes-${selector}`}>
          <Text size="xs" fw={600} c="dimmed">
            Автоматические значения на этом статусе ({recipeEntries.length}) — редактирование
            появится позже, здесь только показ, чтобы было видно, откуда взялось тело
          </Text>
          <Group gap={4} mt={4}>
            {recipeEntries.map(([path, recipe]) => (
              <Badge key={path} size="sm" variant="light">
                {path}: {recipe.kind}
              </Badge>
            ))}
          </Group>
        </Card>
      ) : null}

      <Divider label="Когда отвечать так" labelPosition="left" />
      <Text size="xs" c="dimmed">
        Все условия ниже должны совпасть, иначе используется обычная генерация
      </Text>
      <Stack gap="xs" data-testid={`operation-status-when-${selector}`}>
        {when.map((cond, index) => (
          // Index-keyed on purpose: conditions carry no id of their own and
          // this list is edited in place, never reordered.
          // eslint-disable-next-line react/no-array-index-key
          <Group key={index} gap="xs" wrap="nowrap" align="flex-end">
            <NativeSelect
              label="Где"
              data-testid={`operation-when-in-${selector}-${index}`}
              value={cond.in}
              onChange={(e) =>
                patchCondition(index, { in: e.currentTarget.value as Condition["in"] })
              }
            >
              {IN_OPTIONS.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </NativeSelect>
            <TextInput
              label="Имя"
              data-testid={`operation-when-name-${selector}-${index}`}
              value={cond.name}
              onChange={(e) => patchCondition(index, { name: e.currentTarget.value })}
            />
            <NativeSelect
              label="Условие"
              data-testid={`operation-when-op-${selector}-${index}`}
              value={cond.op}
              onChange={(e) =>
                patchCondition(index, { op: e.currentTarget.value as Condition["op"] })
              }
            >
              {OP_OPTIONS.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </NativeSelect>
            <TextInput
              label="Значение"
              disabled={cond.op === "exists"}
              data-testid={`operation-when-value-${selector}-${index}`}
              value={cond.value ?? ""}
              onChange={(e) => patchCondition(index, { value: e.currentTarget.value })}
            />
            <ActionIcon
              variant="default"
              color="red"
              onClick={() => removeCondition(index)}
              data-testid={`operation-when-remove-${selector}-${index}`}
              aria-label="Удалить условие"
            >
              <IconTrash size={16} />
            </ActionIcon>
          </Group>
        ))}
        <Button
          variant="default"
          size="xs"
          w="fit-content"
          leftSection={<IconPlus size={14} />}
          onClick={addCondition}
          data-testid={`operation-when-add-${selector}`}
        >
          Добавить условие
        </Button>
      </Stack>
    </Stack>
  );
}
