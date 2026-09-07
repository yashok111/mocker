import type { ReactElement } from "react";
import { Alert, Stack, Text, Title } from "@mantine/core";
import { IconAlertTriangle } from "@tabler/icons-react";
import { useListScenarios } from "@/api/generated/scenarios/scenarios.ts";
import { describeApiFailure } from "@/api/errors";
import { TabLink } from "./TabLink";
import { QueryState } from "./QueryState";
import { CreateScenarioForm } from "./scenarios/CreateScenarioForm";
import { ScenarioList } from "./scenarios/ScenarioList";

// ScenariosPage is DESIGN §14 screen 9, P2b: a named snapshot of the
// workspace's SETTINGS and op_overrides rows, composed at request time OVER
// the workspace's own layer (never restored over it — §0/§A1 of the P2b
// context) and switched on with one click. There is no scenario EDITOR by
// design: the only way to produce a snapshot's CONTENTS is to put the
// workspace into the state it should capture and save it from there — P2d's
// clone and rename do not relax that: clone copies an existing snapshot's
// bytes verbatim under a new name, and rename touches only the `name`
// column, never `snapshot`. That is why this screen offers seven actions —
// list, save-from-current-state, clone, rename, activate, deactivate,
// delete — and still nothing that edits a scenario's own contents.
//
// The save button says «сохранить настройки и правки операций», never
// «сохранить текущее состояние»: DESIGN §14:905 wrote the latter before the
// bundle format had a shape, and a scenario does not carry the custom
// endpoints the «Свои эндпоинты» tab shows (§0 — endpoints have no place in a
// snapshot keyed by op_overrides rows) — a button promising «состояние»
// would promise those too.
//
// The outermost element carries data-testid="scenarios-page" OUTSIDE every
// state switch below (§I): a marker only on the success branch would make
// this screen's reachability depend on whether routes.test.tsx happened to
// mock every query it fires.
//
// The create form, the row list and the three per-row forms live in
// ./scenarios/ since the A21 structural pass: this file was 813 lines and
// the orchestration — one query, one empty state, one list — was buried
// under them. Nothing moved but the code.
export function ScenariosPage({ id }: { id: number }): ReactElement {
  const scenarios = useListScenarios(id);
  const list = scenarios.data?.status === 200 ? scenarios.data.data.scenarios : [];
  const activeScenario = list.find((sc) => sc.isActive);

  return (
    <div data-testid="scenarios-page">
      <Stack gap="md">
        <Title order={1}>Сценарии</Title>
        <Text size="sm" c="dimmed">
          Сценарий — именованный снимок настроек и правок операций воркспейса, который подставляется
          поверх собственного слоя воркспейса при каждом запросе, а не переписывает его: деактивация
          возвращает всё как было. Свои эндпоинты сценарий не захватывает — редактируются они, как
          обычно, на вкладке{" "}
          <TabLink id={id} tab="endpoints" testId="scenarios-endpoints-link">
            «Свои эндпоинты»
          </TabLink>
          .
        </Text>
        <CreateScenarioForm id={id} activeScenario={activeScenario} />
        <QueryState queries={[scenarios]} testIdPrefix="scenarios">
          {scenarios.data?.status !== 200 ? (
            // An unexpected status is not the query failing — no retry
            // button, because a second try answers the same way.
            <Alert
              color="red"
              icon={<IconAlertTriangle size={18} />}
              role="alert"
              data-testid="scenarios-error"
            >
              {describeApiFailure(null)}
            </Alert>
          ) : list.length === 0 ? (
            <Text data-testid="scenarios-empty">
              Сценариев пока нет. Настройте воркспейс и правки операций как нужно и сохраните первый
              сценарий формой выше.
            </Text>
          ) : (
            <ScenarioList id={id} scenarios={list} />
          )}
        </QueryState>
      </Stack>
    </div>
  );
}
