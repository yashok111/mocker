import type { ReactElement } from "react";
import { Stack, Text } from "@mantine/core";
import { useGetScenario } from "@/api/generated/scenarios/scenarios.ts";
import { describeApiFailure } from "@/api/errors";

// ScenarioDetails reads the snapshot (GET .../scenarios/{sid}, the same
// route the mask banner on «Операции спеки» reads) and lists what activating it
// would change: the operations it overrides and the settings it carries.
// A viewer only — CARVE-OUTS.md refuses the EDITOR (a scenario's contents
// come from snapshotting the workspace), not a look inside.
export function ScenarioDetails({
  id,
  scenarioId,
}: {
  id: number;
  scenarioId: number;
}): ReactElement {
  const scenario = useGetScenario(id, scenarioId);
  if (scenario.isPending) {
    return (
      <Text size="xs" c="dimmed" component="output">
        Загрузка…
      </Text>
    );
  }
  if (scenario.isError || scenario.data.status !== 200) {
    return (
      <Text size="xs" c="red" data-testid="scenario-details-error">
        {describeApiFailure(scenario.isError ? scenario.error : null)}
      </Text>
    );
  }
  const sc = scenario.data.data;
  const s = sc.settings;
  return (
    <Stack gap={4} mt={4} data-testid="scenario-details">
      <Text size="xs" c="dimmed">
        Спека снимка: {sc.spec.name} · базовый путь {sc.basePath === "" ? "/" : sc.basePath} · seed{" "}
        {s.seed} · размер списков {s.listSize} · задержка {s.delayMs} мс · личность{" "}
        {s.identity.name}
        {s.envelope ? ` · конверт ${s.envelope}` : ""}
      </Text>
      {sc.overrides.length === 0 ? (
        <Text size="xs" c="dimmed" data-testid="scenario-details-empty">
          Правок операций в снимке нет — сценарий меняет только настройки.
        </Text>
      ) : (
        <Stack gap={2}>
          <Text size="xs" c="dimmed">
            Операции, которые сценарий переопределяет ({sc.overrides.length}):
          </Text>
          {sc.overrides.map((ov) => (
            <Text
              key={`${ov.method} ${ov.path}`}
              size="xs"
              ff="monospace"
              data-testid="scenario-details-override"
            >
              {ov.method} {ov.path}
              {ov.activeStatus !== undefined ? ` → ${ov.activeStatus}` : ""}
              {Object.keys(ov.responses).length > 0
                ? ` (статусы: ${Object.keys(ov.responses).join(", ")})`
                : ""}
              {ov.routeOff ? " · маршрут выключен" : ""}
              {!ov.overrideOn ? " · правка отключена" : ""}
            </Text>
          ))}
        </Stack>
      )}
    </Stack>
  );
}
