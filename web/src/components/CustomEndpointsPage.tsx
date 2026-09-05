import { useEffect, useState } from "react";
import type { ReactElement } from "react";
import {
  Alert,
  Anchor,
  Badge,
  Button,
  Card,
  Group,
  Loader,
  NativeSelect,
  Stack,
  Switch,
  Text,
  Textarea,
  TextInput,
  Title,
} from "@mantine/core";
import { modals } from "@mantine/modals";
import {
  IconAlertTriangle,
  IconPencil,
  IconPlugConnected,
  IconPlus,
  IconTrash,
} from "@tabler/icons-react";
import { useNavigate } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { useForm } from "react-hook-form";
import { type } from "arktype";
import dayjs from "dayjs";
import {
  getListEndpointsQueryKey,
  useCreateEndpoint,
  useDeleteEndpoint,
  useListEndpoints,
  useUpdateEndpoint,
} from "@/api/generated/endpoints/endpoints.ts";
import { getGetWorkspaceQueryKey, useGetWorkspace } from "@/api/generated/workspaces/workspaces.ts";
import type {
  EditConflictTombstone,
  EndpointConflictDetails,
  EndpointView,
  ServerConfigView,
  ServerConfigViewLimits,
  StreamDefinition,
  UpdateEndpointRequestResponses,
} from "@/api/generated/schemas";
import {
  StreamCapsStrip,
  StreamEditor,
  draftFromDefinition,
  draftToDefinition,
  emptyStreamDraft,
  type StreamDraft,
  type StreamKind,
} from "./StreamEditor";
import { StreamTestClient } from "./StreamTestClient";
import { TabLink } from "./TabLink";
import { ApiFailure } from "@/api/client";
import { describeApiFailure, describeApiFailureDetailed, isGoneTombstone } from "@/api/errors";
import { arktypeResolver } from "@/validation/resolver";

// CustomEndpointsPage is DESIGN §14 screen 6, P1 subset: a custom endpoint is
// a route this workspace serves that no spec declares (contrast with
// "переопределить операцию" — a custom route that canonically matches a spec
// route, which is configured on the Endpoint'ы screen instead, per DESIGN's
// own wording for screen 6). DESIGN's own preferred way to make one is
// "создать endpoint из запроса" on the traffic screen — a person who has
// never opened OpenAPI just points at a real request. The form here is the
// manual fallback for everyone else.
//
// The outermost element carries data-testid="custom-endpoints-page" OUTSIDE
// every state switch below, matching the marker contract every screen in
// this phase follows: web/src/routes/routes.test.tsx proves the route is
// reachable by finding this marker alone, and it must not depend on how the
// endpoints list itself answers.

const HTTP_METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"] as const;

// P6e (DESIGN §30.14): the type selector. A stream is a custom endpoint and
// nothing else (§30.2), so it is created on this very form — the selector
// swaps the body editor for StreamEditor's four behaviours and pins the
// method to GET, which is the only one a stream handshake can use.
const KIND_OPTIONS: { value: "http" | StreamKind; label: string }[] = [
  { value: "http", label: "Обычный ответ (HTTP)" },
  { value: "sse", label: "Поток событий (SSE)" },
  { value: "ws", label: "WebSocket" },
];

function kindLabel(kind: EndpointView["kind"]): string | null {
  return kind === "sse" ? "SSE" : kind === "ws" ? "WebSocket" : null;
}

/** eventNamesOf lists the named events a definition sends, for the browser
 * client's listeners — EventSource fires `message` only for unnamed frames. */
function eventNamesOf(def: StreamDefinition | undefined): string[] {
  if (!def) {
    return [];
  }
  const names = (def.timeline?.frames ?? []).map((f) => f.event ?? "");
  if (def.tick?.event) {
    names.push(def.tick.event);
  }
  return names.filter((n) => n !== "");
}

const pathTemplate = type("string").narrow((value, ctx) => {
  const trimmed = value.trim();
  if (trimmed === "") {
    return ctx.reject({ problem: "Укажите путь" });
  }
  if (!trimmed.startsWith("/")) {
    return ctx.reject({ problem: "Путь должен начинаться с /" });
  }
  return true;
});

// statusField stays a free-text string rather than a number input: the
// field is OPTIONAL (the server defaults to 200 when it is omitted, per
// api/openapi.json's own description on CreateEndpointRequest.status), and a
// Mantine NumberInput has no clean way to express "empty" that survives a
// round trip through react-hook-form's register() the way an empty string
// does.
const statusField = type("string").narrow((value, ctx) => {
  const trimmed = value.trim();
  if (trimmed === "") {
    return true;
  }
  const n = Number(trimmed);
  if (!Number.isInteger(n) || n < 100 || n > 599) {
    return ctx.reject({ problem: "Код статуса — целое число от 100 до 599" });
  }
  return true;
});

// jsonLocation turns JSON.parse's own SyntaxError ("Unexpected token j in
// JSON at position 5") into "строка N, столбец M": the phase brief asks that
// a malformed body be validated in the browser AND show WHERE it is broken,
// and a byte offset into a multi-line textarea is not something a person can
// use without counting characters by hand.
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

// bodyField stays optional on purpose: CreateEndpointRequest.body is itself
// optional, and an omitted body means an empty pinned body — the contract's
// own description calls that "almost never what anyone wants". This form
// does not FORBID submitting an empty body (that would reject a request the
// server accepts), it warns, via the Textarea's description below.
const bodyField = type("string").narrow((value, ctx) => {
  if (value.trim() === "") {
    return true;
  }
  try {
    JSON.parse(value);
    return true;
  } catch (err) {
    return ctx.reject({ problem: `JSON невалиден (${jsonLocation(value, err)})` });
  }
});

const createForm = type({
  kind: "string",
  method: "string",
  path: pathTemplate,
  status: statusField,
  bodyText: bodyField,
  mediaType: "string",
  functionText: "string",
});
type CreateForm = typeof createForm.infer;

const EMPTY_FORM: CreateForm = {
  kind: "http",
  method: "GET",
  path: "",
  status: "",
  bodyText: "",
  mediaType: "",
  functionText: "",
};

// producerConflict is A18 D5's "one producer per variant" said before a
// round trip: a variant either runs a function or serves a body, and a
// function chooses its own media type. The server refuses the pair by name
// (400 function_and_body); this form says the same thing next to the fields
// so the operator does not read it off an alert. Null when the shape is
// legal. Exported for the test.
export function producerConflict(fields: {
  functionText: string;
  bodyText: string;
  mediaType: string;
}): string | null {
  if (fields.functionText.trim() === "") {
    return null;
  }
  if (fields.bodyText.trim() !== "") {
    return "Функция и тело ответа взаимоисключающие: у варианта один источник ответа — очистите одно из полей";
  }
  if (fields.mediaType.trim() !== "") {
    return "Функция сама выбирает media type (таблица — JSON, строка — как есть): очистите поле Media type";
  }
  return null;
}

// activeStatusField differs from statusField (used by create) in exactly one
// way: UpdateEndpointRequest.activeStatus is REQUIRED on the wire (it is a
// full-replacement PUT, not create's status-defaults-to-200 POST), so an
// empty field is rejected here instead of treated as "omit it".
const activeStatusField = type("string").narrow((value, ctx) => {
  const trimmed = value.trim();
  if (trimmed === "") {
    return ctx.reject({ problem: "Укажите код статуса" });
  }
  const n = Number(trimmed);
  if (!Number.isInteger(n) || n < 100 || n > 599) {
    return ctx.reject({ problem: "Код статуса — целое число от 100 до 599" });
  }
  return true;
});

const editForm = type({
  method: "string",
  path: pathTemplate,
  status: activeStatusField,
  bodyText: bodyField,
  mediaType: "string",
  functionText: "string",
  // A21 (G9): the two switches the operation editor always had — the row
  // showed «маршрут выключен» as a badge with no way to flip it, so
  // disabling a custom endpoint meant deleting it.
  overrideOn: "boolean",
  routeOff: "boolean",
});
type EditForm = typeof editForm.infer;

// The variant currently served at an endpoint's activeStatus, or undefined
// when the map has no entry there — a legitimate shape (a custom endpoint
// whose activeStatus was aimed at a variant that was never filled in).
function activeVariant(
  ep: Pick<EndpointView, "activeStatus" | "responses">,
): EndpointView["responses"][string] | undefined {
  return ep.responses[String(ep.activeStatus)];
}

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
  const variant = activeVariant(ep);
  return {
    method: ep.method,
    path: ep.path,
    status: String(ep.activeStatus),
    overrideOn: ep.overrideOn,
    routeOff: ep.routeOff,
    // Only a "pinned" variant has a literal body to show back; "generated"
    // (the fixture default, and possible on a row this form never touched)
    // has none — pre-filling from it would fabricate JSON nobody wrote.
    bodyText:
      variant?.mode === "pinned" && variant.body !== undefined
        ? JSON.stringify(variant.body, null, 2)
        : "",
    mediaType: variant?.mediaType ?? "",
    // A18: the Lua the agent wrote is shown back, so an edit of this row
    // is never blind to it — before this field (2026-09-05) the form showed
    // an empty body for a function variant and gave no hint one existed.
    functionText: variant?.function ?? "",
  };
}

// keepsOtherProducer says whether an empty body box means "leave the stored
// producer alone" — true for a schema-backed generated variant (P7a) and a
// file-backed one (A6). A pinned literal body with an emptied box is the
// one case where empty MEANS empty (an empty pinned body, as before).
export function keepsOtherProducer(
  variant: EndpointView["responses"][string] | undefined,
  bodyText: string,
): boolean {
  if (variant === undefined || bodyText !== "") {
    return false;
  }
  if (variant.bodyRef !== undefined && variant.bodyRef !== "") {
    return true;
  }
  return variant.mode !== "pinned" && variant.schema !== undefined;
}

// producerNote is the line above the body box that says what the variant
// serves from when it is not a literal body — before A21 the form showed an
// empty box for both cases and nothing else.
export function producerNote(
  variant: EndpointView["responses"][string] | undefined,
): string | null {
  if (variant === undefined || (variant.function !== undefined && variant.function !== "")) {
    return null;
  }
  if (variant.bodyRef !== undefined && variant.bodyRef !== "") {
    return `Сейчас ответ — файл «${variant.bodyRef.replace(/^asset:/, "")}» (вкладка «Файлы»). Введите тело, чтобы заменить файл телом; пустое поле оставит файл.`;
  }
  if (variant.mode !== "pinned" && variant.schema !== undefined) {
    return "Сейчас тело строится по схеме (вкладка «Контракт»). Введите тело, чтобы закрепить его; пустое поле оставит схему.";
  }
  return null;
}

// hasFunction answers the row badge: any variant of the endpoint, not only
// the active one, runs Lua — a 500 the agent scripted is worth knowing
// about while the 200 is what serves.
function hasFunction(ep: Pick<EndpointView, "responses">): boolean {
  return Object.values(ep.responses).some((v) => v.function !== undefined && v.function !== "");
}

// createdAt/updatedAt arrive as Unix seconds (internal/admin/endpoint_handlers.go
// writes row.CreatedAt.Unix()/row.UpdatedAt.Unix()), same convention SpecsPage
// already documents for SpecView — dayjs needs telling which, or it reads
// 1970 for every row.
function formatTimestamp(unixSeconds: number): string {
  return dayjs.unix(unixSeconds).format("DD.MM.YYYY HH:mm");
}

export function CustomEndpointsPage({
  id,
  config,
  initialEditingId,
}: {
  id: number;
  /** P7b: the row id the «Контракт» tab linked here with — its edit form
   * opens on mount. */
  initialEditingId?: number;
  /** The session's server config (A9): its `limits` feed the stream caps
   * strip. Optional so a component test that mounts the screen alone gets
   * the strip's constants instead of a crash. */
  config?: ServerConfigView;
}): ReactElement {
  const endpoints = useListEndpoints(id);
  const limits = config?.limits;
  // The workspace's own public URL, for the browser test client — read from
  // the cache WorkspaceLayout already warmed; a screen that cannot read it
  // (a component test that stubs only the endpoints route) simply has no
  // «Проверить» to offer, it does not fail.
  const workspace = useGetWorkspace(id);
  const workspaceUrl = workspace.data?.status === 200 ? workspace.data.data.url : undefined;
  const navigate = useNavigate();

  return (
    <div data-testid="custom-endpoints-page">
      <Stack gap="md">
        <Title order={1}>Кастомные endpoint&apos;ы</Title>
        <Text size="sm" c="dimmed">
          Кастомный endpoint — это маршрут, которого нет в спеке. Основной способ его завести —{" "}
          <Anchor
            href={`/workspaces/${id}/traffic`}
            onClick={(e) => {
              // A real href (so middle-click / open-in-new-tab still work),
              // but a click still goes through the router's own navigate —
              // Anchor's `component={Link}` prop, tried first, defeats
              // TanStack Router's typed `params` inference through Mantine's
              // polymorphic `component` prop (verified against this exact
              // route: it collapses `params` to the reducer-function overload
              // only, rejecting the plain `{ id }` object every other screen
              // in this codebase passes to useNavigate/Route.useParams).
              e.preventDefault();
              void navigate({ to: "/workspaces/$id/traffic", params: { id } });
            }}
          >
            «создать endpoint из запроса» на экране трафика
          </Anchor>
          : там виден уже готовый ответ, и достаточно поправить нужную цифру. Форма ниже — запасной
          путь для случая, когда подходящего запроса в трафике ещё не было. Маршрут, который
          канонически совпадает со спековым, здесь заводить не нужно — это «переопределить операцию»
          на экране{" "}
          <TabLink id={id} tab="operations" testId="endpoints-operations-link">
            Endpoint&apos;ов
          </TabLink>
          . Уже созданный endpoint можно поправить прямо в списке ниже — кнопка «Изменить» открывает
          форму с текущими значениями.
        </Text>
        <CreateEndpointForm id={id} limits={limits} />
        {endpoints.isPending ? (
          // role on the Text, not the Group: the live region should be the
          // sentence a screen reader announces, not the flex box around it.
          <Group gap="xs">
            <Loader size="sm" />
            <Text size="sm" component="output">
              Загрузка…
            </Text>
          </Group>
        ) : endpoints.isError ? (
          <Stack gap="sm" data-testid="endpoints-error">
            <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
              {describeApiFailure(endpoints.error)}
            </Alert>
            <Button
              variant="default"
              w="fit-content"
              onClick={() => void endpoints.refetch()}
              data-testid="endpoints-retry"
            >
              Повторить
            </Button>
          </Stack>
        ) : endpoints.data.status !== 200 ? (
          <Alert
            color="red"
            icon={<IconAlertTriangle size={18} />}
            role="alert"
            data-testid="endpoints-error"
          >
            {describeApiFailure(null)}
          </Alert>
        ) : endpoints.data.data.endpoints.length === 0 ? (
          // An empty list is an empty state, not an error — explain what a
          // custom endpoint is FOR rather than leaving a blank card, per the
          // phase brief.
          <Text data-testid="endpoints-empty">
            Кастомных endpoint&apos;ов пока нет. Сервер сейчас отвечает только на то, что описано в
            спеке — заведите первый через форму выше или через трафик.
          </Text>
        ) : (
          <EndpointList
            id={id}
            endpoints={endpoints.data.data.endpoints}
            workspaceUrl={workspaceUrl}
            limits={limits}
            initialEditingId={initialEditingId}
          />
        )}
      </Stack>
    </div>
  );
}

function CreateEndpointForm({
  id,
  limits,
}: {
  id: number;
  limits: ServerConfigViewLimits | undefined;
}): ReactElement {
  const queryClient = useQueryClient();
  const [created, setCreated] = useState<string | null>(null);
  const {
    register,
    handleSubmit,
    reset,
    watch,
    setValue,
    formState: { errors },
  } = useForm<CreateForm>({
    resolver: arktypeResolver(createForm),
    defaultValues: EMPTY_FORM,
  });
  const kind = watch("kind") as "http" | StreamKind;
  const path = watch("path");
  const producerError = producerConflict({
    functionText: watch("functionText"),
    bodyText: watch("bodyText"),
    mediaType: watch("mediaType"),
  });
  const [streamDraft, setStreamDraft] = useState<StreamDraft>(emptyStreamDraft);
  const [streamError, setStreamError] = useState<string | null>(null);
  // A stream handshake is a GET and the server refuses anything else by
  // name; the select below is disabled for a stream, and the value is pinned
  // here so a method chosen BEFORE switching the type cannot leak through.
  useEffect(() => {
    if (kind !== "http") {
      setValue("method", "GET");
    }
  }, [kind, setValue]);

  const createEndpoint = useCreateEndpoint({
    mutation: {
      onSuccess: (res) => {
        if (res.status !== 201) {
          return;
        }
        setCreated(`${res.data.method} ${res.data.path}`);
        reset(EMPTY_FORM);
        // §3.9: useCreateEndpoint must invalidate the endpoints list AND the
        // workspace (a new endpoint can move the workspace's revision).
        void queryClient.invalidateQueries({ queryKey: getListEndpointsQueryKey(id) });
        void queryClient.invalidateQueries({ queryKey: getGetWorkspaceQueryKey(id) });
      },
    },
  });

  function onSubmit(values: CreateForm): void {
    const status = values.status.trim();
    const mediaType = values.mediaType.trim();
    const bodyText = values.bodyText.trim();
    setCreated(null);
    if (values.kind !== "http") {
      const streamKind = values.kind as StreamKind;
      const def = draftToDefinition(streamKind, streamDraft);
      if ("error" in def) {
        setStreamError(def.error);
        return;
      }
      setStreamError(null);
      createEndpoint.mutate(
        {
          id,
          data: { method: "GET", path: values.path.trim(), kind: streamKind, stream: def.stream },
        },
        { onSuccess: (res) => res.status === 201 && setStreamDraft(emptyStreamDraft()) },
      );
      return;
    }
    if (producerError !== null) {
      return;
    }
    const functionText = values.functionText.trim() === "" ? "" : values.functionText;
    createEndpoint.mutate({
      id,
      data: {
        method: values.method,
        path: values.path.trim(),
        // Omitted, not sent as a literal default: the server's own default
        // (200) is what actually happens on omission, and pre-filling it
        // here would claim credit for a choice this form did not make.
        status: status === "" ? undefined : Number(status),
        mediaType: mediaType === "" ? undefined : mediaType,
        // JSON.parse already ran once during validation (bodyField); it runs
        // again here rather than caching that result so the request always
        // reflects whatever is currently in the textarea.
        body: bodyText === "" ? undefined : (JSON.parse(bodyText) as unknown),
        // A18: sent untrimmed — Lua source is the operator's bytes, and a
        // leading newline is not the form's to remove.
        function: functionText === "" ? undefined : functionText,
      },
    });
  }

  return (
    <Card
      component="form"
      withBorder
      p="md"
      data-testid="endpoint-create-form"
      onSubmit={handleSubmit(onSubmit)}
    >
      <Stack gap="sm">
        {createEndpoint.isError ? (
          <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
            {/* describeApiFailureDetailed, not describeApiFailure: a 409 here
                names the route that already exists, and that sentence is the
                actionable content, per the phase brief. */}
            {describeApiFailureDetailed(createEndpoint.error)}
          </Alert>
        ) : null}
        {created !== null ? (
          <Text size="sm" data-testid="endpoint-created">
            Создан endpoint «<strong>{created}</strong>»
          </Text>
        ) : null}
        <Group grow align="flex-start">
          <NativeSelect label="Тип" data-testid="endpoint-create-kind" {...register("kind")}>
            {KIND_OPTIONS.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </NativeSelect>
          <NativeSelect
            label={kind === "http" ? "Метод" : "Метод (поток — всегда GET)"}
            disabled={kind !== "http"}
            data-testid="endpoint-create-method"
            {...register("method")}
          >
            {HTTP_METHODS.map((method) => (
              <option key={method} value={method}>
                {method}
              </option>
            ))}
          </NativeSelect>
          <TextInput
            label="Путь"
            placeholder={kind === "http" ? "/custom/ping" : "/events"}
            data-testid="endpoint-create-path"
            error={errors.path?.message}
            {...register("path")}
          />
        </Group>
        {kind !== "http" ? (
          <>
            {streamError ? (
              <Alert
                color="red"
                icon={<IconAlertTriangle size={18} />}
                role="alert"
                data-testid="endpoint-create-stream-error"
              >
                {streamError}
              </Alert>
            ) : null}
            <StreamEditor
              kind={kind}
              draft={streamDraft}
              onChange={setStreamDraft}
              testIdPrefix="endpoint-create"
            />
            <StreamCapsStrip
              workspaceId={id}
              path={path}
              kind={kind}
              draft={streamDraft}
              limits={limits}
              testIdPrefix="endpoint-create"
            />
          </>
        ) : null}
        <Group grow align="flex-start" hidden={kind !== "http"}>
          <TextInput
            label="Статус (необязательно)"
            placeholder="по умолчанию сервер выберет 200"
            data-testid="endpoint-create-status"
            error={errors.status?.message}
            {...register("status")}
          />
          <TextInput
            label="Media type (необязательно)"
            placeholder="application/json"
            data-testid="endpoint-create-media-type"
            {...register("mediaType")}
          />
        </Group>
        <Textarea
          hidden={kind !== "http"}
          label="Тело ответа, JSON (необязательно)"
          description="Пустое поле — тоже валидный ответ: закреплённое пустое тело. Обычно это не то, что нужно — впишите JSON, который должен вернуться."
          placeholder={'{\n  "ok": true\n}'}
          // NOT autosize: Mantine's autosize Textarea measures itself
          // through document.fonts.addEventListener, which jsdom does not
          // implement — every test render would throw before the first
          // assertion. A fixed height costs nothing this screen needs.
          rows={4}
          data-testid="endpoint-create-body"
          error={errors.bodyText?.message}
          {...register("bodyText")}
        />
        <Textarea
          hidden={kind !== "http"}
          label="Функция (Lua, необязательно) — вместо тела: над аргументом req, возвращает status, body, headers"
          description="Раздел «Функции» в руководстве. Компилируется при сохранении: синтаксическая ошибка — отказ со словами парсера."
          rows={4}
          styles={{ input: { fontFamily: "var(--mantine-font-family-monospace)" } }}
          data-testid="endpoint-create-function"
          error={producerError ?? undefined}
          {...register("functionText")}
        />
        <Button
          type="submit"
          w="fit-content"
          leftSection={<IconPlus size={16} />}
          loading={createEndpoint.isPending}
          data-testid="endpoint-create-submit"
        >
          {createEndpoint.isPending
            ? "Создаём…"
            : kind === "http"
              ? "Создать endpoint"
              : "Создать поток"}
        </Button>
      </Stack>
    </Card>
  );
}

function EndpointList({
  id,
  endpoints,
  workspaceUrl,
  limits,
  initialEditingId,
}: {
  id: number;
  endpoints: EndpointView[];
  workspaceUrl: string | undefined;
  limits: ServerConfigViewLimits | undefined;
  initialEditingId?: number;
}): ReactElement {
  const queryClient = useQueryClient();
  // P6e: at most one row's browser test client open at a time, like the
  // edit form — a second open client would hold a second connection.
  const [testingId, setTestingId] = useState<number | null>(null);
  // Named per-row rather than read off deleteEndpoint.error directly: the
  // mutation itself carries no memory of WHICH endpoint it was deleting.
  const [deleteError, setDeleteError] = useState<{ label: string; message: string } | null>(null);
  // At most one row's edit form open at a time — id of the endpoint, or
  // null. A row swaps its own display for the form rather than opening a
  // modal: the whole point of the affordance is showing the CURRENT values
  // next to the fields being changed.
  const [editingId, setEditingId] = useState<number | null>(initialEditingId ?? null);

  const deleteEndpoint = useDeleteEndpoint({
    mutation: {
      onSuccess: () => {
        setDeleteError(null);
        // §3.9: useDeleteEndpoint must invalidate the endpoints list AND the
        // workspace, same as create.
        void queryClient.invalidateQueries({ queryKey: getListEndpointsQueryKey(id) });
        void queryClient.invalidateQueries({ queryKey: getGetWorkspaceQueryKey(id) });
      },
    },
  });

  function handleDelete(ep: EndpointView): void {
    const label = `${ep.method} ${ep.path}`;
    modals.openConfirmModal({
      title: "Удалить endpoint",
      children: (
        <Text size="sm">
          Удалить «{label}»? Это действие необратимо. Если нужно просто поправить endpoint, вместо
          удаления используйте кнопку «Изменить» в списке.
        </Text>
      ),
      labels: { confirm: "Удалить", cancel: "Отмена" },
      confirmProps: { color: "red", "data-testid": "endpoint-delete-confirm" },
      onConfirm: () => {
        deleteEndpoint.mutate(
          { id, eid: ep.id },
          { onError: (err) => setDeleteError({ label, message: describeApiFailure(err) }) },
        );
      },
    });
  }

  return (
    <Stack gap="sm">
      {deleteError !== null ? (
        <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
          Не удалось удалить «{deleteError.label}»: {deleteError.message}
        </Alert>
      ) : null}
      <Card withBorder p={0} data-testid="endpoint-list">
        <Stack gap={0}>
          {endpoints.map((ep) => (
            <Group
              key={ep.id}
              justify="space-between"
              wrap="nowrap"
              px="md"
              py="sm"
              data-testid="endpoint-row"
              style={{ borderTop: "1px solid var(--mantine-color-gray-3)" }}
            >
              {editingId === ep.id ? (
                ep.kind === "http" ? (
                  <EditEndpointForm
                    id={id}
                    endpoint={ep}
                    onDone={() => setEditingId(null)}
                    onCancel={() => setEditingId(null)}
                  />
                ) : (
                  <EditStreamForm
                    id={id}
                    endpoint={ep}
                    limits={limits}
                    onDone={() => setEditingId(null)}
                    onCancel={() => setEditingId(null)}
                  />
                )
              ) : testingId === ep.id && ep.kind !== "http" && workspaceUrl !== undefined ? (
                <Stack gap="xs" w="100%" data-testid="endpoint-test-client">
                  <Group justify="space-between">
                    <Text size="sm" fw={500}>
                      {ep.method} {ep.path} — проверка из браузера
                    </Text>
                    <Button
                      variant="default"
                      size="xs"
                      onClick={() => setTestingId(null)}
                      data-testid="endpoint-test-close"
                    >
                      Свернуть
                    </Button>
                  </Group>
                  <StreamTestClient
                    url={`${workspaceUrl}${ep.path}`}
                    kind={ep.kind}
                    eventNames={eventNamesOf(ep.stream)}
                    testIdPrefix={`endpoint-${ep.id}`}
                  />
                </Stack>
              ) : (
                <>
                  <div>
                    <Group gap="xs">
                      <Text size="sm" fw={500}>
                        {ep.method} {ep.path}
                      </Text>
                      {kindLabel(ep.kind) ? (
                        <Badge color="blue" size="sm" data-testid="endpoint-kind">
                          {kindLabel(ep.kind)}
                        </Badge>
                      ) : null}
                      {ep.routeOff ? (
                        <Badge color="yellow" size="sm">
                          маршрут выключен
                        </Badge>
                      ) : null}
                      {hasFunction(ep) ? (
                        <Badge color="grape" size="sm" data-testid="endpoint-function">
                          функция Lua
                        </Badge>
                      ) : null}
                    </Group>
                    <Text size="xs" c="dimmed">
                      канонический путь {ep.canonicalPath} · активный статус {ep.activeStatus} ·
                      статусы: {Object.keys(ep.responses).join(", ") || "—"}
                    </Text>
                    <Text size="xs" c="dimmed">
                      создан {formatTimestamp(ep.createdAt)} · обновлён{" "}
                      {formatTimestamp(ep.updatedAt)}
                    </Text>
                  </div>
                  <Group gap="xs" wrap="nowrap">
                    {ep.kind !== "http" ? (
                      <Button
                        variant="default"
                        size="xs"
                        leftSection={<IconPlugConnected size={16} />}
                        disabled={workspaceUrl === undefined}
                        onClick={() => setTestingId(ep.id)}
                        data-testid="endpoint-test-toggle"
                      >
                        Проверить
                      </Button>
                    ) : null}
                    <Button
                      variant="default"
                      size="xs"
                      leftSection={<IconPencil size={16} />}
                      onClick={() => setEditingId(ep.id)}
                      data-testid="endpoint-edit-toggle"
                    >
                      Изменить
                    </Button>
                    <Button
                      variant="default"
                      size="xs"
                      color="red"
                      leftSection={<IconTrash size={16} />}
                      onClick={() => handleDelete(ep)}
                      loading={deleteEndpoint.isPending}
                      data-testid="endpoint-delete"
                    >
                      Удалить
                    </Button>
                  </Group>
                </>
              )}
            </Group>
          ))}
        </Stack>
      </Card>
    </Stack>
  );
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
// how to edit one.
function EditEndpointForm({
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
  const producerError = producerConflict({
    functionText: watch("functionText"),
    bodyText: watch("bodyText"),
    mediaType: watch("mediaType"),
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
  const base = conflictBase ?? endpoint;

  const updateEndpoint = useUpdateEndpoint({
    mutation: {
      onSuccess: (res) => {
        if (res.status !== 200) {
          return;
        }
        // §3.9, same as create/delete above: a PUT can move the endpoint's
        // (method, path) or its revision-bearing settings, so both queries
        // invalidate, not just the endpoints list.
        void queryClient.invalidateQueries({ queryKey: getListEndpointsQueryKey(id) });
        void queryClient.invalidateQueries({ queryKey: getGetWorkspaceQueryKey(id) });
        onDone();
      },
    },
  });

  function onSubmit(values: EditForm): void {
    const status = values.status.trim();
    const mediaType = values.mediaType.trim();
    const bodyText = values.bodyText.trim();
    // A full-replacement PUT still only touches the ONE variant this form
    // edits — every other status this endpoint already serves is resent
    // byte-for-byte from `base` (endpoint, or the conflict's own current
    // document once one has landed).
    //
    // The edited status itself is MUTATED from the variant it already had,
    // not replaced with a fresh literal — the same anti-pattern
    // from_traffic.go's pinObservedBody names by comment: a struct literal
    // here would silently discard bodyEncoding (and any when/headers/
    // recipes/schemaPatch) the endpoint already carried at this status, the
    // instant an operator edited an unrelated field like path or mediaType.
    const responses: UpdateEndpointRequestResponses = { ...base.responses };
    const functionText = values.functionText.trim() === "" ? "" : values.functionText;
    if (producerError !== null) {
      return;
    }
    responses[status] =
      functionText !== ""
        ? {
            // A18 D5: a function is the variant's ONE producer, so every
            // other producer goes — body, encoding, media type, and also
            // bodyRef, recipes and schemaPatch, which are not this form's
            // fields but which the server refuses beside a function by name
            // (function_and_body). Leaving them would make an asset- or
            // recipe-backed variant impossible to convert from this screen:
            // the 400 would name a field the form cannot clear. The label
            // says what is replaced; `when[]` and `headers` survive (a
            // function keeps its selection and its headers). Mode is the
            // neutral one — A18's own rows leave it unset.
            ...responses[status],
            mode: "generated",
            body: undefined,
            bodyEncoding: undefined,
            bodyRef: undefined,
            mediaType: undefined,
            recipes: undefined,
            schemaPatch: undefined,
            function: functionText,
          }
        : keepsOtherProducer(responses[status], bodyText)
          ? {
              // The variant serves from a schema (P7a, generated) or from a
              // file (A6, bodyRef) and the body box is empty: the operator
              // edited something else, and forcing `mode: pinned, body:
              // undefined` here would turn the schema into an empty pinned
              // body (the review's B2) — so the producer stays as stored.
              // mediaType is left alone too: a bodyRef refuses one.
              ...responses[status],
              mode: responses[status]?.mode ?? "generated",
              function: undefined,
            }
          : {
              ...responses[status],
              mode: "pinned",
              body: bodyText === "" ? undefined : (JSON.parse(bodyText) as unknown),
              mediaType: mediaType === "" ? undefined : mediaType,
              // A typed body replaces a file: the form said so above the box.
              bodyRef: undefined,
              // A cleared Lua box removes the function: the operator saw it in
              // the box and emptied it, so nothing is dropped unseen.
              function: undefined,
            };
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
        editVersion: conflictBase?.editVersion ?? endpoint.editVersion,
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
      void queryClient.invalidateQueries({ queryKey: getListEndpointsQueryKey(id) });
      void queryClient.invalidateQueries({ queryKey: getGetWorkspaceQueryKey(id) });
      onCancel();
      return;
    }
    setConflictBase(details);
    reset(defaultsFromEndpoint(details));
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
        const conflict =
          updateEndpoint.isError &&
          updateEndpoint.error instanceof ApiFailure &&
          updateEndpoint.error.code === "edit_conflict"
            ? updateEndpoint.error
            : null;
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
      <Group grow align="flex-start">
        <NativeSelect label="Метод" data-testid="endpoint-edit-method" {...register("method")}>
          {HTTP_METHODS.map((method) => (
            <option key={method} value={method}>
              {method}
            </option>
          ))}
        </NativeSelect>
        <TextInput
          label="Путь"
          data-testid="endpoint-edit-path"
          error={errors.path?.message}
          {...register("path")}
        />
      </Group>
      <Group grow align="flex-start">
        <TextInput
          label="Активный статус"
          data-testid="endpoint-edit-status"
          error={errors.status?.message}
          {...register("status")}
        />
        <TextInput
          label="Media type (необязательно)"
          data-testid="endpoint-edit-media-type"
          {...register("mediaType")}
        />
      </Group>
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
      {producerNote(activeVariant(base)) !== null ? (
        <Text size="xs" c="dimmed" data-testid="endpoint-edit-producer-note">
          {producerNote(activeVariant(base))}
        </Text>
      ) : null}
      <Textarea
        label="Тело ответа, JSON (необязательно)"
        rows={4}
        data-testid="endpoint-edit-body"
        error={errors.bodyText?.message}
        {...register("bodyText")}
      />
      <Textarea
        label="Функция (Lua, необязательно) — вместо тела: над аргументом req, возвращает status, body, headers"
        description="Функция заменяет тело, файл, рецепты и правки схемы этого статуса; условия when и заголовки остаются. Пустое поле у варианта с функцией — удаление функции."
        rows={4}
        styles={{ input: { fontFamily: "var(--mantine-font-family-monospace)" } }}
        data-testid="endpoint-edit-function"
        error={producerError ?? undefined}
        {...register("functionText")}
      />
      <Group gap="xs">
        <Button
          type="submit"
          size="xs"
          loading={updateEndpoint.isPending}
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

// EditStreamForm is the stream row's editor (P6e): path plus the same four
// behaviours the create form shows, seeded from the stored definition. PUT
// is a full replacement, so kind and stream are resent together — an
// omitted kind reads as "http" and the server refuses a stream on an http
// row by name — and activeStatus is 200 because a stream row admits no
// other value (customep.ValidateStreamFor). overrideOn/routeOff ride along
// from the row, as EditEndpointForm sends them.
function EditStreamForm({
  id,
  endpoint,
  limits,
  onDone,
  onCancel,
}: {
  id: number;
  endpoint: EndpointView;
  limits: ServerConfigViewLimits | undefined;
  onDone: () => void;
  onCancel: () => void;
}): ReactElement {
  const queryClient = useQueryClient();
  const kind = endpoint.kind as StreamKind;
  const [path, setPath] = useState(endpoint.path);
  const [draft, setDraft] = useState<StreamDraft>(() => draftFromDefinition(endpoint.stream));
  const [draftError, setDraftError] = useState<string | null>(null);
  const [conflictBase, setConflictBase] = useState<EndpointConflictDetails | null>(null);
  const base = conflictBase ?? endpoint;

  const updateEndpoint = useUpdateEndpoint({
    mutation: {
      onSuccess: (res) => {
        if (res.status !== 200) {
          return;
        }
        void queryClient.invalidateQueries({ queryKey: getListEndpointsQueryKey(id) });
        void queryClient.invalidateQueries({ queryKey: getGetWorkspaceQueryKey(id) });
        onDone();
      },
    },
  });

  function submit(): void {
    const trimmed = path.trim();
    if (trimmed === "" || !trimmed.startsWith("/")) {
      setDraftError("Путь должен начинаться с /");
      return;
    }
    const def = draftToDefinition(kind, draft);
    if ("error" in def) {
      setDraftError(def.error);
      return;
    }
    setDraftError(null);
    updateEndpoint.mutate({
      id,
      eid: endpoint.id,
      data: {
        method: "GET",
        path: trimmed,
        kind,
        stream: def.stream,
        activeStatus: 200,
        overrideOn: base.overrideOn,
        routeOff: base.routeOff,
        // A20's second reader: a stream row still carries listSize, delayMs
        // and reqSchema (an agent may have written them; a stream ignores
        // the first two but a full-replacement PUT that omits them resets
        // the row to the defaults), so they ride along exactly as
        // EditEndpointForm sends them.
        listSize: base.listSize,
        delayMs: base.delayMs,
        reqSchema: base.reqSchema,
        // P7a: the operation fields survive a stream edit the same way.
        operation: base.operation,
        editVersion: conflictBase?.editVersion ?? endpoint.editVersion,
      },
    });
  }

  function handleConflictReload(details: EndpointConflictDetails | EditConflictTombstone): void {
    if (isGoneTombstone(details)) {
      void queryClient.invalidateQueries({ queryKey: getListEndpointsQueryKey(id) });
      void queryClient.invalidateQueries({ queryKey: getGetWorkspaceQueryKey(id) });
      onCancel();
      return;
    }
    setConflictBase(details);
    setPath(details.path);
    setDraft(draftFromDefinition(details.stream));
  }

  const conflict =
    updateEndpoint.isError &&
    updateEndpoint.error instanceof ApiFailure &&
    updateEndpoint.error.code === "edit_conflict"
      ? updateEndpoint.error
      : null;

  return (
    <Stack gap="sm" w="100%" data-testid="endpoint-edit-stream-form">
      {conflict !== null ? (
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
      ) : updateEndpoint.isError ? (
        <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
          {describeApiFailureDetailed(updateEndpoint.error)}
        </Alert>
      ) : null}
      {draftError ? (
        <Alert
          color="red"
          icon={<IconAlertTriangle size={18} />}
          role="alert"
          data-testid="endpoint-edit-stream-error"
        >
          {draftError}
        </Alert>
      ) : null}
      <TextInput
        label={`Путь (${kindLabel(endpoint.kind)}, GET)`}
        value={path}
        onChange={(e) => setPath(e.currentTarget.value)}
        data-testid="endpoint-edit-path"
      />
      <StreamEditor kind={kind} draft={draft} onChange={setDraft} testIdPrefix="endpoint-edit" />
      <StreamCapsStrip
        workspaceId={id}
        path={path}
        kind={kind}
        draft={draft}
        limits={limits}
        testIdPrefix="endpoint-edit"
      />
      <Group gap="xs">
        <Button
          type="button"
          size="xs"
          loading={updateEndpoint.isPending}
          onClick={submit}
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
