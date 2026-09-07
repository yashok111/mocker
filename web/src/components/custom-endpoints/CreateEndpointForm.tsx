import { useEffect, useState } from "react";
import type { ReactElement } from "react";
import {
  Alert,
  Anchor,
  Button,
  Card,
  Group,
  NativeSelect,
  Stack,
  Text,
  Textarea,
  TextInput,
} from "@mantine/core";
import { IconAlertTriangle, IconPlus } from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import { useForm } from "react-hook-form";
import { type } from "arktype";
import { invalidateEndpointChange } from "@/api/cachePolicy";
import { useCreateEndpoint } from "@/api/generated/endpoints/endpoints.ts";
import type { ServerConfigViewLimits } from "@/api/generated/schemas";
import { StreamEditor } from "../StreamEditor";
import { StreamCapsStrip } from "../stream/StreamCapsStrip";
import {
  draftToDefinition,
  emptyStreamDraft,
  type StreamDraft,
  type StreamKind,
} from "../stream/streamDraft";
import { GUIDE_FUNCTIONS } from "../VariantEditor";
import { describeApiFailureDetailed } from "@/api/errors";
import { jsonLocation } from "@/validation/json";
import { arktypeResolver } from "@/validation/resolver";
import { EndpointFormFields } from "./EndpointFormFields";
import { pathTemplate, statusCodeField } from "./shared";

// P6e (DESIGN §30.14): the type selector. A stream is a custom endpoint and
// nothing else (§30.2), so it is created on this very form — the selector
// swaps the body editor for StreamEditor's four behaviours and pins the
// method to GET, which is the only one a stream handshake can use.
const KIND_OPTIONS: { value: "http" | StreamKind; label: string }[] = [
  { value: "http", label: "Обычный ответ (HTTP)" },
  { value: "sse", label: "Поток событий (SSE)" },
  { value: "ws", label: "WebSocket" },
];

const statusField = statusCodeField(false);

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

// createProducerConflict is A18 D5's "one producer per variant" for the
// CREATE card, which keeps its flat fields (a new endpoint is one status and
// one body, the common case; the edit form mounts VariantEditor over the
// whole variant). Exported for the test.
export function createProducerConflict(fields: {
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

export function CreateEndpointForm({
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
  const producerError = createProducerConflict({
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
        invalidateEndpointChange(queryClient, id);
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
        <EndpointFormFields
          testIdPrefix="endpoint-create"
          leading={
            <NativeSelect label="Тип" data-testid="endpoint-create-kind" {...register("kind")}>
              {KIND_OPTIONS.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </NativeSelect>
          }
          methodLabel={kind === "http" ? "Метод" : "Метод (поток — всегда GET)"}
          methodDisabled={kind !== "http"}
          methodField={register("method")}
          pathLabel="Путь"
          pathPlaceholder={kind === "http" ? "/custom/ping" : "/events"}
          pathError={errors.path?.message}
          pathField={register("path")}
        />
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
          description={
            <>
              <Anchor href={GUIDE_FUNCTIONS} size="xs">
                Раздел «Функция эндпоинта» в руководстве
              </Anchor>
              . Компилируется при сохранении: синтаксическая ошибка — отказ со словами парсера.
            </>
          }
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
