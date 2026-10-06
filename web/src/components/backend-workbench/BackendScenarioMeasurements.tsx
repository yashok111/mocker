import { useObservationScope } from "./BackendObservationContext";
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Alert,
  Button,
  Code,
  Group,
  NativeSelect,
  Stack,
  Table,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import type {
  BackendObservationAnalysisPin,
  BackendScenarioMeasurements as MeasurementReport,
  BackendDiagramScope,
  ObservationVersion,
  ObservationCorrelationSnapshot,
  BackendAnalysisStartMeasurement,
} from "@/api/generated/schemas";
import { getBackendAnalysisResults } from "@/api/generated/backend-projects/backend-projects";
import { readAnalysis, analysisTerminal } from "./backendAnalysisReads";
import { makeAnalysisAttempt, useBackendAnalysisRecovery } from "./backendAnalysisRecovery";
import { BackendDiagramObservations } from "./BackendDiagramObservations";
export function MeasurementValues({ report }: { report: MeasurementReport }) {
  return (
    <Stack>
      <Table captionSide="top">
        <Table.Caption>Observed metrics · {report.input.side}</Table.Caption>
        <Table.Thead>
          <Table.Tr>
            <Table.Th>Метрика</Table.Th>
            <Table.Th>Значение</Table.Th>
            <Table.Th>p95</Table.Th>
            <Table.Th>Samples / missing</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {Object.entries(report.metrics).map(([name, m]) => (
            <Table.Tr key={name}>
              <Table.Td>
                {name} ({m.unit})
              </Table.Td>
              <Table.Td>{m.value ?? "Неизвестно"}</Table.Td>
              <Table.Td>{m.p95 ?? "—"}</Table.Td>
              <Table.Td>
                {m.sampleCount} / {m.missingSamples}
                <Text size="xs">{m.limitations.join("; ")}</Text>
              </Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
      <Text>
        Sampled executions: {report.sampledExecutions}; failed: {report.failedExecutions}; skipped:{" "}
        {report.skippedExecutions}; unknown: {report.unknownExecutions}. Это не population error
        rate.
      </Text>
      <details>
        <summary>Условия и исходные samples</summary>
        <Code block>
          {JSON.stringify({ conditions: report.conditions, metrics: report.metrics }, null, 2)}
        </Code>
      </details>
    </Stack>
  );
}
export function BackendScenarioMeasurements({
  projectId,
  selected,
  correlation,
  scope,
}: {
  projectId: string;
  selected?: ObservationVersion;
  correlation?: ObservationCorrelationSnapshot;
  scope?: BackendDiagramScope;
}) {
  const observationContext = useObservationScope();
  const recovery = useBackendAnalysisRecovery(projectId);
  const [pins, setPins] = useState<{ pin: BackendObservationAnalysisPin; revision: string }[]>([]);
  const [executions, setExecutions] = useState("");
  const [afterExecutions, setAfterExecutions] = useState("");
  const [roots, setRoots] = useState("");
  const [afterRoots, setAfterRoots] = useState("");
  const [basis, setBasis] = useState("spans");
  const [provider, setProvider] = useState("");
  const [metricScope, setMetricScope] = useState("");
  const [jobId, setJobId] = useState("");
  const [version, setVersion] = useState("");
  const [section, setSection] = useState<"measurements" | "comparisons" | "observed_sequences">(
    "measurements",
  );
  const [error, setError] = useState("");
  const detail = useQuery({
    queryKey: ["measurement-job", projectId, jobId],
    enabled: !!jobId,
    queryFn: ({ signal }) => readAnalysis(projectId, jobId, signal),
    retry: false,
    refetchInterval: (q) =>
      q.state.data && !analysisTerminal(q.state.data.job.status) ? 2000 : false,
  });
  const results = useQuery({
    queryKey: ["measurement-results", projectId, jobId, version, section],
    enabled: !!jobId && Number.isSafeInteger(Number(version)) && Number(version) > 0,
    queryFn: async ({ signal }) => {
      const r = await getBackendAnalysisResults(
        projectId,
        jobId,
        {
          resultVersion: Number(version),
          section: section === "observed_sequences" ? "witnesses" : "checks",
          kind:
            section === "observed_sequences"
              ? "observed_sequence"
              : section === "comparisons"
                ? "scenario_comparison"
                : "scenario_measurement",
          limit: 100,
        },
        { signal },
      );
      if (r.status !== 200) throw new Error("Result unavailable");
      return r.data;
    },
    retry: false,
  });
  const ids = (v: string) =>
    v
      .split(",")
      .map((x) => x.trim())
      .filter(Boolean);
  const ready =
    !!selected &&
    !!correlation &&
    selected.setId === correlation.input.observation.setId &&
    selected.version === correlation.input.observation.version &&
    selected.contentHash === correlation.input.observation.contentHash;
  function add(side: "before" | "after") {
    if (!ready || !selected || !correlation) return;
    const pin = {
      observationSetId: selected.setId,
      version: selected.version,
      contentHash: selected.contentHash,
      correlationVersion: correlation.version,
      correlationHash: correlation.contentHash,
      side,
    };
    setPins((old) => [
      ...old.filter(
        (x) =>
          !(
            x.pin.side === side &&
            x.pin.observationSetId === pin.observationSetId &&
            x.pin.version === pin.version
          ),
      ),
      { pin, revision: correlation.input.revisionId },
    ]);
  }
  async function start() {
    try {
      const before = pins.filter((p) => p.pin.side === "before"),
        after = pins.filter((p) => p.pin.side === "after");
      if (
        !before.length ||
        new Set(before.map((p) => p.revision)).size !== 1 ||
        new Set(after.map((p) => p.revision)).size > 1
      )
        throw new Error("Нужна одна точная source revision на каждой стороне");
      const metricBasis =
        basis === "spans"
          ? { kind: "spans" as const }
          : { kind: "measurements" as const, basis: provider, scope: metricScope };
      const input: BackendAnalysisStartMeasurement = {
        kind: after.length ? "scenario_comparison" : "scenario_measurement",
        beforeRevisionId: before[0]!.revision,
        ...(after.length ? { afterRevisionId: after[0]!.revision } : {}),
        observationPins: pins.map((x) => x.pin),
        measurements: [
          {
            side: "before",
            executionIds: ids(executions),
            rootSpanIds: ids(roots),
            basis: metricBasis,
            policy: "backend-scenario-measures-v1",
          },
          ...(after.length
            ? [
                {
                  side: "after" as const,
                  executionIds: ids(afterExecutions),
                  rootSpanIds: ids(afterRoots),
                  basis: metricBasis,
                  policy: "backend-scenario-measures-v1" as const,
                },
              ]
            : []),
        ],
        limits: {},
        idempotencyKey: crypto.randomUUID(),
        ...(scope && !after.length
          ? { diagramScope: { pin: scope.pin, selectors: scope.selectors } }
          : {}),
      };
      const job = await recovery.execute(makeAnalysisAttempt("start", { projectId }, input));
      if (job && "id" in job) {
        setJobId(job.id);
        setVersion("");
        setError("");
      }
    } catch (e) {
      setError(String(e));
    }
  }
  return (
    <Stack data-testid="backend-scenario-measurements">
      <Title order={3}>Измерения и сравнение сценариев</Title>
      <Text>
        Закрепите прочитанную observation + correlation для каждой стороны. Unknown build остаётся
        exploratory; desired proposal не измеряется.
      </Text>
      <Group>
        <Button disabled={!ready || pins.length >= 20} onClick={() => add("before")}>
          Добавить выбранный exact pin: before
        </Button>
        <Button disabled={!ready || pins.length >= 20} onClick={() => add("after")}>
          Добавить выбранный exact pin: after
        </Button>
      </Group>
      {pins.map((x, i) => (
        <Group key={`${x.pin.side}/${x.pin.observationSetId}/${x.pin.version}`}>
          <Text>
            {x.pin.side}: {x.pin.observationSetId} v{x.pin.version}, correlation v
            {x.pin.correlationVersion}
          </Text>
          <Button variant="subtle" onClick={() => setPins((old) => old.filter((_, n) => n !== i))}>
            Убрать pin {i + 1}
          </Button>
        </Group>
      ))}
      <TextInput
        label="Before execution IDs (через запятую)"
        value={executions}
        onChange={(e) => setExecutions(e.currentTarget.value)}
      />
      <TextInput
        label="Before root span IDs"
        value={roots}
        onChange={(e) => setRoots(e.currentTarget.value)}
      />
      <TextInput
        label="After execution IDs (через запятую)"
        value={afterExecutions}
        onChange={(e) => setAfterExecutions(e.currentTarget.value)}
      />
      <TextInput
        label="After root span IDs"
        value={afterRoots}
        onChange={(e) => setAfterRoots(e.currentTarget.value)}
      />
      <NativeSelect
        label="Основание counts"
        value={basis}
        onChange={(e) => setBasis(e.currentTarget.value)}
        data={[
          { value: "spans", label: "Client spans" },
          { value: "measurements", label: "Explicit measurements" },
        ]}
      />
      {basis === "measurements" && (
        <>
          <TextInput
            label="Measurement basis"
            value={provider}
            onChange={(e) => setProvider(e.currentTarget.value)}
          />
          <TextInput
            label="Measurement scope"
            value={metricScope}
            onChange={(e) => setMetricScope(e.currentTarget.value)}
          />
        </>
      )}
      <Button
        disabled={recovery.blocked || !pins.length || !executions}
        onClick={() => void start()}
      >
        Сохранить measurement job
      </Button>
      {(error || detail.isError || results.isError) && (
        <Alert color="red">{error || "Точный отчёт недоступен"}</Alert>
      )}
      <TextInput
        label="Measurement job ID"
        value={jobId}
        onChange={(e) => {
          setJobId(e.currentTarget.value);
          setVersion("");
        }}
      />
      {detail.data && (
        <Text>
          Job: {detail.data.job.status}; input hash {detail.data.job.analysisInputHash}
        </Text>
      )}
      <Button
        disabled={!detail.data?.job.resultVersion}
        onClick={() => setVersion(String(detail.data!.job.resultVersion))}
      >
        Закрепить доступную result version
      </Button>
      <TextInput
        label="Точная result version"
        value={version}
        onChange={(e) => setVersion(e.currentTarget.value)}
      />
      <NativeSelect
        label="Раздел measurement report"
        value={section}
        data={["measurements", "comparisons", "observed_sequences"]}
        onChange={(e) => setSection(e.currentTarget.value as typeof section)}
      />
      {results.data?.items.map((row) => {
        const d = row.detail;
        if ("metrics" in d) return <MeasurementValues key={row.id} report={d} />;
        if ("deltas" in d)
          return (
            <Stack key={row.id}>
              <Alert color="yellow">
                Различаются условия: {d.conditionDifferences.join(", ") || "не выявлены"}. Delta не
                доказывает причинность.
              </Alert>
              <MeasurementValues report={d.before} />
              <MeasurementValues report={d.after} />
              <Code block>{JSON.stringify(d.deltas, null, 2)}</Code>
            </Stack>
          );
        if ("relations" in d && "measurements" in d)
          return (
            <Stack key={row.id}>
              <Button
                disabled={
                  !scope || scope.scopeHash !== d.diagramScope.scopeHash || !observationContext
                }
                onClick={() =>
                  observationContext?.setOverlay({
                    jobId,
                    resultVersion: Number(version),
                    analysisInputHash: results.data!.manifest.analysisInputHash,
                    report: d,
                  })
                }
              >
                Показать этот saved overlay на точной диаграмме
              </Button>
              <BackendDiagramObservations report={d} scope={scope} />
            </Stack>
          );
        return null;
      })}
    </Stack>
  );
}
