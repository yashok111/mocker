import { Fragment, useState } from "react";
import type { ReactElement } from "react";
import {
  Alert,
  Anchor,
  Badge,
  Box,
  CopyButton,
  Button,
  Card,
  Group,
  Loader,
  Stack,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { modals } from "@mantine/modals";
import { IconAlertTriangle, IconCheck, IconList, IconX } from "@tabler/icons-react";
import { TabLink } from "./TabLink";
import { useQueryClient } from "@tanstack/react-query";
import {
  getListWorkspaceResourcesQueryKey,
  useDecideResource,
  useListResourceSuggestions,
  useListWorkspaceResources,
} from "@/api/generated/resources/resources.ts";
import { useGetWorkspace } from "@/api/generated/workspaces/workspaces.ts";
import type { ResourceFamilyView } from "@/api/generated/schemas";
import { describeApiFailure, describeApiFailureDetailed } from "@/api/errors";
import { ResourceEntities } from "./ResourceEntities";

// ResourcesPage is DESIGN §14 screen 7, P3a: the operator's window onto the
// slice's whole point — "created it - saw it in the list". D10 cut the route
// table down to three: this screen had no entity browser (there was no route
// left to feed one, D10's own cut of GET .../resources/{rid}/entities at
// round 6), so a confirmed row's proof was its entityCount plus a pointer at
// GET X on the mock plane. A4 brought the read back by family name and A11
// the write pair, agent-only; since 2026-09-05 «Записи» under a confirmed
// row opens ResourceEntities.tsx over exactly those three routes.
//
// D11: the first load fires GET .../resource-suggestions (below, via
// useListResourceSuggestions) purely for its side effect — it is what runs
// R8's one-time backfill on the workspace's bound spec. Its own response is
// never read here: GET .../resources already answers with EVERY suggested
// family together with its decision state (D10's ResourceFamiliesView), so
// that is the single source this screen renders from. Both hooks still have
// to be called somewhere real, not just imported, for
// web/src/api/coverage.test.ts to count them as covered (obs 42's own
// population, three routes for three callers).
//
// «подтвердить» and «отклонить» both call the ONE decision route (R9): a
// suggestion that was never confirmed declines with nothing extra, and a
// CONFIRMED resource's decline states its irreversibility in words and asks
// for the workspace slug — the same fact R10 enforces server-side.
//
// The outermost element carries data-testid="resources-page" OUTSIDE every
// state switch below, same as every other screen in this app (obs 45): a
// marker only on the success branch would make routes.test.tsx's reachability
// check depend on whether it happened to stub every query this screen fires.
export function ResourcesPage({ id }: { id: number }): ReactElement {
  const workspace = useGetWorkspace(id);
  const specId = workspace.data?.status === 200 ? workspace.data.data.specId : null;
  const workspaceUrl = workspace.data?.status === 200 ? workspace.data.data.url : "";

  // Guarded by specId !== null rather than fired unconditionally with a
  // fallback id: a workspace with no bound spec has nothing to derive
  // suggestions FROM, and the route answers 404 for an id that names no
  // spec — asking it would just be a doomed request on every render.
  useListResourceSuggestions(specId ?? 0, { query: { enabled: specId !== null } });

  const resources = useListWorkspaceResources(id);
  const families = resources.data?.status === 200 ? resources.data.data.families : [];

  return (
    <div data-testid="resources-page">
      <Stack gap="md">
        <Title order={1}>Ресурсы</Title>
        <Text size="sm" c="dimmed">
          Ресурс — семейство маршрутов (коллекция и её элемент), которое можно подтвердить и дальше
          обслуживать из хранилища вместо генератора: то, что записано через POST, переживает
          рестарт и видно в следующем GET. Ресурс, который подтвердили неправильно, не редактируется
          — его отклоняют и подтверждают заново. Строки подтверждённого семейства — кнопка «Записи»,
          по 50 на страницу. Сбросить данные всех семейств разом (заполнить заново или очистить) —
          на вкладке{" "}
          <TabLink id={id} tab="history" testId="resources-reset-data-link">
            «История»
          </TabLink>
          .
        </Text>
        {workspace.data?.status === 200 && specId === null ? (
          <Alert color="gray" data-testid="resources-no-spec">
            К воркспейсу не привязана спека — новых предложений нет. Привязать её можно на вкладке{" "}
            <TabLink id={id} tab="overview" testId="resources-no-spec-link">
              «Обзор»
            </TabLink>
            . Ниже показаны только ресурсы, подтверждённые раньше, если они остались от другой
            спеки.
          </Alert>
        ) : null}
        {resources.isPending ? (
          // role on the Text, not the Group: the live region should be the
          // sentence a screen reader announces, not the flex box around it.
          <Group gap="xs">
            <Loader size="sm" />
            <Text size="sm" component="output">
              Загрузка…
            </Text>
          </Group>
        ) : resources.isError ? (
          <Stack gap="sm" data-testid="resources-error">
            <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
              {describeApiFailure(resources.error)}
            </Alert>
            <Button
              variant="default"
              w="fit-content"
              onClick={() => void resources.refetch()}
              data-testid="resources-retry"
            >
              Повторить
            </Button>
          </Stack>
        ) : resources.data.status !== 200 ? (
          <Alert
            color="red"
            icon={<IconAlertTriangle size={18} />}
            role="alert"
            data-testid="resources-error"
          >
            {describeApiFailure(null)}
          </Alert>
        ) : families.length === 0 ? (
          // With no spec bound the alert above already said why the list is
          // empty; a second sentence about «привязанная спека» contradicted it.
          specId === null ? null : (
            <Text data-testid="resources-empty">
              В привязанной спеке не нашлось ни одного семейства маршрутов, подходящего под ресурс.
            </Text>
          )
        ) : (
          <ResourceList id={id} workspaceUrl={workspaceUrl} families={families} />
        )}
      </Stack>
    </div>
  );
}

// nestedHint answers the dimmed pointer line under a nested family's GET
// URL, or null for a top-level one. P3g raises nesting past one level
// (CLAUDE.md "Architecture"), so a path can carry more than one "{}" now —
// D12.2 requires the copy to say so rather than keep the P3e-era singular
// for a depth-2+ family, and the string stays Russian and is DATA: neither
// form is a translation of the other, both are written by hand.
function nestedHint(routeFamily: string): string | null {
  const depth = (routeFamily.match(/\{\}/g) ?? []).length;
  if (depth === 0) {
    return null;
  }
  return depth === 1
    ? "«{}» в пути — это идентификатор родительской записи, подставьте свой"
    : "«{}» в пути — это идентификаторы родительских записей, подставьте свои";
}

function ResourceList({
  id,
  workspaceUrl,
  families,
}: {
  id: number;
  workspaceUrl: string;
  families: ResourceFamilyView[];
}): ReactElement {
  const queryClient = useQueryClient();
  // Named per-row rather than read off the mutation's own .error: both
  // "подтвердить" and the undecided-suggestion "отклонить" share this one
  // decision mutation, and it does not remember on its own which row it was
  // acting on — the same shape ScenariosPage's own actionError uses.
  const [actionError, setActionError] = useState<{ label: string; message: string } | null>(null);
  // At most one family's rows open at a time, keyed by routeFamily (the
  // family's stable address — never the row id, per CLAUDE.md's rule).
  const [openFamily, setOpenFamily] = useState<string | null>(null);

  function invalidateAfterWrite(): void {
    setActionError(null);
    void queryClient.invalidateQueries({ queryKey: getListWorkspaceResourcesQueryKey(id) });
  }

  const decideResource = useDecideResource({
    mutation: { onSuccess: invalidateAfterWrite },
  });

  function handleConfirm(family: ResourceFamilyView): void {
    decideResource.mutate(
      { id, data: { routeFamily: family.routeFamily, state: "confirmed" } },
      {
        onError: (err) =>
          setActionError({ label: family.name, message: describeApiFailureDetailed(err) }),
      },
    );
  }

  // «отклонить» on a suggestion that was never confirmed needs no slug and
  // fires immediately (R9's own shape: nothing to destroy). On a CONFIRMED
  // resource it opens a modal that states the irreversibility in words and
  // asks for the workspace slug — the same fact R10 enforces on the wire.
  function handleDecline(family: ResourceFamilyView): void {
    if (family.decision !== "confirmed") {
      decideResource.mutate(
        { id, data: { routeFamily: family.routeFamily, state: "declined" } },
        {
          onError: (err) =>
            setActionError({ label: family.name, message: describeApiFailureDetailed(err) }),
        },
      );
      return;
    }
    const modalId = `resource-decline-${family.routeFamily}`;
    modals.open({
      modalId,
      title: `Отклонить «${family.name}»`,
      children: (
        <DeclineConfirmedForm
          id={id}
          family={family}
          onCancel={() => modals.close(modalId)}
          onDeclined={() => {
            modals.close(modalId);
            invalidateAfterWrite();
          }}
        />
      ),
    });
  }

  return (
    <Stack gap="sm">
      {actionError !== null ? (
        <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
          «{actionError.label}»: {actionError.message}
        </Alert>
      ) : null}
      <Card withBorder p={0} data-testid="resource-list">
        <Stack gap={0}>
          {families.map((family) => (
            <Fragment key={family.routeFamily}>
              <Group
                justify="space-between"
                wrap="nowrap"
                px="md"
                py="sm"
                data-testid="resource-row"
                style={{ borderTop: "1px solid var(--mantine-color-gray-3)" }}
              >
                <div>
                  <Group gap="xs">
                    <Text size="sm" fw={500}>
                      {family.name}
                    </Text>
                    <Text size="xs" c="dimmed" ff="monospace">
                      {family.routeFamily}
                    </Text>
                    {family.decision === "confirmed" ? (
                      <Badge color="green" size="sm" data-testid="resource-confirmed-badge">
                        подтверждён
                      </Badge>
                    ) : family.decision === "declined" ? (
                      <Badge color="gray" size="sm" data-testid="resource-declined-badge">
                        отклонён
                      </Badge>
                    ) : null}
                  </Group>
                  {family.decision === "confirmed" ? (
                    <Stack gap={2} mt={4}>
                      <Text size="xs" c="dimmed" data-testid="resource-entity-count">
                        Записей: {family.entityCount}
                        {family.byBaseScope !== null && family.byBaseScope.length > 0
                          ? ` (${family.byBaseScope.map((b) => `${b.baseScope}: ${b.entityCount}`).join(", ")})`
                          : ""}
                      </Text>
                      <Text
                        size="xs"
                        c="dimmed"
                        ff="monospace"
                        data-testid="resource-collection-url"
                      >
                        GET {workspaceUrl}
                        {family.routeFamily}{" "}
                        <CopyButton value={`${workspaceUrl}${family.routeFamily}`}>
                          {({ copied, copy }) => (
                            <Anchor
                              size="xs"
                              component="button"
                              type="button"
                              onClick={copy}
                              data-testid="resource-collection-url-copy"
                            >
                              {copied ? "скопировано" : "копировать"}
                            </Anchor>
                          )}
                        </CopyButton>
                      </Text>
                      {nestedHint(family.routeFamily) !== null ? (
                        <Text size="xs" c="dimmed" data-testid="resource-nested-hint">
                          {nestedHint(family.routeFamily)}
                        </Text>
                      ) : null}
                      {family.writeForm === null ? (
                        <Text size="xs" c="dimmed" data-testid="resource-no-write-form">
                          форма создания не распознана — POST идёт как раньше, из генератора
                        </Text>
                      ) : null}
                    </Stack>
                  ) : null}
                </div>
                <Group gap="xs" wrap="nowrap">
                  {family.decision === "confirmed" ? (
                    <Button
                      variant="default"
                      size="xs"
                      leftSection={<IconList size={16} />}
                      onClick={() =>
                        setOpenFamily(openFamily === family.routeFamily ? null : family.routeFamily)
                      }
                      data-testid="resource-entities-toggle"
                    >
                      {openFamily === family.routeFamily ? "Свернуть" : "Записи"}
                    </Button>
                  ) : (
                    <Button
                      variant="default"
                      size="xs"
                      leftSection={<IconCheck size={16} />}
                      onClick={() => handleConfirm(family)}
                      loading={decideResource.isPending}
                      data-testid="resource-confirm"
                    >
                      Подтвердить
                    </Button>
                  )}
                  <Button
                    variant="default"
                    size="xs"
                    color="red"
                    leftSection={<IconX size={16} />}
                    onClick={() => handleDecline(family)}
                    loading={decideResource.isPending}
                    data-testid="resource-decline"
                  >
                    Отклонить
                  </Button>
                </Group>
              </Group>
              {openFamily === family.routeFamily && family.decision === "confirmed" ? (
                <Box px="md" pb="sm">
                  <ResourceEntities id={id} family={family} />
                </Box>
              ) : null}
            </Fragment>
          ))}
        </Stack>
      </Card>
    </Stack>
  );
}

// DeclineConfirmedForm is its own component, not inline JSX in handleDecline,
// for the same reason ScenariosPage's CloneScenarioForm/RenameScenarioForm
// are: it needs a mutation instance of its own, separate from the row list's
// shared one, so this modal's pending/error state does not tangle with
// whichever OTHER row's confirm/decline is in flight at the same time. Plain
// useState rather than react-hook-form + arktype: the one field is a slug the
// server compares for exact equality, there is no local shape to validate
// beyond "non-empty", and the refusal that matters (confirm_slug_mismatch) can
// only ever come from the server.
// Exported since A20 for DriftPanel.tsx, whose orphaned-resource row is the
// same verb over the same slug; the prop is a Pick so a drift row (name and
// routeFamily, no decision state) can be handed in as is.
export function DeclineConfirmedForm({
  id,
  family,
  onCancel,
  onDeclined,
}: {
  id: number;
  family: Pick<ResourceFamilyView, "name" | "routeFamily">;
  onCancel: () => void;
  onDeclined: () => void;
}): ReactElement {
  const [slug, setSlug] = useState("");

  const decideResource = useDecideResource({
    mutation: {
      onSuccess: (res) => {
        if (res.status === 200) {
          onDeclined();
        }
      },
    },
  });

  function handleSubmit(): void {
    decideResource.mutate({
      id,
      data: { routeFamily: family.routeFamily, state: "declined", confirmSlug: slug.trim() },
    });
  }

  return (
    <Stack gap="sm">
      <Text size="sm">
        Само по себе необратимо: будут удалены и запись ресурса «{family.name}», и все его сущности,
        а отклонение не сохраняет свою собственную точку отката. Вернуть их можно только откатом на
        чекпойнт, снятый до отклонения — и то лишь если тот чекпойнт сохранил данные ресурсов. Чтобы
        подтвердить, введите слаг воркспейса.
      </Text>
      {decideResource.isError ? (
        <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
          {describeApiFailureDetailed(decideResource.error)}
        </Alert>
      ) : null}
      <TextInput
        label="Слаг воркспейса"
        data-testid="resource-decline-slug"
        value={slug}
        onChange={(event) => setSlug(event.currentTarget.value)}
      />
      <Group justify="flex-end">
        <Button type="button" variant="default" onClick={onCancel}>
          Отмена
        </Button>
        <Button
          type="button"
          color="red"
          loading={decideResource.isPending}
          onClick={handleSubmit}
          data-testid="resource-decline-submit"
        >
          Отклонить
        </Button>
      </Group>
    </Stack>
  );
}
