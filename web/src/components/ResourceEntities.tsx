import { useState } from "react";
import type { ReactElement } from "react";
import { Alert, Button, Code, Group, Loader, Stack, Text, Textarea } from "@mantine/core";
import { modals } from "@mantine/modals";
import { IconAlertTriangle, IconPencil, IconTrash } from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import {
  getListResourceEntitiesQueryKey,
  getListWorkspaceResourcesQueryKey,
  useListResourceEntities,
} from "@/api/generated/resources/resources.ts";
import { useDeleteResourceEntity, useSetResourceEntity } from "@/api/generated/default/default.ts";
import type { ResourceEntityView, ResourceFamilyView } from "@/api/generated/schemas";
import { describeApiFailure, describeApiFailureDetailed } from "@/api/errors";

// ResourceEntities is the entity browser ResourcesPage.tsx's own header
// comment says it does not have (D10, P3a cut the read route at round 6).
// A4 brought the read back as an agent-only route and A11 added the write
// pair, all three under the A4 rule; on 2026-09-05 the owner lifted it for
// them («надо бы доделать страницы», a Russian string quoted as data), and
// this is the screen: the rows a confirmed family actually serves, one page
// at a time, each row editable as the JSON object it is and deletable by key.
//
// Paging is the route's own id cursor (`after`, echoed as `lastId`): every
// page is its OWN query keyed by its cursor, rendered in order, and the last
// one offers «Ещё» when it came back full. No rows are copied into state,
// and there is no effect syncing server data into a local list. A write
// collapses the pages back to the first one: a delete on page 1 would
// otherwise pull page 2's first row up into a refetched page 1 while page 2
// still shows it — two copies of one row on screen, which id cursors cannot
// prevent once the pages are refetched independently.
//
// Addressing: the {family} segment carries the routeFamily percent-encoded
// (the route's own parameter description — orval substitutes path
// parameters as is), so "/users/{}/posts" travels as one segment. The scope
// of a nested or base-scoped row (scopeKey, baseScopeKey) is sent back
// exactly as the read returned it, never rebuilt here.

const PAGE = 50;

export function familySegment(family: Pick<ResourceFamilyView, "routeFamily">): string {
  return encodeURIComponent(family.routeFamily);
}

export function ResourceEntities({
  id,
  family,
}: {
  id: number;
  family: ResourceFamilyView;
}): ReactElement {
  const [cursors, setCursors] = useState<number[]>([0]);
  return (
    <Stack gap="xs" data-testid="resource-entities">
      {cursors.map((after, index) => (
        <EntityPage
          key={after}
          id={id}
          family={family}
          after={after}
          isLast={index === cursors.length - 1}
          onMore={(lastId) => setCursors((prev) => [...prev, lastId])}
          onWrite={() => setCursors([0])}
        />
      ))}
    </Stack>
  );
}

function EntityPage({
  id,
  family,
  after,
  isLast,
  onMore,
  onWrite,
}: {
  id: number;
  family: ResourceFamilyView;
  after: number;
  isLast: boolean;
  onMore: (lastId: number) => void;
  onWrite: () => void;
}): ReactElement {
  const segment = familySegment(family);
  const page = useListResourceEntities(id, segment, {
    limit: PAGE,
    after: after === 0 ? undefined : after,
  });

  if (page.isPending) {
    return (
      <Group gap="xs">
        <Loader size="sm" />
        <Text size="sm" component="output">
          Загрузка…
        </Text>
      </Group>
    );
  }
  if (page.isError || page.data.status !== 200) {
    return (
      <Stack gap="sm" data-testid="resource-entities-error">
        <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
          {describeApiFailure(page.isError ? page.error : null)}
        </Alert>
        <Button variant="default" size="xs" w="fit-content" onClick={() => void page.refetch()}>
          Повторить
        </Button>
      </Stack>
    );
  }
  const { rows, lastId } = page.data.data;
  return (
    <>
      {rows.length === 0 && after === 0 ? (
        <Text size="sm" c="dimmed" data-testid="resource-entities-empty">
          Записей пока нет — они появляются из POST на коллекцию или из записи агентом.
        </Text>
      ) : null}
      {rows.map((row) => (
        <EntityRow key={row.id} id={id} family={family} row={row} onWrite={onWrite} />
      ))}
      {isLast && rows.length === PAGE ? (
        <Button
          variant="default"
          size="xs"
          w="fit-content"
          onClick={() => onMore(lastId)}
          data-testid="resource-entities-more"
        >
          Ещё
        </Button>
      ) : null}
    </>
  );
}

function scopeLine(row: ResourceEntityView): string | null {
  const parts: string[] = [];
  if (row.scopeKey !== "") {
    parts.push(`родитель ${row.scopeKey}`);
  }
  if (row.baseScopeKey !== "") {
    parts.push(`basePath ${row.baseScopeKey}`);
  }
  return parts.length === 0 ? null : parts.join(" · ");
}

// EntityRow owns its own two mutations (one row edited or deleted at a
// time is what a person does; a shared mutation would not remember which
// row it was acting on). The edit is inline, as EndpointList's is: the
// current JSON is what gets changed, so it stays on screen next to the box.
function EntityRow({
  id,
  family,
  row,
  onWrite,
}: {
  id: number;
  family: ResourceFamilyView;
  row: ResourceEntityView;
  onWrite: () => void;
}): ReactElement {
  const queryClient = useQueryClient();
  const [editing, setEditing] = useState(false);
  // updatedAt of the row the draft was opened from. The PUT has no version
  // check (A11's request carries data and scope only), so the one thing
  // this form can do about a row that changed under an open draft — an
  // agent's set_resource_entity, a POST on the mock plane — is notice the
  // refetched updatedAt and refuse to overwrite the newer row with the
  // stale text.
  const [draftOf, setDraftOf] = useState<string | null>(null);
  const stale = editing && draftOf !== null && draftOf !== row.updatedAt;
  const [text, setText] = useState("");
  const [parseError, setParseError] = useState<string | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);
  const segment = familySegment(family);

  function invalidate(): void {
    // Every open page (prefix match on the route path) and the family list
    // (entityCount is what the row above the table shows); then the pages
    // collapse to the first (the header comment says why).
    void queryClient.invalidateQueries({ queryKey: getListResourceEntitiesQueryKey(id, segment) });
    void queryClient.invalidateQueries({ queryKey: getListWorkspaceResourcesQueryKey(id) });
    onWrite();
  }

  const setEntity = useSetResourceEntity({
    mutation: {
      onSuccess: (res) => {
        if (res.status === 200) {
          setEditing(false);
          invalidate();
        }
      },
    },
  });
  const deleteEntity = useDeleteResourceEntity({
    mutation: {
      onSuccess: () => {
        setDeleteError(null);
        invalidate();
      },
    },
  });

  // The scope travels back only when the row has one: the request's own
  // description says both default to "" (the top-level scope), and a
  // top-level family's write must not carry a key it never had.
  const scope = {
    scopeKey: row.scopeKey === "" ? undefined : row.scopeKey,
    baseScopeKey: row.baseScopeKey === "" ? undefined : row.baseScopeKey,
  };

  function startEdit(): void {
    setText(JSON.stringify(row.data, null, 2));
    setDraftOf(row.updatedAt);
    setParseError(null);
    setEditing(true);
  }

  function save(): void {
    if (stale) {
      return;
    }
    let parsed: unknown;
    try {
      parsed = JSON.parse(text);
    } catch (err) {
      setParseError(`JSON невалиден: ${err instanceof Error ? err.message : String(err)}`);
      return;
    }
    if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) {
      setParseError("Запись — JSON-объект");
      return;
    }
    setParseError(null);
    setEntity.mutate({
      id,
      family: segment,
      key: row.entityKey,
      data: { data: parsed as Record<string, unknown>, ...scope },
    });
  }

  function remove(): void {
    modals.openConfirmModal({
      title: "Удалить запись",
      children: (
        <Text size="sm">
          Удалить запись «{row.entityKey}» из «{family.name}»? Следующий GET её не увидит; вернуть
          можно только откатом на чекпойнт с данными ресурсов.
        </Text>
      ),
      labels: { confirm: "Удалить", cancel: "Отмена" },
      confirmProps: { color: "red", "data-testid": "entity-delete-confirm" },
      onConfirm: () => {
        deleteEntity.mutate(
          {
            id,
            family: segment,
            key: row.entityKey,
            data:
              scope.scopeKey === undefined && scope.baseScopeKey === undefined ? undefined : scope,
          },
          { onError: (err) => setDeleteError(describeApiFailureDetailed(err)) },
        );
      },
    });
  }

  return (
    <Stack
      gap={4}
      px="sm"
      py="xs"
      data-testid="entity-row"
      style={{ borderTop: "1px solid var(--mantine-color-gray-3)" }}
    >
      <Group justify="space-between" wrap="nowrap" align="flex-start">
        <div>
          <Text size="sm" fw={500}>
            {family.idField ?? "id"} = {row.entityKey}
          </Text>
          {scopeLine(row) !== null ? (
            <Text size="xs" c="dimmed" ff="monospace" data-testid="entity-scope">
              {scopeLine(row)}
            </Text>
          ) : null}
        </div>
        <Group gap="xs" wrap="nowrap">
          <Button
            variant="default"
            size="xs"
            leftSection={<IconPencil size={16} />}
            onClick={startEdit}
            disabled={editing}
            data-testid="entity-edit"
          >
            Изменить
          </Button>
          <Button
            variant="default"
            size="xs"
            color="red"
            leftSection={<IconTrash size={16} />}
            onClick={remove}
            loading={deleteEntity.isPending}
            data-testid="entity-delete"
          >
            Удалить
          </Button>
        </Group>
      </Group>
      {deleteError !== null ? (
        <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
          {deleteError}
        </Alert>
      ) : null}
      {editing ? (
        <Stack gap="xs" data-testid="entity-edit-form">
          {stale ? (
            <Alert
              color="orange"
              icon={<IconAlertTriangle size={18} />}
              role="alert"
              data-testid="entity-edit-stale"
            >
              Запись изменилась, пока вы её редактировали. Откройте её заново — иначе сохранение
              затёрло бы чужую правку.
            </Alert>
          ) : null}
          {setEntity.isError ? (
            <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
              {describeApiFailureDetailed(setEntity.error)}
            </Alert>
          ) : null}
          <Textarea
            label={`Запись, JSON — поле ${family.idField ?? "id"} перезапишется ключом`}
            rows={6}
            value={text}
            onChange={(e) => setText(e.currentTarget.value)}
            error={parseError}
            styles={{ input: { fontFamily: "var(--mantine-font-family-monospace)" } }}
            data-testid="entity-edit-data"
          />
          <Group gap="xs">
            <Button
              size="xs"
              loading={setEntity.isPending}
              disabled={stale}
              onClick={save}
              data-testid="entity-edit-submit"
            >
              Сохранить
            </Button>
            <Button
              variant="default"
              size="xs"
              onClick={() => setEditing(false)}
              data-testid="entity-edit-cancel"
            >
              Отмена
            </Button>
          </Group>
        </Stack>
      ) : (
        <Code block style={{ maxHeight: 200, overflow: "auto" }} data-testid="entity-data">
          {JSON.stringify(row.data, null, 2)}
        </Code>
      )}
    </Stack>
  );
}
