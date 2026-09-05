import { useEffect, useMemo, useRef, useState } from "react";
import type { ReactElement } from "react";
import {
  Alert,
  Anchor,
  Badge,
  Button,
  Group,
  Loader,
  ScrollArea,
  Stack,
  Text,
  TextInput,
  Title,
  UnstyledButton,
} from "@mantine/core";
import { IconAlertTriangle, IconSearch } from "@tabler/icons-react";
import { Link } from "@tanstack/react-router";
import { useGetWorkspace } from "@/api/generated/workspaces/workspaces.ts";
import { useListWorkspaceOperations } from "@/api/generated/operations/operations.ts";
import { useListSpecOperations } from "@/api/generated/specs/specs.ts";
import { useGetScenario } from "@/api/generated/scenarios/scenarios.ts";
import type { MergedOperationView, MergedStatusView, OperationView } from "@/api/generated/schemas";
import { modals } from "@mantine/modals";
import { TabLink } from "./TabLink";
import { describeApiFailure } from "@/api/errors";
import { OperationEditor } from "./OperationEditor";
import { SessionControls } from "./SessionControls";

// OperationsPage is DESIGN §14 screen 5, P1 subset: the merged operation
// tree (grouped by tag, searchable), the per-operation override editor, and
// the session-directive buttons that live on the same screen. §3.3 of the
// phase context: MergedOperationView carries no tag/summary of its own — that
// lives on OperationView from the spec's own operations list — so the tree
// this screen shows is a join of two independent queries on method+path.

// A real customer spec caps out around 130 operations (§3.2); this is the
// server's own hard limit, kept here as one named constant instead of a
// magic 500 repeated at the call site and the "capped" notice below.
const SPEC_OPERATIONS_LIMIT = 500;
const UNTAGGED = "без тега";

interface SelectedOperation {
  method: string;
  path: string;
  opKey: string;
  statuses: MergedStatusView[];
}

function joinKey(method: string, path: string): string {
  return `${method} ${path}`;
}

// signature composes the line DESIGN §14 asks for ("сохранено: закреплённый
// 409, 3 значения на 200") so that picking a different operation, or an
// override losing then regaining a status, never reads as silently losing
// work — the whole point of showing it at all.
function signature(op: MergedOperationView): string {
  const ov = op.override;
  if (ov === undefined) {
    return "без переопределений — ответ строится по спеке";
  }
  const bits: string[] = [];
  if (ov.routeOff) {
    bits.push("операция выключена");
  } else if (ov.overrideOn) {
    bits.push("переопределение включено");
  } else {
    bits.push("переопределение выключено");
  }
  if (ov.activeStatus !== undefined) {
    bits.push(`активный статус ${ov.activeStatus}`);
  }
  const perStatus = Object.entries(ov.responses)
    .map(
      ([status, r]) =>
        `${status}: ${r.mode === "pinned" ? "закреплённый" : "сгенерированный"}${
          r.recipeCount > 0 ? `, ${r.recipeCount} значения` : ""
        }`,
    )
    .join("; ");
  if (perStatus !== "") {
    bits.push(perStatus);
  }
  return bits.join(" · ");
}

export function OperationsPage({
  id,
  initialOpKey,
  initialOpId,
}: {
  id: number;
  /** P7b: the opKey the «Контракт» tab linked here with; selected once the
   * list has loaded, and only while nothing else is selected yet. */
  initialOpKey?: string;
  /** A21 (U4): the spec operation id a traffic row linked here with
   * (TrafficRow.matchedId for kind "operation"); resolved to an opKey
   * through the spec operations list, then selected the same way. */
  initialOpId?: number;
}): ReactElement {
  const workspace = useGetWorkspace(id);
  // specId is only known once the workspace itself loaded; null both before
  // that and when the workspace genuinely has no spec attached — the two are
  // told apart by the pending/error checks below running first.
  const specId = workspace.data?.status === 200 ? workspace.data.data.specId : null;

  const mergedOps = useListWorkspaceOperations(id);
  // enabled: specId !== null overrides the hook's own default (id !== null &&
  // id !== undefined) — without it a workspace with no spec would send
  // GET /api/specs/0/operations and get a 404 nobody asked for.
  const specOps = useListSpecOperations(
    specId ?? 0,
    { limit: SPEC_OPERATIONS_LIMIT },
    { query: { enabled: specId !== null } },
  );

  const [search, setSearch] = useState("");
  const [selected, setSelected] = useState<SelectedOperation | null>(null);
  // A21 (U9): the editor reports an unsaved draft; switching operations
  // remounts it (key={opKey}), so the switch asks first.
  const [editorDirty, setEditorDirty] = useState(false);
  function select(next: SelectedOperation): void {
    if (!editorDirty || next.opKey === selected?.opKey) {
      setSelected(next);
      return;
    }
    modals.openConfirmModal({
      title: "Несохранённые изменения",
      children: (
        <Text size="sm">
          У операции {selected?.method} {selected?.path} есть несохранённые изменения. Перейти к
          другой и потерять их?
        </Text>
      ),
      labels: { confirm: "Перейти", cancel: "Остаться" },
      confirmProps: { color: "red", "data-testid": "operations-discard-confirm" },
      onConfirm: () => setSelected(next),
    });
  }

  // Each query is only "pending"/"errored" for real when it is actually in
  // play — specOps stays technically isPending forever while disabled
  // (specId === null), so it is excluded from the combined check in that
  // case rather than blocking the page on a request that will never fire.
  const isPending =
    workspace.isPending || mergedOps.isPending || (specId !== null && specOps.isPending);
  const isError = workspace.isError || mergedOps.isError || (specId !== null && specOps.isError);
  const firstError: unknown = workspace.isError
    ? workspace.error
    : mergedOps.isError
      ? mergedOps.error
      : specOps.error;
  const badStatus =
    (workspace.data !== undefined && workspace.data.status !== 200) ||
    (mergedOps.data !== undefined && mergedOps.data.status !== 200) ||
    (specId !== null && specOps.data !== undefined && specOps.data.status !== 200);

  const specByKey = useMemo(() => {
    const map = new Map<string, OperationView>();
    if (specOps.data?.status === 200) {
      for (const o of specOps.data.data) {
        map.set(joinKey(o.method, o.path), o);
      }
    }
    return map;
  }, [specOps.data]);

  const mergedList = mergedOps.data?.status === 200 ? mergedOps.data.data : [];
  // P7b: the «Контракт» tab links here with an opKey; select it ONCE the
  // list has loaded. Keyed on the query's own data (a stable reference per
  // fetch) and guarded by a ref, so the effect neither depends on a
  // per-render array nor re-selects after the operator moved on.
  const initialApplied = useRef(false);
  useEffect(() => {
    if (
      (initialOpKey === undefined && initialOpId === undefined) ||
      initialApplied.current ||
      mergedOps.data?.status !== 200
    ) {
      return;
    }
    // An id resolves through the spec operations (method + path) to the
    // merged row; the spec list may still be loading, in which case the
    // effect runs again when it lands (it is in the deps below).
    const byId =
      initialOpId !== undefined && specOps.data?.status === 200
        ? specOps.data.data.find((op) => op.id === initialOpId)
        : undefined;
    const linked = mergedOps.data.data.find((op) =>
      initialOpKey !== undefined
        ? op.opKey === initialOpKey
        : byId !== undefined && op.method === byId.method && op.path === byId.path,
    );
    if (linked !== undefined) {
      initialApplied.current = true;
      setSelected({
        method: linked.method,
        path: linked.path,
        opKey: linked.opKey,
        statuses: linked.statuses,
      });
    }
  }, [initialOpKey, initialOpId, mergedOps.data, specOps.data]);
  const needle = search.trim().toLowerCase();
  const filtered =
    needle === ""
      ? mergedList
      : mergedList.filter((op) => {
          const spec = specByKey.get(joinKey(op.method, op.path));
          const haystack = [op.method, op.path, spec?.summary ?? "", spec?.operationId ?? ""]
            .join(" ")
            .toLowerCase();
          return haystack.includes(needle);
        });

  const groups = new Map<string, MergedOperationView[]>();
  for (const op of filtered) {
    const spec = specByKey.get(joinKey(op.method, op.path));
    const tag = spec?.tag ?? UNTAGGED;
    const bucket = groups.get(tag);
    if (bucket) {
      bucket.push(op);
    } else {
      groups.set(tag, [op]);
    }
  }

  const capped = specOps.data?.status === 200 && specOps.data.data.length === SPEC_OPERATIONS_LIMIT;

  function retry(): void {
    void workspace.refetch();
    void mergedOps.refetch();
    if (specId !== null) {
      void specOps.refetch();
    }
  }

  // A18: the scenario id, when the workspace has one active, comes off the
  // SAME query used above — the workspace is the one authority on which
  // scenario (if any) is currently overlaid, and reading it a second way
  // here would risk disagreeing with the rest of the screen about whether
  // one is active at all.
  const activeScenarioId = workspace.data?.status === 200 ? workspace.data.data.scenarioId : null;

  return (
    <Stack gap="md" data-testid="operations-page">
      <Title order={1}>Операции спеки</Title>
      {activeScenarioId !== null ? (
        <ScenarioMaskBanner workspaceId={id} scenarioId={activeScenarioId} />
      ) : null}
      {isPending ? (
        <Group gap="xs">
          <Loader size="sm" />
          <Text size="sm" component="output">
            Загрузка…
          </Text>
        </Group>
      ) : isError ? (
        <Stack gap="sm" data-testid="operations-error">
          <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
            {describeApiFailure(firstError)}
          </Alert>
          <Button variant="default" w="fit-content" onClick={retry} data-testid="operations-retry">
            Повторить
          </Button>
        </Stack>
      ) : badStatus ? (
        <Alert
          color="red"
          icon={<IconAlertTriangle size={18} />}
          role="alert"
          data-testid="operations-error"
        >
          {describeApiFailure(null)}
        </Alert>
      ) : specId === null ? (
        // A workspace with no spec has no operations to show — this is an
        // ordinary, expected state (§3.3), not an error.
        <Text data-testid="operations-empty" c="dimmed">
          У воркспейса нет привязанной спеки — переопределять пока нечего.{" "}
          <Anchor component={Link} to="/specs">
            Загрузите спеку
          </Anchor>{" "}
          и привяжите её в настройках воркспейса.
        </Text>
      ) : (
        <Group align="flex-start" gap="lg" wrap="nowrap">
          <Stack w={340} gap="sm">
            <TextInput
              placeholder="Метод, путь, тег, описание…"
              value={search}
              onChange={(e) => setSearch(e.currentTarget.value)}
              leftSection={<IconSearch size={14} />}
              data-testid="operations-search"
            />
            {capped ? (
              <Text size="xs" c="dimmed" data-testid="operations-capped-note">
                Спека отдала первые {SPEC_OPERATIONS_LIMIT} операций: теги и описания для операций
                за этой границей могут не подтянуться.
              </Text>
            ) : null}
            <ScrollArea h={560} data-testid="operation-list">
              <Stack gap="md">
                {groups.size === 0 ? (
                  <Text size="sm" c="dimmed">
                    Ничего не найдено
                  </Text>
                ) : (
                  [...groups.entries()].map(([tag, ops]) => (
                    <div key={tag}>
                      <Text fw={600} size="sm">
                        {tag}
                      </Text>
                      <Stack gap={4} mt={4}>
                        {ops.map((op) => (
                          <UnstyledButton
                            key={op.opKey}
                            data-testid="operation-row"
                            onClick={() =>
                              select({
                                method: op.method,
                                path: op.path,
                                opKey: op.opKey,
                                statuses: op.statuses,
                              })
                            }
                            px="xs"
                            py={4}
                            style={{
                              borderRadius: 4,
                              background:
                                selected?.opKey === op.opKey
                                  ? "var(--mantine-color-blue-light)"
                                  : undefined,
                            }}
                          >
                            <Group gap="xs" wrap="nowrap">
                              <Badge size="sm" variant="light">
                                {op.method}
                              </Badge>
                              <Text size="sm" fw={500} style={{ wordBreak: "break-all" }}>
                                {op.path}
                              </Text>
                            </Group>
                            <Text size="xs" c="dimmed">
                              {signature(op)}
                            </Text>
                          </UnstyledButton>
                        ))}
                      </Stack>
                    </div>
                  ))
                )}
              </Stack>
            </ScrollArea>
          </Stack>
          <div style={{ flex: 1, minWidth: 0 }}>
            {selected ? (
              <OperationEditor
                key={selected.opKey}
                onDirtyChange={setEditorDirty}
                workspaceId={id}
                opKey={selected.opKey}
                statuses={selected.statuses}
                path={selected.path}
                basePath={
                  workspace.data?.status === 200 ? workspace.data.data.settings.basePath : ""
                }
              />
            ) : (
              <Text c="dimmed">Выберите операцию слева</Text>
            )}
          </div>
        </Group>
      )}
      <SessionControls
        id={id}
        target={selected ? { method: selected.method, path: selected.path } : null}
      />
    </Stack>
  );
}

// ScenarioMaskBanner is A18's own screen: an edit made through OperationEditor
// while a scenario is active still writes to the WORKSPACE layer (this page
// never stops calling PUT .../operations/{opKey} just because a scenario is
// running), but under A1's per-key overlay that edit is invisible for any
// operation the active scenario's own snapshot already covers — the
// scenario's row for that key wins regardless of what this screen shows.
// Silently accepting such an edit would be the same class of lie as a switch
// that does nothing, so this banner names exactly which operations are
// currently masked.
//
// It reads GET .../scenarios/{sid} — the snapshot itself — and DELIBERATELY
// NOT GET .../operations/{opKey}: that route reports the workspace's row
// straight from the repo and is never composed with the active scenario
// (A18's own text, and the fact this run's decisive live observation #2
// depends on it staying that way). Composing it here to save this banner an
// extra request would make that observation vacuous.
function ScenarioMaskBanner({
  workspaceId,
  scenarioId,
}: {
  workspaceId: number;
  scenarioId: number;
}): ReactElement | null {
  const scenario = useGetScenario(workspaceId, scenarioId);
  if (scenario.data?.status !== 200) {
    // Advisory only: a slow or failed fetch here must not block the rest of
    // the screen (the editor itself works fine either way), and saying
    // nothing is honest where a stale "no scenario active" claim would not
    // be — this banner appears at all only once it has a real snapshot to
    // read the masked keys from.
    return null;
  }
  const sc = scenario.data.data;
  const maskedKeys = sc.overrides.map((ov) => `${ov.method} ${ov.path}`);

  return (
    <Alert
      color="yellow"
      icon={<IconAlertTriangle size={18} />}
      data-testid="scenario-active-banner"
    >
      <Text size="sm">
        Активен сценарий «{sc.name}». Правки на этой странице по-прежнему сохраняются в слой
        воркспейса, но пока сценарий активен, следующие операции отвечают ТАК, КАК ИХ ЗАДАЁТ
        СЦЕНАРИЙ, а не так, как задано здесь — деактивируйте сценарий на вкладке{" "}
        <TabLink id={workspaceId} tab="scenarios" testId="scenario-mask-link">
          «Сценарии»
        </TabLink>
        , чтобы увидеть эффект правки:
      </Text>
      {maskedKeys.length === 0 ? (
        <Text size="xs" c="dimmed">
          Сценарий не переопределяет ни одной операции — маскирует только настройки воркспейса
          (seed, listSize, nullRate, identity, auth, delayMs, envelope).
        </Text>
      ) : (
        <Text size="xs" c="dimmed" data-testid="scenario-masked-keys">
          {maskedKeys.join(", ")}
        </Text>
      )}
    </Alert>
  );
}
