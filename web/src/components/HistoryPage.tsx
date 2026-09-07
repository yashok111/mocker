import type { ReactElement } from "react";
import { Alert, Stack, Text, Title } from "@mantine/core";
import { IconAlertTriangle } from "@tabler/icons-react";
import { useListCheckpoints } from "@/api/generated/checkpoints/checkpoints.ts";
import { useGetWorkspace } from "@/api/generated/workspaces/workspaces.ts";
import { describeApiFailure } from "@/api/errors";
import { QueryState } from "./QueryState";
import { CheckpointList } from "./history/CheckpointList";
import { CreateCheckpointForm } from "./history/CreateCheckpointForm";
import { ResetDataCard } from "./history/ResetDataCard";
import { ResetOverridesCard } from "./history/ResetOverridesCard";

// HistoryPage is DESIGN §14 screen 10, P2c: the workspace's undo log. A
// checkpoint is a point-in-time snapshot of the WORKSPACE layer only —
// settings, op_overrides, custom_endpoints — never the scenario layer
// composed on top of it at request time (bundle.New always hard-codes an
// empty endpoints slice on a SCENARIO snapshot; a checkpoint is the one
// producer that fills it in afterward, C2/C3 of the P2c context). That is
// why every warning below about a scenario "masking" restored state says
// masking, not loss: nothing this screen does can touch what a scenario
// itself serves (mockplane/scenario.go:113-116 — a key the scenario names
// keeps the scenario's answer, before and after any of these three calls).
//
// Four actions, four different blast radii:
//   - «сохранить точку» never destroys anything and never bumps revision
//     (C12) — pure bookkeeping, no confirmation needed for the act itself.
//   - «откатить» and «сбросить всё к спеке» are genuinely destructive.
//     Both write their OWN pre-destructive checkpoint, server-side, in the
//     same transaction as the destruction (C5/C9/C10) — but that safety net
//     is not what the confirmation copy leads with, because the button's own
//     label already promises "reversible". What a person cannot see coming
//     from the button text is a relocated basePath, an invalidated signing
//     key, or a dropped auth preset — so those are what get named.
//   - «удалить» (P2d, SIG-DELCP) removes one history row outright and writes
//     NO safety-net checkpoint of its own — unlike rollback and reset there
//     is nothing left to undo it with afterward, so unlike those two the
//     confirmation copy leads with exactly that: this one has no undo.
//
//
// The four cards and the row list this screen is assembled from live in
// ./history/ since the A21 structural pass: the file was 815 lines with all
// of them inlined, and the orchestration above — which query answers which
// card — was the part a reader had to scroll past them to find. Nothing
// moved but the code; every comment travelled with the function it explains.
//
// The outermost element carries data-testid="history-page" OUTSIDE every
// state switch below (§I of the P2c context, obs 17): a marker only on the
// success branch would make this screen's reachability depend on whether
// routes.test.tsx happened to mock every query it fires — exactly the gap
// obs 17 exists to close.
export function HistoryPage({ id }: { id: number }): ReactElement {
  const checkpoints = useListCheckpoints(id);
  const list = checkpoints.data?.status === 200 ? checkpoints.data.data.checkpoints : [];

  // Read a SECOND time here rather than threaded down as a prop from
  // WorkspaceLayout: OperationsPage's own A18 banner already reads
  // workspace.scenarioId the same way, for the same reason — it is the one
  // authority on whether a scenario is active, and every screen that needs
  // that fact reads it off this query rather than risk disagreeing with each
  // other about it. staleTime is 30s in production, so by the time this
  // screen mounts under WorkspaceLayout (which already fetched it to render
  // {children} at all) this is a cache hit, not a second round trip.
  const workspace = useGetWorkspace(id);
  const scenarioActive = workspace.data?.status === 200 && workspace.data.data.scenarioId !== null;
  const workspaceSlug = workspace.data?.status === 200 ? workspace.data.data.slug : "";

  return (
    <div data-testid="history-page">
      <Stack gap="md">
        <Title order={1}>История</Title>
        <Text size="sm" c="dimmed" data-testid="history-intro">
          Чекпойнт — снимок слоя воркспейса: настройки, правки операций, свои эндпоинты и
          подтверждённые ресурсы. При откате можно вернуть и сами записи ресурсов — флажком «вернуть
          и данные ресурсов», если эта точка их сохранила. Откат и сброс правок сохраняют свою
          собственную точку прямо перед тем, как что-то стереть, так что их можно отменить откатом
          на неё. Сброс ДАННЫХ ресурсов — нет: он необратим.
        </Text>
        <CreateCheckpointForm id={id} scenarioActive={scenarioActive} />
        <ResetOverridesCard id={id} scenarioActive={scenarioActive} />
        <ResetDataCard id={id} workspaceSlug={workspaceSlug} />
        <QueryState queries={[checkpoints]} testIdPrefix="history">
          {checkpoints.data?.status !== 200 ? (
            // An unexpected status is not the query failing — no retry
            // button, because a second try answers the same way.
            <Alert
              color="red"
              icon={<IconAlertTriangle size={18} />}
              role="alert"
              data-testid="history-error"
            >
              {describeApiFailure(null)}
            </Alert>
          ) : list.length === 0 ? (
            <Text data-testid="history-empty">
              Чекпойнтов пока нет. Сохраните первую точку формой выше — либо сделайте откат или
              сброс: оба тоже пишут точку перед тем, как что-то менять.
            </Text>
          ) : (
            <CheckpointList
              id={id}
              checkpoints={list}
              scenarioActive={scenarioActive}
              workspaceSlug={workspaceSlug}
            />
          )}
        </QueryState>
      </Stack>
    </div>
  );
}

// A21 REVERSES this file's own "a fourth copy is cheaper than a shared util"
// note: a fifth, undocumented copy had appeared inline in AssetsPage.tsx,
// which is exactly the threshold that argument was reasoning about, so
// formatTimestamp moved to @/format and all five call sites now import it —
// this screen's is history/CheckpointList.tsx since the split.
