import { useState } from "react";
import type { ReactElement } from "react";
import { Alert, Button, Group, Stack, Text, TextInput } from "@mantine/core";
import { IconAlertTriangle } from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import { invalidateEndpointChange } from "@/api/cachePolicy";
import { useUpdateEndpoint } from "@/api/generated/endpoints/endpoints.ts";
import type {
  EditConflictTombstone,
  EndpointConflictDetails,
  EndpointView,
  ServerConfigViewLimits,
} from "@/api/generated/schemas";
import { StreamEditor } from "../StreamEditor";
import { StreamCapsStrip } from "../stream/StreamCapsStrip";
import {
  draftFromDefinition,
  draftToDefinition,
  type StreamDraft,
  type StreamKind,
} from "../stream/streamDraft";
import { conflictOf, describeApiFailureDetailed, isGoneTombstone } from "@/api/errors";
import { kindLabel } from "./shared";

// EditStreamForm is the stream row's editor (P6e): path plus the same four
// behaviours the create form shows, seeded from the stored definition. PUT
// is a full replacement, so kind and stream are resent together — an
// omitted kind reads as "http" and the server refuses a stream on an http
// row by name — and activeStatus is 200 because a stream row admits no
// other value (customep.ValidateStreamFor). overrideOn/routeOff ride along
// from the row, as EditEndpointForm sends them.
export function EditStreamForm({
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
        invalidateEndpointChange(queryClient, id);
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
      invalidateEndpointChange(queryClient, id);
      onCancel();
      return;
    }
    setConflictBase(details);
    setPath(details.path);
    setDraft(draftFromDefinition(details.stream));
  }

  const conflict = conflictOf(updateEndpoint);

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
