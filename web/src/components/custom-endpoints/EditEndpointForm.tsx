import { useState } from "react";
import type { ReactElement } from "react";
import { Alert, Button, Group, Stack, Switch, Text, TextInput } from "@mantine/core";
import { IconAlertTriangle } from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import { useForm } from "react-hook-form";
import { type } from "arktype";
import { invalidateEndpointChange } from "@/api/cachePolicy";
import { useUpdateEndpoint } from "@/api/generated/endpoints/endpoints.ts";
import type {
  EditConflictTombstone,
  EndpointConflictDetails,
  EndpointView,
  UpdateEndpointRequestResponses,
  Variant,
} from "@/api/generated/schemas";
import { VariantEditor } from "../VariantEditor";
import { conflictOf, describeApiFailureDetailed, isGoneTombstone } from "@/api/errors";
import { arktypeResolver } from "@/validation/resolver";
import { EndpointFormFields } from "./EndpointFormFields";
import { pathTemplate, statusCodeField } from "./shared";

const activeStatusField = statusCodeField(true);

// A21 step 5: the variant itself — body, media type, file, function,
// headers, conditions — is VariantEditor.tsx's, held in local state beside
// this form; the form owns only the row's own fields.
const editForm = type({
  method: "string",
  path: pathTemplate,
  status: activeStatusField,
  // A21 (G9): the two switches the operation editor always had — the row
  // showed «маршрут выключен» as a badge with no way to flip it, so
  // disabling a custom endpoint meant deleting it.
  overrideOn: "boolean",
  routeOff: "boolean",
});
type EditForm = typeof editForm.infer;

// Pick, not the full EndpointView: this also seeds the form from a 409's
// EndpointConflictDetails (A3/D6), which shares exactly these four fields
// with EndpointView and none of the server-owned ones (id, canonicalPath,
// createdAt, updatedAt) this form never renders.
function defaultsFromEndpoint(
  ep: Pick<
    EndpointView,
    "method" | "path" | "activeStatus" | "responses" | "overrideOn" | "routeOff"
  >,
): EditForm {
  return {
    method: ep.method,
    path: ep.path,
    status: String(ep.activeStatus),
    overrideOn: ep.overrideOn,
    routeOff: ep.routeOff,
  };
}

// EditEndpointForm is PUT's caller: useUpdateEndpoint (the four call shapes
// orval emits per operation — coverage.test.ts accepts any followed by "(";
// this is the mutation-hook shape, same one useCreateEndpoint/useDeleteEndpoint
// already use above). PUT is a full replacement (UpdateEndpointRequest has no
// optional method/path/activeStatus/responses), so the fields this form does
// NOT expose — overrideOn, routeOff, listSize, delayMs — are sent back
// unchanged from `endpoint`, and only the activeStatus variant's body and
// mediaType are ever rewritten by hand here. Keeping the affordance to that
// one variant (rather than a responses-map editor) is deliberate: the form
// this reuses (method/path/status/mediaType/body) is exactly CreateEndpointForm's
// shape, so a person who already knows how to CREATE a custom endpoint knows
// how to edit one. That shared shape is EndpointFormFields; everything below
// it is this form's own, and the comments say why.
export function EditEndpointForm({
  id,
  endpoint,
  onDone,
  onCancel,
}: {
  id: number;
  endpoint: EndpointView;
  onDone: () => void;
  onCancel: () => void;
}): ReactElement {
  const queryClient = useQueryClient();
  const {
    register,
    handleSubmit,
    reset,
    watch,
    formState: { errors },
  } = useForm<EditForm>({
    resolver: arktypeResolver(editForm),
    defaultValues: defaultsFromEndpoint(endpoint),
  });
  // A3: the fields a full-replacement PUT resends unchanged (overrideOn,
  // routeOff, listSize, delayMs, the rest of `responses`) and the
  // editVersion expectation itself both start from THIS row's own preceding
  // read (`endpoint`, from EndpointList's useListEndpoints) — never
  // re-fetched at submit time. `conflictBase` overrides both only in the one
  // window after a 409, before `endpoint` itself catches up through the
  // list's own refetch: D6's `details` already carries a fresher endpoint
  // than the stale prop, so this adopts it directly instead of a second GET.
  const [conflictBase, setConflictBase] = useState<EndpointConflictDetails | null>(null);
  // FROZEN at mount, not the live prop: the list refetches under an open
  // form, and a base that followed it would pair drafts begun on the old
  // document with the new editVersion — a full-replacement PUT that
  // overwrites another writer's change without a 409 (a reader of A21).
  // The only way a fresher document enters is the conflict reload above.
  const [mountedEndpoint] = useState(endpoint);
  const base = conflictBase ?? mountedEndpoint;

  // One draft PER STATUS, keyed by the status text: the editor always edits
  // the variant of the status currently typed in «Активный статус» — a draft
  // begun on 200 must not land under 201 when the operator retypes the
  // status (the second reader of A21 caught exactly that). A status with no
  // draft yet starts from the stored variant, or from a fresh generated one;
  // every updater spreads first, so recipes, schemaPatch, schema and
  // anything else the editor does not show survive.
  const [drafts, setDrafts] = useState<Record<string, Variant>>({});
  const [variantError, setVariantError] = useState(false);
  const statusText = watch("status").trim();
  const variant: Variant = drafts[statusText] ??
    base.responses[statusText] ?? { mode: "generated" };
  function updateVariant(updater: (v: Variant) => Variant): void {
    setDrafts((prev) => ({
      ...prev,
      [statusText]: updater(
        prev[statusText] ?? base.responses[statusText] ?? { mode: "generated" },
      ),
    }));
  }

  const updateEndpoint = useUpdateEndpoint({
    mutation: {
      onSuccess: (res) => {
        if (res.status !== 200) {
          return;
        }
        // §3.9, same as create/delete above: a PUT can move the endpoint's
        // (method, path) or its revision-bearing settings, so both queries
        // invalidate, not just the endpoints list.
        invalidateEndpointChange(queryClient, id);
        onDone();
      },
    },
  });

  function onSubmit(values: EditForm): void {
    const status = values.status.trim();
    if (variantError) {
      return;
    }
    // A full-replacement PUT still only touches the ONE variant this form
    // edits — every other status this endpoint already serves is resent
    // byte-for-byte from `base` (endpoint, or the conflict's own current
    // document once one has landed). The edited variant is the stored one
    // MUTATED through the editor's spreading updaters, never a fresh
    // literal — the anti-pattern from_traffic.go's pinObservedBody names by
    // comment. A "chosen, not typed" state cannot reach here: the editor
    // reports it through variantError and the button is disabled.
    const responses: UpdateEndpointRequestResponses = { ...base.responses };
    responses[status] = variant;
    updateEndpoint.mutate({
      id,
      eid: endpoint.id,
      data: {
        method: values.method,
        path: values.path.trim(),
        activeStatus: Number(status),
        responses,
        overrideOn: values.overrideOn,
        routeOff: values.routeOff,
        listSize: base.listSize,
        delayMs: base.delayMs,
        // P7a: a PUT is a full replacement and the form has no field for
        // these two yet (P7b's) — pass the row's own back untouched, or an
        // edit from this screen would silently clear them.
        reqSchema: base.reqSchema,
        operation: base.operation,
        editVersion: base.editVersion,
      },
    });
  }

  // handleConflictReload is D10's per-screen affordance: PUT .../endpoints's
  // 0-means-no-row rule does NOT apply here (UpdateEndpointRequest.editVersion's
  // own description — a row addressed by {eid} always already exists), so a
  // gone-tombstone here means the endpoint itself was deleted from under the
  // operator mid-edit and there is nothing left to resend. That case only
  // refreshes the list/workspace queries and closes this row's form —
  // EndpointList's own empty-row disappearance is the honest answer, not a
  // pretend document.
  function handleConflictReload(details: EndpointConflictDetails | EditConflictTombstone): void {
    if (isGoneTombstone(details)) {
      invalidateEndpointChange(queryClient, id);
      onCancel();
      return;
    }
    setConflictBase(details);
    reset(defaultsFromEndpoint(details));
    // The conflict's document is the new base; drafts begun on the stale one
    // are dropped rather than replayed over rows that may have changed.
    setDrafts({});
  }

  return (
    <Stack
      component="form"
      gap="sm"
      w="100%"
      data-testid="endpoint-edit-form"
      onSubmit={handleSubmit(onSubmit)}
    >
      {(() => {
        const conflict = conflictOf(updateEndpoint);
        if (conflict !== null) {
          return (
            <Alert
              color="orange"
              icon={<IconAlertTriangle size={18} />}
              role="alert"
              data-testid="endpoint-edit-conflict"
            >
              <Text size="sm">{describeApiFailureDetailed(conflict)}</Text>
              <Button
                variant="light"
                size="xs"
                mt="xs"
                onClick={() =>
                  handleConflictReload(
                    conflict.details as EndpointConflictDetails | EditConflictTombstone,
                  )
                }
                data-testid="endpoint-conflict-reload"
              >
                Загрузить актуальную версию
              </Button>
            </Alert>
          );
        }
        return updateEndpoint.isError ? (
          <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
            {describeApiFailureDetailed(updateEndpoint.error)}
          </Alert>
        ) : null;
      })()}
      <EndpointFormFields
        testIdPrefix="endpoint-edit"
        methodLabel="Метод"
        methodField={register("method")}
        pathLabel="Путь"
        pathError={errors.path?.message}
        pathField={register("path")}
      />
      <TextInput
        label="Активный статус"
        description={
          statusText !== "" && statusText !== String(base.activeStatus)
            ? base.responses[statusText] !== undefined
              ? `Ниже — ответ статуса ${statusText}, как он сохранён; ${base.activeStatus} останется как есть`
              : `Статуса ${statusText} у endpoint'а ещё нет: ниже — его новый ответ; ${base.activeStatus} останется как есть`
            : undefined
        }
        data-testid="endpoint-edit-status"
        error={errors.status?.message}
        {...register("status")}
      />
      <Group gap="md">
        <Switch
          label="Перекрывает операцию спеки с таким же путём"
          data-testid="endpoint-edit-override-on"
          {...register("overrideOn")}
        />
        <Switch
          label="Маршрут выключен — мок перестаёт на него отвечать"
          color="red"
          data-testid="endpoint-edit-route-off"
          {...register("routeOff")}
        />
      </Group>
      <VariantEditor
        key={`${statusText}:${base.editVersion}`}
        workspaceId={id}
        variant={variant}
        updateVariant={updateVariant}
        onErrorChange={setVariantError}
        testId={(name) => `endpoint-edit-${name}`}
        whenTestId={(name, index) => `endpoint-edit-when-${name}-${index}`}
        hasSchema={variant.schema !== undefined}
        // A custom endpoint serves its stored headers under a generated and
        // a pinned variant (custom.go); a function's come from the Lua return.
        headersAppliedOn={["generated", "pinned", "file"]}
      />
      <Group gap="xs">
        <Button
          type="submit"
          size="xs"
          loading={updateEndpoint.isPending}
          disabled={variantError}
          data-testid="endpoint-edit-submit"
        >
          {updateEndpoint.isPending ? "Сохраняем…" : "Сохранить"}
        </Button>
        <Button
          type="button"
          variant="default"
          size="xs"
          onClick={onCancel}
          data-testid="endpoint-edit-cancel"
        >
          Отмена
        </Button>
      </Group>
    </Stack>
  );
}
