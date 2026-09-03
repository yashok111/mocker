import { useEffect, useState, type ReactElement } from "react";
import {
  Alert,
  Badge,
  Button,
  Checkbox,
  Code,
  CopyButton,
  Group,
  List,
  Loader,
  Stack,
  Table,
  Text,
  Title,
} from "@mantine/core";
import { IconAlertTriangle, IconCheck, IconCopy } from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import { getGetWorkspaceQueryKey } from "@/api/generated/workspaces/workspaces.ts";
import {
  getGetAuthPresetQueryKey,
  getListWorkspaceOperationsQueryKey,
  useApplyAuthPreset,
  useGetAuthPreset,
} from "@/api/generated/operations/operations.ts";
import type { AuthPresetProposal, PresetConflictDetails } from "@/api/generated/schemas";
import { ApiFailure } from "@/api/client";
import { describeApiFailureDetailed } from "@/api/errors";

// AuthPresetPanel implements DESIGN §10: the identity/token mapping must be
// SHOWN and APPROVED before anything is written, which is why preview
// (useGetAuthPreset, a GET that writes nothing) and apply (useApplyAuthPreset)
// are two separate routes rather than one that derives-and-writes. Approval
// happens here as a checkbox per proposed binding, all checked by default —
// unchecking one is the only way to keep the preview from becoming the write.
// notesPreview is how many skip-notes are shown before the toggle. Five is
// enough to see what KIND of thing was skipped — which is the question a
// reader actually has — without the list becoming the screen.
const notesPreview = 5;

export function AuthPresetPanel({ id }: { id: number }): ReactElement {
  const proposal = useGetAuthPreset(id);

  return (
    <div data-testid="auth-preset-panel">
      <Title order={2}>Auth preset</Title>
      {proposal.isPending ? (
        <Group gap="xs">
          <Loader size="sm" />
          <Text size="sm" component="output">
            Загрузка…
          </Text>
        </Group>
      ) : proposal.isError ? (
        <Stack gap="sm">
          <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
            {describeApiFailureDetailed(proposal.error)}
          </Alert>
          <Button
            variant="default"
            w="fit-content"
            onClick={() => void proposal.refetch()}
            data-testid="auth-preset-retry"
          >
            Повторить
          </Button>
        </Stack>
      ) : proposal.data.status !== 200 ? (
        <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
          {describeApiFailureDetailed(null)}
        </Alert>
      ) : (
        // Handed off to a child that owns selection/apply state: keeping
        // that state here would need it re-derived across a query-object
        // property TypeScript cannot narrow inside nested closures (proposal
        // .data is not provably readonly across a callback boundary), and
        // splitting it out means the loaded AuthPresetProposal is a plain,
        // fully-narrowed prop instead.
        <AuthPresetBindings
          id={id}
          proposal={proposal.data.data}
          onReload={() => void proposal.refetch()}
        />
      )}
    </div>
  );
}

// opKeyLabel turns an opKey back into "METHOD /path" for display — the
// preset's own conflict (D12) names STALE ROWS by opKey, not by the binding
// documents the operator already has on screen (D6's reasoning: what the
// caller lacks is which ones moved, not their content), so a human-readable
// row label has to come from decoding the key itself rather than from a
// binding lookup.
function opKeyLabel(opKey: string): string {
  try {
    return decodeURIComponent(opKey);
  } catch {
    return opKey;
  }
}

function AuthPresetBindings({
  id,
  proposal,
  onReload,
}: {
  id: number;
  proposal: AuthPresetProposal;
  onReload: () => void;
}): ReactElement {
  const queryClient = useQueryClient();
  // Selection is index-based against the CURRENTLY LOADED proposal, reset
  // whenever a fresh proposal arrives — the server derives bindings fresh
  // from the spec on every GET (it writes nothing), so an old selection
  // could outlive the bindings it was chosen against.
  const [selected, setSelected] = useState<boolean[]>(() => proposal.bindings.map(() => true));
  const [appliedCount, setAppliedCount] = useState<number | null>(null);
  const [notesExpanded, setNotesExpanded] = useState(false);
  // editVersions (A3): the map this screen will send on its next apply —
  // seeded from this read's own proposal.editVersions and then kept current
  // by the write path below, never re-read from `proposal` at submit time
  // (D10's "vacuous implementation adds a request" argument, same as
  // OperationEditor's single `editVersion`). Without this, a second apply
  // right after a successful one would resend the PRE-apply map and get a
  // false edit_conflict against the operator's own just-completed write.
  const [editVersions, setEditVersions] = useState<Record<string, number>>(
    () => proposal.editVersions,
  );

  useEffect(() => {
    setSelected(proposal.bindings.map(() => true));
    setAppliedCount(null);
    setEditVersions(proposal.editVersions);
    // Only the bindings identity should reset the selection — proposal
    // itself is a fresh object every render (React Query does not memoize
    // it across cache updates), and keying off it would wipe an in-progress
    // selection on every unrelated re-render of the parent.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [proposal.bindings]);

  const applyPreset = useApplyAuthPreset({
    mutation: {
      onSuccess: (res) => {
        if (res.status !== 200) {
          return;
        }
        setAppliedCount(res.data.applied);
        // A3: adopt the fresh per-row tokens this write itself returned —
        // merged over the seeded map so untouched opKeys (deselected
        // bindings whose version was sent for the check but never written)
        // keep the version they already had. Without this the NEXT apply
        // from this same open panel would resend the pre-apply map and
        // conflict against the write that just succeeded.
        setEditVersions((prev) => ({ ...prev, ...res.data.editVersions }));
        // §3.9: applying moves the revision (workspace) and can add recipes
        // to operations already showing an override summary or an open
        // override doc — invalidate the workspace, the operations list, and
        // every currently cached override document for this workspace
        // (their query keys all start with the operations-list key plus
        // "/opKey" — the list key alone has no trailing segment). Also
        // invalidate the auth-preset GET itself so a later remount/refetch
        // does not seed a new proposal off a stale editVersions map.
        void queryClient.invalidateQueries({ queryKey: getGetWorkspaceQueryKey(id) });
        void queryClient.invalidateQueries({ queryKey: getGetAuthPresetQueryKey(id) });
        void queryClient.invalidateQueries({ queryKey: getListWorkspaceOperationsQueryKey(id) });
        const overrideDocPrefix = `${getListWorkspaceOperationsQueryKey(id)[0]}/`;
        void queryClient.invalidateQueries({
          predicate: (query) => {
            const key = query.queryKey[0];
            return typeof key === "string" && key.startsWith(overrideDocPrefix);
          },
        });
      },
    },
  });

  function toggle(index: number): void {
    setSelected((prev) => prev.map((v, i) => (i === index ? !v : v)));
    setAppliedCount(null);
  }

  function handleApply(): void {
    const chosen = proposal.bindings.filter((_, i) => selected[i] === true);
    if (chosen.length === 0) {
      return;
    }
    // A3/D12: the map is REQUIRED and is sent UNFILTERED — the exact map
    // this screen currently holds (`editVersions` state, seeded from the
    // preceding GET and kept current by this screen's own prior applies —
    // see the state declaration above), not a subset rebuilt from `chosen`.
    // The server's own reasoning (preset_handlers.go's handleApplyAuthPreset)
    // is the citation: it scopes the check to the opKeys the submitted
    // bindings actually touch and only refuses on a MISSING entry, precisely
    // so the UI never has to recompute opKeys client-side (a Binding carries
    // no opKey of its own) just to filter this map down after the operator
    // deselects rows.
    applyPreset.mutate({ id, data: { bindings: chosen, editVersions } });
  }

  const selectedCount = selected.filter(Boolean).length;

  return (
    <Stack gap="md">
      {proposal.notes.length > 0 ? (
        // notes say what was SKIPPED and why — a workspace with no spec
        // attached lands here with an empty bindings list and exactly one
        // note explaining it, which is what keeps that state from reading
        // as a broken/empty screen rather than an ordinary one.
        //
        // Capped at notesPreview, because the real document produces 412 of
        // them. Measured on the customer's own 130-operation spec: 31 bindings
        // and 412 notes, one per property the deriver looked at and declined.
        // Rendered flat — as this was until a dress rehearsal against that
        // document — the wall of yellow buries the 31 rows the operator came
        // to approve. The COUNT stays in the title, because a silent skip
        // reads as "nothing to do" and that is the whole reason notes exist.
        //
        // An explicit slice rather than Mantine's <Spoiler>: Spoiler decides
        // whether to show its control by MEASURING its content, and under
        // jsdom every element is zero-height, so it renders everything and the
        // test that claims to guard this would pass against a flat list. Same
        // reason VTable is not in this tree.
        <Alert
          color="yellow"
          title={`Пропущено: ${proposal.notes.length}`}
          data-testid="auth-preset-notes"
        >
          <List size="sm">
            {(notesExpanded ? proposal.notes : proposal.notes.slice(0, notesPreview)).map(
              (note) => (
                <List.Item key={note}>{note}</List.Item>
              ),
            )}
          </List>
          {proposal.notes.length > notesPreview ? (
            <Button
              variant="subtle"
              size="compact-sm"
              mt="xs"
              onClick={() => setNotesExpanded((v) => !v)}
              data-testid="auth-preset-notes-toggle"
            >
              {notesExpanded ? "Свернуть" : `Показать все ${proposal.notes.length}`}
            </Button>
          ) : null}
        </Alert>
      ) : null}

      {proposal.schemes.length > 0 || proposal.authPaths.length > 0 ? (
        <Stack gap={4} data-testid="auth-preset-context">
          {proposal.schemes.length > 0 ? (
            <Text size="sm" c="dimmed">
              Схемы: {proposal.schemes.join(", ")}
            </Text>
          ) : null}
          {proposal.authPaths.length > 0 ? (
            <Text size="sm" c="dimmed">
              Пути авторизации: {proposal.authPaths.join(", ")}
            </Text>
          ) : null}
        </Stack>
      ) : null}

      <Stack gap={4}>
        <Text size="sm" fw={500}>
          Пример токена
        </Text>
        <Group gap="xs" wrap="nowrap" align="flex-start">
          <Code
            block
            style={{ flex: 1, wordBreak: "break-all" }}
            data-testid="auth-preset-sample-jwt"
          >
            {proposal.sampleJwt}
          </Code>
          <CopyButton value={proposal.sampleJwt}>
            {({ copied, copy }) => (
              <Button
                size="xs"
                variant="default"
                color={copied ? "teal" : undefined}
                leftSection={copied ? <IconCheck size={14} /> : <IconCopy size={14} />}
                onClick={copy}
              >
                {copied ? "Скопировано" : "Копировать"}
              </Button>
            )}
          </CopyButton>
        </Group>
      </Stack>

      {proposal.bindings.length === 0 ? (
        <Text c="dimmed" data-testid="auth-preset-empty">
          Предлагать нечего
        </Text>
      ) : (
        <Table data-testid="auth-preset-bindings">
          <Table.Thead>
            <Table.Tr>
              <Table.Th />
              <Table.Th>Метод и путь</Table.Th>
              <Table.Th>Статус</Table.Th>
              <Table.Th>Путь в теле</Table.Th>
              <Table.Th>Рецепт</Table.Th>
              <Table.Th>Почему</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {proposal.bindings.map((binding, index) => (
              // Bindings carry no stable id of their own (the server derives
              // them fresh every GET); method+path+status+dataPath is unique
              // within one proposal, which index alone is not once the list
              // is ever re-sorted.
              <Table.Tr
                key={`${binding.method} ${binding.path} ${binding.status} ${binding.dataPath}`}
              >
                <Table.Td>
                  <Checkbox
                    checked={selected[index] ?? true}
                    onChange={() => toggle(index)}
                    data-testid={`auth-preset-binding-${index}`}
                    aria-label={`${binding.method} ${binding.path}`}
                  />
                </Table.Td>
                <Table.Td>
                  <Badge size="sm" variant="light">
                    {binding.method}
                  </Badge>{" "}
                  {binding.path}
                </Table.Td>
                <Table.Td>{binding.status}</Table.Td>
                <Table.Td>
                  <Code>{binding.dataPath}</Code>
                </Table.Td>
                <Table.Td>{binding.recipe.kind}</Table.Td>
                <Table.Td>
                  <Text size="sm" c="dimmed">
                    {binding.reason}
                  </Text>
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      )}

      {(() => {
        const conflict =
          applyPreset.isError &&
          applyPreset.error instanceof ApiFailure &&
          applyPreset.error.code === "edit_conflict"
            ? applyPreset.error
            : null;
        if (conflict !== null) {
          // D10/D12's own distinction: the preset's conflict carries
          // staleVersions — IDENTITIES AND NUMBERS, deliberately not
          // documents — so this affordance is the LIST of opKeys that
          // moved, with a reload, not "the current document" the four
          // single-object screens show. Requiring a document here is an
          // assertion no correct implementation of this route could satisfy
          // (D10's own words).
          const details = conflict.details as PresetConflictDetails;
          const entries = Object.entries(details.staleVersions);
          return (
            <Alert
              color="orange"
              icon={<IconAlertTriangle size={18} />}
              role="alert"
              data-testid="auth-preset-conflict"
            >
              <Text size="sm">{describeApiFailureDetailed(conflict)}</Text>
              <List size="sm" mt="xs" data-testid="auth-preset-conflict-stale">
                {entries.map(([opKey, version]) => (
                  <List.Item key={opKey}>
                    {opKeyLabel(opKey)} —{" "}
                    {version === null ? "удалена" : `текущая версия ${version}`}
                  </List.Item>
                ))}
              </List>
              <Button
                variant="light"
                size="xs"
                mt="xs"
                onClick={onReload}
                data-testid="auth-preset-conflict-reload"
              >
                Обновить
              </Button>
            </Alert>
          );
        }
        return applyPreset.isError ? (
          <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
            {describeApiFailureDetailed(applyPreset.error)}
          </Alert>
        ) : null;
      })()}
      {appliedCount !== null ? (
        <Text size="sm" data-testid="auth-preset-applied">
          Применено привязок: {appliedCount}
        </Text>
      ) : null}

      {proposal.bindings.length > 0 ? (
        <Button
          w="fit-content"
          // An empty selection is disabled rather than sent as []: the
          // server would happily accept [] and apply nothing, but a button
          // that silently no-ops on click reads as broken, not as "there is
          // nothing approved right now".
          disabled={selectedCount === 0}
          loading={applyPreset.isPending}
          onClick={handleApply}
          data-testid="auth-preset-apply"
        >
          Применить ({selectedCount})
        </Button>
      ) : null}
    </Stack>
  );
}
