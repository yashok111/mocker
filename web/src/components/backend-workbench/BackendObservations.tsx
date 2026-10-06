import { BackendScenarioMeasurements } from "./BackendScenarioMeasurements";
import { useEffect, useRef, useState } from "react";
import {
  Alert,
  Button,
  Code,
  Group,
  Select,
  Stack,
  Text,
  Textarea,
  TextInput,
  Title,
} from "@mantine/core";
import {
  adaptBackendObservations,
  importBackendObservations,
  listBackendObservations,
  getBackendObservationVersion,
  getBackendObservationRecords,
  correlateBackendObservations,
  getBackendObservationCorrelation,
} from "@/api/generated/backend-projects/backend-projects";
import type {
  ObservationVersion,
  ObservationRecordPage,
  ObservationCorrelationSnapshot,
  ObservationImportInput,
  ObservationAdaptInput,
  ObservationCorrelateInput,
  ObservationAdaptedBatch,
} from "@/api/generated/schemas";
import { parseBrowserSafeJson } from "@/api/preciseJson";
import { BackendCorrelation } from "./BackendCorrelation";
import { useObservationScope } from "./BackendObservationContext";
import {
  observationPinKey,
  observationRequestGate,
  observationDisplay,
} from "./backendObservationReads";
export function BackendObservations({ projectId }: { projectId: string }) {
  const [catalog, setCatalog] = useState<ObservationVersion[]>([]),
    [catalogCursor, setCatalogCursor] = useState("");
  const [selected, setSelected] = useState<ObservationVersion>();
  const [records, setRecords] = useState<ObservationRecordPage>();
  const [correlation, setCorrelation] = useState<ObservationCorrelationSnapshot>();
  const [adapted, setAdapted] = useState<ObservationAdaptedBatch>();
  const [draft, setDraft] = useState("");
  const [mapping, setMapping] = useState("");
  const [correlationVersion, setCorrelationVersion] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const gate = useRef(observationRequestGate());
  const scope = useObservationScope()?.scope;
  const scopeKey = scope
    ? `${scope.pin.id}/${scope.pin.version}/${scope.pin.contentHash}/${scope.scopeHash}`
    : "";
  useEffect(() => {
    gate.current.begin();
    setBusy(false);
    setMapping("");
    setCorrelation(undefined);
    setCorrelationVersion("");
  }, [scopeKey]);

  useEffect(
    () => () => {
      gate.current.begin();
    },
    [],
  );
  async function action(fn: (current: () => boolean) => Promise<void>) {
    const token = gate.current.begin();
    setBusy(true);
    setError("");
    try {
      await fn(() => gate.current.current(token));
    } catch {
      if (gate.current.current(token))
        setError(
          "Запрос отклонён. Проверьте точные pins и контекст. При неизвестном результате повторите тот же JSON/key.",
        );
    } finally {
      if (gate.current.current(token)) setBusy(false);
    }
  }
  function assertOK<T extends { status: number }>(r: T) {
    if (r.status !== 200) throw new Error("request rejected");
  }
  function loadCatalog(cursor = "") {
    void action(async (current) => {
      const r = await listBackendObservations(projectId, { limit: 100, cursor });
      assertOK(r);
      if (r.status === 200 && current()) {
        setCatalog((old) => (cursor ? [...old, ...r.data.items] : r.data.items));
        setCatalogCursor(r.data.nextCursor);
      }
    });
  }
  function selectPin(key: string | null) {
    gate.current.begin();
    setRecords(undefined);
    setCorrelation(undefined);
    setMapping("");
    const pin = catalog.find((p) => observationPinKey(p) === key);
    setSelected(pin);
    if (!pin) return;
    void action(async (current) => {
      const [v, r] = await Promise.all([
        getBackendObservationVersion(projectId, pin.setId, pin.version),
        getBackendObservationRecords(projectId, pin.setId, pin.version, { limit: 100 }),
      ]);
      assertOK(v);
      assertOK(r);
      if (current() && v.status === 200 && r.status === 200) {
        if (v.data.contentHash !== pin.contentHash) throw new Error("pin mismatch");
        setSelected(v.data);
        setRecords(r.data);
      }
    });
  }
  function prepareMapping() {
    if (!selected) return;
    setMapping(
      JSON.stringify(
        {
          observation: {
            setId: selected.setId,
            version: selected.version,
            contentHash: selected.contentHash,
            recordCount: selected.recordCount,
          },
          revisionId: scope && "revisionId" in scope.target ? scope.target.revisionId : "",
          sourceHash: "",
          targetGraphHash: scope?.targetHash ?? "",
          serviceId: selected.context.source.serviceId,
          policy: "backend-correlation-v1",
          settings: { inferSourceLocator: false, inferFingerprint: false },
          overrides: [],
          expectedCorrelationVersion: correlation?.version ?? 0,
          idempotencyKey: crypto.randomUUID(),
          ...(scope
            ? {
                diagramScope: { pin: scope.pin, selectors: scope.selectors },
                scopeHash: scope.scopeHash,
              }
            : {}),
        },
        null,
        2,
      ),
    );
  }
  return (
    <Stack data-testid="backend-observations">
      <Title order={2} tabIndex={-1}>
        Наблюдения и сопоставления
      </Title>
      <Text>
        Source — импортированная модель; intent — проект; observed — выбранные исполнения.
        Отсутствующие данные неизвестны. Одна трасса не подтверждает все пути.
      </Text>
      {error && (
        <Alert role="alert" color="red">
          {error}
        </Alert>
      )}
      <Group>
        <Button disabled={busy} onClick={() => loadCatalog()}>
          Обновить версии
        </Button>
        {catalogCursor && (
          <Button disabled={busy} onClick={() => loadCatalog(catalogCursor)}>
            Ещё версии
          </Button>
        )}
      </Group>
      <Select
        label="Точная версия наблюдений"
        value={selected ? observationPinKey(selected) : null}
        data={catalog.map((p) => ({
          value: observationPinKey(p),
          label: `${p.name}: v${p.version} · ${p.setId} · ${p.contentHash}`,
        }))}
        onChange={selectPin}
        searchable
      />
      {selected && (
        <>
          <Text>Контекст сборки, среды, окна и sampling</Text>
          <Code block>{JSON.stringify(selected.context, null, 2)}</Code>
          <Text>
            Записей: {observationDisplay(selected.recordCount)}; input size:{" "}
            {observationDisplay(selected.context.input.size)}
          </Text>
          <Code block>{JSON.stringify(records?.items ?? [], null, 2)}</Code>
          {records?.nextCursor && (
            <Button
              disabled={busy}
              onClick={() =>
                void action(async (current) => {
                  const r = await getBackendObservationRecords(
                    projectId,
                    selected.setId,
                    selected.version,
                    { limit: 100, cursor: records.nextCursor },
                  );
                  assertOK(r);
                  if (r.status === 200 && current()) setRecords(r.data);
                })
              }
            >
              Следующая страница записей
            </Button>
          )}
        </>
      )}
      <Textarea
        label="JSON адаптации или импорта (полный context, batchId/key)"
        autosize
        minRows={5}
        value={draft}
        onChange={(e) => setDraft(e.currentTarget.value)}
      />
      <Group>
        <Button
          disabled={busy || !draft}
          onClick={() =>
            void action(async (current) => {
              const r = await adaptBackendObservations(
                projectId,
                parseBrowserSafeJson(draft) as ObservationAdaptInput,
              );
              assertOK(r);
              if (r.status === 200 && current()) setAdapted(r.data);
            })
          }
        >
          Адаптировать без сохранения
        </Button>
        <Button
          disabled={busy || !draft}
          onClick={() =>
            void action(async (current) => {
              const r = await importBackendObservations(
                projectId,
                parseBrowserSafeJson(draft) as ObservationImportInput,
              );
              assertOK(r);
              if (current())
                setError("Импорт сохранён. Обновите список и выберите возвращённую версию.");
            })
          }
        >
          Импортировать точный batch
        </Button>
      </Group>
      {adapted && (
        <>
          <Text>Исключения privacy и неподдерживаемые записи</Text>
          <Code block>
            {JSON.stringify(
              { excluded: adapted.excluded, unsupported: adapted.unsupported, gaps: adapted.gaps },
              null,
              2,
            )}
          </Code>
          {adapted.batches.map((b, i) => (
            <Button
              key={i}
              onClick={() =>
                setDraft(
                  JSON.stringify(
                    {
                      mode: "create",
                      name: `Наблюдения ${i + 1}`,
                      context: b.context,
                      records: b.records,
                      batchId: crypto.randomUUID(),
                      idempotencyKey: crypto.randomUUID(),
                    },
                    null,
                    2,
                  ),
                )
              }
            >
              Выбрать группу {i + 1}: {b.context.source.serviceId}, {b.records.length} записей
            </Button>
          ))}
        </>
      )}
      {scope && (
        <Code block>
          {JSON.stringify(
            {
              diagram: scope.pin,
              selectors: scope.selectors,
              scopeHash: scope.scopeHash,
              target: scope.target,
            },
            null,
            2,
          )}
        </Code>
      )}
      <Button disabled={!selected || busy} onClick={prepareMapping}>
        Подготовить точное сопоставление
      </Button>
      <Textarea
        label="Correlation JSON: заполните sourceHash и точную source revision; manual overrides требуют reason"
        autosize
        minRows={5}
        value={mapping}
        onChange={(e) => setMapping(e.currentTarget.value)}
      />
      <Button
        disabled={!mapping || busy || !selected}
        onClick={() =>
          void action(async (current) => {
            if (!selected) return;
            const input = parseBrowserSafeJson(mapping) as ObservationCorrelateInput;
            const r = await correlateBackendObservations(projectId, selected.setId, input);
            assertOK(r);
            if (r.status === 200 && current()) {
              setCorrelation(r.data);
              setCorrelationVersion(String(r.data.version));
            }
          })
        }
      >
        Сохранить новое сопоставление
      </Button>
      <TextInput
        label="Точная версия сохранённой correlation"
        value={correlationVersion}
        onChange={(e) => setCorrelationVersion(e.currentTarget.value)}
      />
      <Button
        disabled={!selected || busy || !correlationVersion}
        onClick={() =>
          void action(async (current) => {
            if (!selected) return;
            const v = Number(correlationVersion);
            if (!Number.isSafeInteger(v) || v < 1) throw new Error("unsafe version");
            const r = await getBackendObservationCorrelation(projectId, selected.setId, v);
            assertOK(r);
            if (r.status === 200 && current()) setCorrelation(r.data);
          })
        }
      >
        Открыть исходный результат
      </Button>
      {correlation?.nextCursor && (
        <Button
          disabled={busy}
          onClick={() =>
            void action(async (current) => {
              if (!selected || !correlation) return;
              const r = await getBackendObservationCorrelation(
                projectId,
                selected.setId,
                correlation.version,
                { limit: 100, cursor: correlation.nextCursor },
              );
              assertOK(r);
              if (r.status === 200 && current()) setCorrelation(r.data);
            })
          }
        >
          Следующая страница сопоставления
        </Button>
      )}
      <BackendScenarioMeasurements
        key={`${projectId}/${scopeKey}`}
        projectId={projectId}
        selected={selected}
        correlation={correlation}
        scope={scope}
      />
      {correlation && (
        <BackendCorrelation
          snapshot={correlation}
          onOverride={(override) => {
            const input = structuredClone(correlation.input);
            input.expectedCorrelationVersion = correlation.version;
            input.idempotencyKey = crypto.randomUUID();
            input.overrides = [
              ...input.overrides.filter((o) => o.recordId !== override.recordId),
              override,
            ];
            setMapping(JSON.stringify(input, null, 2));
          }}
        />
      )}
    </Stack>
  );
}
