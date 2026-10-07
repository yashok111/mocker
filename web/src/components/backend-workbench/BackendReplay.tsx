import { useDiagramReplayPreparation } from "./BackendDiagramReplayContext";
import { parseBrowserSafeJson } from "@/api/preciseJson";
import { ApiFailure } from "@/api/client";
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Alert,
  Button,
  Group,
  NativeSelect,
  Paper,
  Stack,
  Text,
  Textarea,
  TextInput,
  Title,
} from "@mantine/core";
import { BackendTestProfiles } from "./BackendTestProfiles";
import {
  clearReplayAttempt,
  readReplayAttempt,
  writeReplayAttempt,
  type ReplayAttempt,
} from "./backendReplayRecovery";
import {
  reconcileBackendReplayRun,
  cancelBackendReplayRun,
  compareBackendReplayRuns,
  connectBackendReplayProfile,
  getBackendReplayRun,
  getBackendReplayProfile,
  getBackendReplayPackage,
  getBackendReplayTemplate,
  listBackendReplayPackages,
  listBackendReplayProfiles,
  listBackendReplayRuns,
  listBackendReplayTargets,
  revokeBackendReplayAuthorization,
  saveBackendReplayPackage,
  startBackendReplay,
} from "@/api/generated/backend-projects/backend-projects";
import type {
  BackendReplayComparison,
  BackendReplayPackage,
  BackendReplayProvenance,
  ConnectBackendReplayProfileRequest,
  RevokeBackendReplayAuthorizationRequest,
  SaveBackendReplayPackageRequest,
  StartBackendReplayRequest,
} from "@/api/generated/schemas";
const exactKey = (pin: { id: string; version: number }) => `${pin.id}:${pin.version}`;
function result(response: { status: number; data: unknown }): unknown {
  if (response.status >= 300) throw new Error(JSON.stringify(response.data));
  return response.data;
}
// `open` says whether the operator can see the panel. BackendProjectPage
// mounts it inside a closed <details> on EVERY project page, eagerly on
// purpose: the mount restores a pending attempt from localStorage and the
// diagram's prepare button moves focus into the panel. Only the runs list's
// 2 s poll waits on `open`; a standalone mount is visible, hence the default.
export function BackendReplay({ projectId, open = true }: { projectId: string; open?: boolean }) {
  const diagramReplay = useDiagramReplayPreparation();
  const [refused, setRefused] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [recovery] = useState(() => {
    try {
      return { pending: readReplayAttempt(localStorage, projectId), error: "" };
    } catch (e) {
      return { pending: null, error: String(e) };
    }
  });
  const [pending, setPending] = useState<ReplayAttempt | null>(recovery.pending);
  const [profileID, setProfileID] = useState("");
  const [packageID, setPackageID] = useState("");
  const [runID, setRunID] = useState("");
  const [rightID, setRightID] = useState("");
  const [draft, setDraft] = useState("");
  const [provenance, setProvenance] = useState(
    JSON.stringify(
      {
        sourceFiles: [],
        build: {
          serviceVersion: "",
          variant: "fixed",
          sourceTreeHash: "",
          toolchain: "",
          goos: "",
          goarch: "",
          buildFlags: [],
        },
      },
      null,
      2,
    ),
  );
  const [saveID, setSaveID] = useState<string>(() => crypto.randomUUID());
  const [expectedVersion, setExpectedVersion] = useState("0");
  const [ack, setAck] = useState("");
  const [comparison, setComparison] = useState<BackendReplayComparison | null>(null);
  const targets = useQuery({
    queryKey: ["replay-targets", projectId],
    queryFn: async () => {
      const r = await listBackendReplayTargets(projectId);
      if (r.status !== 200) throw new Error(JSON.stringify(r.data));
      return r.data;
    },
    retry: false,
  });
  const profiles = useQuery({
    queryKey: ["replay-profiles", projectId],
    queryFn: async () => {
      const r = await listBackendReplayProfiles(projectId);
      if (r.status !== 200) throw new Error(JSON.stringify(r.data));
      return r.data;
    },
    retry: false,
  });
  const packages = useQuery({
    queryKey: ["replay-packages", projectId],
    queryFn: async () => {
      const r = await listBackendReplayPackages(projectId);
      if (r.status !== 200) throw new Error(JSON.stringify(r.data));
      return r.data;
    },
    retry: false,
  });
  const run = useQuery({
    queryKey: ["replay-run", projectId, runID],
    enabled: !!runID,
    queryFn: async () => {
      const r = await getBackendReplayRun(projectId, runID);
      if (r.status !== 200) throw new Error(JSON.stringify(r.data));
      return r.data;
    },
    retry: false,
    refetchInterval: (q) =>
      q.state.data && ["queued", "running"].includes(q.state.data.status) ? 2000 : false,
  });
  const runActive = !!run.data && ["queued", "running"].includes(run.data.status);
  const runs = useQuery({
    queryKey: ["replay-runs", projectId],
    queryFn: async () => {
      const r = await listBackendReplayRuns(projectId);
      if (r.status !== 200) throw new Error(JSON.stringify(r.data));
      return r.data;
    },
    retry: false,
    // The list re-read every 2 s on every project page, the panel closed or
    // not. It polls while the panel is open, or while the run selected here
    // (Start selects the run it created) is still queued or running, so the
    // run picker's status labels follow a run the operator awaits. The
    // verdict itself never depended on this list: `run` above polls the
    // selected run on its own until it leaves queued/running.
    refetchInterval: open || runActive ? 2000 : false,
  });
  const selectedProfile = profiles.data?.find((x) => exactKey(x.pin) === profileID)?.pin;
  const selectedPackage = packages.data?.find((x) => exactKey(x.pin) === packageID)?.pin;
  const exactProfile = useQuery({
    queryKey: ["replay-profile-exact", projectId, selectedProfile],
    enabled: !!selectedProfile,
    queryFn: async ({ signal }) => {
      if (!selectedProfile) throw new Error("Выберите профиль");
      const r = await getBackendReplayProfile(
        projectId,
        selectedProfile.id,
        selectedProfile.version,
        { signal },
      );
      if (r.status !== 200) throw new Error(JSON.stringify(r.data));
      if (r.data.pin.contentHash !== selectedProfile.contentHash)
        throw new Error("Hash профиля не совпадает");
      return r.data;
    },
    retry: false,
  });
  const exactPackage = useQuery({
    queryKey: ["replay-package-exact", projectId, selectedPackage],
    enabled: !!selectedPackage,
    queryFn: async ({ signal }) => {
      if (!selectedPackage) throw new Error("Выберите пакет");
      const r = await getBackendReplayPackage(
        projectId,
        selectedPackage.id,
        selectedPackage.version,
        { signal },
      );
      if (r.status !== 200) throw new Error(JSON.stringify(r.data));
      if (r.data.pin.contentHash !== selectedPackage.contentHash)
        throw new Error("Hash пакета не совпадает");
      return r.data;
    },
    retry: false,
  });
  const profile = selectedProfile ? exactProfile.data : undefined;
  const saved = selectedPackage ? exactPackage.data : undefined;
  const blocked = busy || !!pending || !!recovery.error;
  async function refresh() {
    await Promise.all([profiles.refetch(), packages.refetch(), runs.refetch()]);
  }
  async function send(intent: ReplayAttempt) {
    setBusy(true);
    setError("");
    setRefused(false);
    try {
      if (intent.action === "connect")
        result(
          await connectBackendReplayProfile(
            projectId,
            intent.body as ConnectBackendReplayProfileRequest,
          ),
        );
      if (intent.action === "save")
        result(
          await saveBackendReplayPackage(projectId, intent.body as SaveBackendReplayPackageRequest),
        );
      if (intent.action === "revoke")
        result(
          await revokeBackendReplayAuthorization(
            projectId,
            intent.body as RevokeBackendReplayAuthorizationRequest,
          ),
        );
      if (intent.action === "start") {
        const r = await startBackendReplay(projectId, intent.body as StartBackendReplayRequest);
        if (r.status !== 202) throw new Error(JSON.stringify(r.data));
        setRunID(r.data.id);
      }
      await clearReplayAttempt(localStorage, projectId, intent);
      setPending(readReplayAttempt(localStorage, projectId));
      await refresh();
    } catch (e) {
      setError(String(e));
      setRefused(e instanceof ApiFailure && [400, 403, 404, 409, 413, 422, 429].includes(e.status));
    } finally {
      setBusy(false);
    }
  }
  async function mutate(action: ReplayAttempt["action"], body: unknown) {
    if (blocked) return;
    setBusy(true);
    try {
      const intent = { projectId, action, body };
      await writeReplayAttempt(localStorage, projectId, intent);
      setPending(intent);
      await send(intent);
    } catch (e) {
      setError(String(e));
      setBusy(false);
    }
  }
  async function template() {
    try {
      const r = await getBackendReplayTemplate(projectId);
      if (r.status !== 200) throw new Error(JSON.stringify(r.data));
      setDraft(
        JSON.stringify(
          {
            ...r.data,
            target: { revisionId: "" },
            targetHash: "",
            profile: profile?.pin ?? { id: "", version: 1, contentHash: "" },
            ...(profile ? { profile: profile.pin } : {}),
          },
          null,
          2,
        ),
      );
    } catch (e) {
      setError(String(e));
    }
  }
  function save() {
    try {
      const version = Number(expectedVersion);
      if (!Number.isSafeInteger(version) || version < 0)
        throw new Error("Нужна точная версия CAS ≥ 0");
      mutate("save", {
        id: saveID,
        expectedVersion: version,
        package: parseBrowserSafeJson(draft) as BackendReplayPackage,
        provenance: parseBrowserSafeJson(provenance) as BackendReplayProvenance,
        idempotencyKey: crypto.randomUUID(),
      });
    } catch (e) {
      setError(String(e));
    }
  }
  const options = [
    { value: "", label: "Выберите run" },
    ...(runs.data ?? []).map((x) => ({ value: x.id, label: `${x.status} · ${x.id}` })),
  ];
  return (
    <Paper withBorder p="md" component="section" data-testid="backend-replay">
      <Stack>
        <Title order={2} tabIndex={-1}>
          Orders replay
        </Title>
        <Text>
          Payment — mocked; сохранение заказа — actual_fixture. Отчёт относится только к
          закреплённому пакету, выбранным шагам и привязкам диаграммы.
        </Text>
        {(error || recovery.error) && <Alert color="red">{error || recovery.error}</Alert>}
        {[targets, profiles, packages, runs, run, exactProfile, exactPackage].some(
          (q) => q.isError,
        ) && (
          <Alert color="red">
            Не удалось прочитать replay. Обновите данные; отсутствие ответа не означает отсутствие
            эффектов.
          </Alert>
        )}
        {pending && (
          <Alert color="yellow">
            <Stack>
              <Text>
                Есть сохранённый запрос {pending.action}. После потери ответа повторяется только
                этот запрос с прежним ключом. Перезагрузка ничего не отправляет.
              </Text>
              <Button disabled={busy} onClick={() => void send(pending)}>
                Повторить точный сохранённый запрос
              </Button>
              <details>
                <summary>Запрос для восстановления</summary>
                <pre style={{ whiteSpace: "pre-wrap" }}>{JSON.stringify(pending, null, 2)}</pre>
              </details>
              {refused && (
                <Button
                  variant="default"
                  disabled={busy}
                  onClick={async () => {
                    try {
                      await clearReplayAttempt(localStorage, projectId, pending);
                      setPending(readReplayAttempt(localStorage, projectId));
                      setRefused(false);
                    } catch (e) {
                      setError(String(e));
                    }
                  }}
                >
                  Dismiss refused request and edit input
                </Button>
              )}
            </Stack>
          </Alert>
        )}
        <Button variant="default" disabled={busy} onClick={() => void refresh()}>
          Обновить профили, пакеты и runs
        </Button>
        <BackendTestProfiles
          targets={targets.data ?? []}
          profiles={profiles.data ?? []}
          selected={profileID}
          onSelect={setProfileID}
          disabled={blocked}
          onConnect={(body) => mutate("connect", body)}
          onRevoke={(x) =>
            mutate("revoke", { profile: x.pin, idempotencyKey: crypto.randomUUID() })
          }
        />
        <Title order={3}>Неизменяемый пакет</Title>
        {diagramReplay?.preparation?.projectId === projectId && (
          <Alert>
            <Stack>
              <Text>
                Подготовлена точная область diagram v
                {diagramReplay.preparation.package.diagramScope?.pin.version}. Привязок:{" "}
                {diagramReplay.preparation.package.diagramBindings?.length ?? 0}.
              </Text>
              {diagramReplay.preparation.excluded.map((x) => (
                <Text key={x.id}>
                  {x.id}: {x.reason}
                </Text>
              ))}
              <Button
                disabled={
                  blocked || !profile || !diagramReplay.preparation.package.diagramBindings?.length
                }
                onClick={() => {
                  if (profile && diagramReplay.preparation) {
                    setDraft(
                      JSON.stringify(
                        { ...diagramReplay.preparation.package, profile: profile.pin },
                        null,
                        2,
                      ),
                    );
                    setSaveID(crypto.randomUUID());
                    setExpectedVersion("0");
                  }
                }}
              >
                Перенести подготовленный пакет с выбранным профилем в редактор
              </Button>
              <Text size="xs">
                Исключённые элементы остаются в excludedIds. Заполните provenance, затем отдельно
                Save и Start.
              </Text>
            </Stack>
          </Alert>
        )}

        <Text size="sm">
          Сохраните exact target/hash, artifact pins, finding fingerprint и выбранные
          diagramScope/bindings. SourceFiles и Build берутся из независимого манифеста сборки
          fixture. Save проверяет владельца и точные версии, но не даёт согласие на reset.
        </Text>
        <NativeSelect
          label="Сохранённая версия пакета"
          value={packageID}
          data={[
            { value: "", label: "Выберите пакет" },
            ...(packages.data ?? []).map((x) => ({
              value: exactKey(x.pin),
              label: `${x.pin.id} · v${x.pin.version} · ${x.pin.contentHash}`,
            })),
          ]}
          onChange={(e) => setPackageID(e.currentTarget.value)}
        />
        <Group>
          <Button variant="default" onClick={() => void template()}>
            Загрузить шаблон Orders для редактирования
          </Button>
          <Button
            variant="default"
            disabled={!saved}
            onClick={() => {
              if (saved) {
                setDraft(JSON.stringify(saved.package, null, 2));
                setProvenance(JSON.stringify(saved.provenance, null, 2));
                setSaveID(saved.pin.id);
                setExpectedVersion(String(saved.pin.version));
              }
            }}
          >
            Подготовить новую версию выбранного пакета
          </Button>
        </Group>
        <TextInput
          label="ID пакета"
          value={saveID}
          onChange={(e) => setSaveID(e.currentTarget.value)}
        />
        <TextInput
          label="Ожидаемая предыдущая версия (0 — новый пакет)"
          value={expectedVersion}
          onChange={(e) => setExpectedVersion(e.currentTarget.value)}
        />
        <Textarea
          label="Точный пакет JSON, включая diagram bindings"
          minRows={8}
          autosize
          maxRows={22}
          value={draft}
          onChange={(e) => setDraft(e.currentTarget.value)}
        />
        <Textarea
          label="Provenance JSON: sourceFiles и build"
          minRows={5}
          autosize
          maxRows={16}
          value={provenance}
          onChange={(e) => setProvenance(e.currentTarget.value)}
        />
        <Button disabled={blocked || !draft || !provenance} onClick={save}>
          Сохранить точную версию пакета
        </Button>
        <TextInput
          label="ID предыдущего неопределённого run для явного подтверждения оператором (если требуется)"
          value={ack}
          onChange={(e) => setAck(e.currentTarget.value)}
        />
        <Button
          disabled={
            blocked ||
            !saved ||
            !profile ||
            exactKey(saved.package.profile) !== exactKey(profile.pin) ||
            saved.package.profile.contentHash !== profile.pin.contentHash
          }
          onClick={() => {
            if (saved && profile)
              mutate("start", {
                package: saved.pin,
                profile: profile.pin,
                expectedIdentityHash: profile.identityHash,
                resetAuthorizationId: profile.authorization.id,
                resetAuthorizationVersion: profile.authorization.version,
                idempotencyKey: crypto.randomUUID(),
                ...(ack ? { acknowledgedPreviousRunId: ack } : {}),
              });
          }}
        >
          Start: сбросить fixture и выполнить сохранённый пакет
        </Button>
        <Text size="xs">
          Start требует профиль той же точной версии, что сохранён в пакете. Для другого
          build/profile сохраните новую версию пакета. Выбор пакета или run ничего не запускает.
        </Text>
        <NativeSelect
          label="Run и неизменяемый отчёт"
          value={runID}
          data={options}
          onChange={(e) => {
            setRunID(e.currentTarget.value);
            setComparison(null);
          }}
        />
        {run.data && (
          <Stack>
            <Text component="output" aria-live="polite">
              Статус: {run.data.status}
            </Text>
            <Button
              color="orange"
              disabled={busy || !["queued", "running"].includes(run.data.status)}
              onClick={async () => {
                setBusy(true);
                try {
                  result(await cancelBackendReplayRun(projectId, runID, {}));
                  await run.refetch();
                  await runs.refetch();
                } catch (e) {
                  setError(String(e));
                } finally {
                  setBusy(false);
                }
              }}
            >
              Отменить на сервере
            </Button>
            <Button
              variant="default"
              disabled={busy || ["queued", "running"].includes(run.data.status)}
              onClick={async () => {
                setBusy(true);
                try {
                  result(await reconcileBackendReplayRun(projectId, runID, {}));
                  await run.refetch();
                } catch (e) {
                  setError(String(e));
                } finally {
                  setBusy(false);
                }
              }}
            >
              Прочитать journal и сверить неопределённый run
            </Button>
            <Text size="sm">
              Отмена останавливает дальнейшие шаги; уже отправленный запрос может иметь эффект.
              Закрытие экрана не отменяет run.
            </Text>
            {run.data.report && (
              <>
                <Text>{run.data.report.reason}</Text>
                {run.data.report.receipts.map((receipt) => (
                  <Text key={receipt.requestKey}>
                    {receipt.stepId} · {receipt.endpoint}: {receipt.outcome} · HTTP{" "}
                    {receipt.httpStatus} · epoch {receipt.resultEpoch} · orders{" "}
                    {receipt.counters.orders} · mocked charges {receipt.counters.charges} · attempts{" "}
                    {receipt.counters.attempts} · triggers {receipt.counters.triggers}
                  </Text>
                ))}

                {run.data.report.assertions.map((a) => (
                  <Text key={a.id}>
                    {a.kind}: {a.actual} / {a.expected} · {a.passed ? "passed" : "failed"} ·{" "}
                    {a.scope}
                  </Text>
                ))}
                {run.data.report.bindings.map((b, i) => (
                  <Text key={i}>
                    {b.binding.elementId} · diagram v{b.binding.diagram.version} · {b.status}
                  </Text>
                ))}
              </>
            )}
            <details>
              <summary>Точные pins, provenance и отчёт</summary>
              <pre style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>
                {JSON.stringify(run.data, null, 2)}
              </pre>
            </details>
          </Stack>
        )}
        <NativeSelect
          label="Правый run для сравнения"
          value={rightID}
          data={options}
          onChange={(e) => {
            setRightID(e.currentTarget.value);
            setComparison(null);
          }}
        />
        <Button
          variant="default"
          disabled={busy || !runID || !rightID}
          onClick={async () => {
            setBusy(true);
            try {
              const r = await compareBackendReplayRuns(projectId, {
                leftRunId: runID,
                rightRunId: rightID,
              });
              if (r.status !== 200) throw new Error(JSON.stringify(r.data));
              setComparison(r.data);
            } catch (e) {
              setError(String(e));
            } finally {
              setBusy(false);
            }
          }}
        >
          Сравнить закреплённые runs
        </Button>
        {comparison && (
          <Alert color={comparison.fixed ? "green" : "yellow"}>
            <Text>
              Reproduced: {String(comparison.reproduced)} · Fixed: {String(comparison.fixed)}
            </Text>
            <details>
              <summary>Оба отчёта с исходными pins</summary>
              <pre style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>
                {JSON.stringify(comparison, null, 2)}
              </pre>
            </details>
          </Alert>
        )}
      </Stack>
    </Paper>
  );
}
