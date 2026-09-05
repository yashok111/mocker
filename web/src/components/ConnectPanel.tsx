import { useEffect, useRef, useState } from "react";
import {
  Alert,
  Button,
  Card,
  Group,
  List,
  SimpleGrid,
  Stack,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { IconCheck, IconCopy, IconPlayerPlay } from "@tabler/icons-react";
import { useListTraffic } from "@/api/generated/traffic/traffic.ts";
import { useProbeWorkspace } from "@/api/generated/workspaces/workspaces.ts";
import { apiAddress, buildRecipes, type Recipe } from "@/connect/recipes";
import { runProbe, type ProbeResult } from "@/connect/probe";
import { describeApiFailure } from "@/api/errors";
import type { ServerConfigView, ServerProbeView, WorkspaceView } from "@/api/generated/schemas";

// ConnectPanel is DESIGN §14 screen 4, «Подключить»: the panel that turns "the
// mock exists" into "my frontend talks to it" for someone who has never heard
// of OpenAPI. It takes the WorkspaceView its parent already loaded rather than
// fetching its own copy — the one query this file owns beyond the probe below
// is the traffic summary behind the rate indicator, kept isolated on purpose
// (see RateIndicator's own comment).
//
// «Проверить» dials {workspace.url}{config.reservedPrefix}/health from BOTH
// sides at once: this file's own runProbe from the browser, and the server's
// internal/probe (POST /api/workspaces/{id}/probe) from mocker's own process.
// The two answer different questions — DNS/routing/TLS-chain vs THIS
// browser's own trust store and CORS — so a server-yes/browser-no split gets
// its own named diagnosis (diagnoseCertMismatch below) instead of reading as
// one more opaque network error.

// useDocumentVisible backs the polling rule directly: stop while
// document.visibilityState is 'hidden'. A tab a person alt-tabbed away from has
// no reason to keep hitting the admin plane every few seconds.
function useDocumentVisible(): boolean {
  const [visible, setVisible] = useState(() => document.visibilityState === "visible");
  useEffect(() => {
    function onChange(): void {
      setVisible(document.visibilityState === "visible");
    }
    document.addEventListener("visibilitychange", onChange);
    return () => document.removeEventListener("visibilitychange", onChange);
  }, []);
  return visible;
}

type CopyStatus = "idle" | "copied" | "manual";

// copyToClipboard handles the trap this tool always hits: writeText is
// undefined outside a secure context, and mocker runs over plain http on an
// internal name in every dev and smoke setup there is. Three tiers, tried in
// order, so a copy button never just silently does nothing: the Clipboard API
// when it exists, a selection-based execCommand('copy') when it doesn't, and —
// when neither does — the value is at least left selected so the user has
// something to Ctrl+C, and the caller is told to say so.
async function copyToClipboard(text: string, input: HTMLInputElement | null): Promise<CopyStatus> {
  if (typeof navigator.clipboard?.writeText === "function") {
    try {
      await navigator.clipboard.writeText(text);
      return "copied";
    } catch {
      // A dismissed permission prompt, or a browser that only throws here
      // instead of leaving writeText undefined — either way, fall through.
    }
  }
  if (input === null) {
    return "manual";
  }
  input.select();
  if (typeof document.execCommand === "function") {
    try {
      if (document.execCommand("copy")) {
        return "copied";
      }
    } catch {
      // Some sandboxed contexts throw here instead of returning false.
    }
  }
  // Neither tier worked. input.select() above already highlighted the value
  // for a manual Ctrl+C — this is the honest "manual" outcome, not a button
  // that did nothing.
  return "manual";
}

// CopyField pairs a read-only, already-selectable input (the copy target
// itself, so copyToClipboard's execCommand tier has something real to act on)
// with the button and the outcome message. Used for both the workspace address
// and every recipe's snippet.
function CopyField({ testId, label, value }: { testId: string; label: string; value: string }) {
  const [status, setStatus] = useState<CopyStatus>("idle");
  const inputRef = useRef<HTMLInputElement>(null);

  async function handleCopy(): Promise<void> {
    setStatus(await copyToClipboard(value, inputRef.current));
  }

  return (
    <Stack gap={4}>
      <Group gap="xs" align="flex-end" wrap="nowrap">
        <TextInput
          label={label}
          readOnly
          value={value}
          ref={inputRef}
          data-testid={`${testId}-input`}
          onFocus={(event) => event.currentTarget.select()}
          styles={{ input: { fontFamily: "var(--mantine-font-family-monospace)", fontSize: 12 } }}
          style={{ flex: 1 }}
        />
        <Button
          variant="default"
          leftSection={<IconCopy size={16} />}
          onClick={() => void handleCopy()}
          data-testid={testId}
        >
          Скопировать
        </Button>
      </Group>
      {status === "copied" ? (
        <Text component="output" size="xs" c="teal.7">
          Скопировано
        </Text>
      ) : null}
      {status === "manual" ? (
        <Text component="output" size="xs" c="yellow.8">
          Не скопировалось автоматически — текст выделен, скопируйте вручную (Ctrl+C)
        </Text>
      ) : null}
    </Stack>
  );
}

function RecipeCard({ recipe }: { recipe: Recipe }) {
  return (
    <Card withBorder p="sm" data-testid={`connect-recipe-${recipe.id}`}>
      <Text size="sm" fw={500}>
        {recipe.title}
      </Text>
      <Text size="xs" c="dimmed" mb="xs">
        {recipe.note}
      </Text>
      <CopyField
        testId={`connect-recipe-copy-${recipe.id}`}
        label={recipe.title}
        value={recipe.snippet}
      />
    </Card>
  );
}

// RateIndicator owns the only query this whole panel makes on its own —
// GET /api/workspaces/{id}/traffic, whose rate1m is computed server-side for
// exactly this line (never /traffic/poll: that endpoint is the feed's cursor
// protocol and carries no rate at all). Deliberately no early return on
// isError: a failing background poll — the workspace this belongs to can be
// deleted out from under an open panel — must degrade this component and
// NOTHING outside it, so the failure stays contained to the one line below.
function RateIndicator({ workspaceId }: { workspaceId: number }) {
  const visible = useDocumentVisible();
  const traffic = useListTraffic(workspaceId, undefined, {
    query: {
      enabled: visible,
      // Owns its own cadence so the panel doesn't have to: a screen
      // re-polling in a useEffect alongside this hook's own refetch is how
      // two timers end up racing each other. Only ticks while visible.
      refetchInterval: visible ? 4000 : false,
    },
  });

  let text: string;
  if (traffic.isError) {
    text = "Не удалось получить данные о трафике";
  } else if (traffic.data === undefined || traffic.data.status !== 200) {
    text = "Считаем…";
  } else {
    text = `Сюда пришло ${traffic.data.data.rate1m} запросов за минуту`;
  }

  return (
    <Text size="sm" component="output" data-testid="connect-rate">
      {text}
    </Text>
  );
}

// ProbeResultView turns one ProbeResult into the exact copy DESIGN §14 asks
// for. The network-error branch is the one worth reading twice: the mock plane
// sets CORS headers for every request AFTER a workspace resolves, so a
// workspace that exists answers readably even on an error, but a host that
// resolves and names no workspace 404s through a path that never reaches that
// line — the browser reports a bare network error, and there is no single cause
// to blame, so three are named, in the order worth checking in a corporate
// contour.
function ProbeResultView({ result }: { result: ProbeResult }) {
  switch (result.kind) {
    case "ok":
      return (
        <Alert
          color="teal"
          icon={<IconCheck size={18} />}
          data-testid="connect-probe-result"
          data-probe-kind="ok"
          title="Мок отвечает"
        >
          <Text size="sm">
            Воркспейс: <span data-testid="connect-probe-workspace">{result.workspace}</span>
          </Text>
          <Text size="sm">
            Ревизия: <span data-testid="connect-probe-revision">{result.revision}</span>
          </Text>
        </Alert>
      );
    case "wrong-workspace":
      return (
        <Alert
          color="red"
          role="alert"
          data-testid="connect-probe-result"
          data-probe-kind="wrong-workspace"
        >
          Отвечает другой воркспейс:{" "}
          <span data-testid="connect-probe-workspace">{result.workspace}</span>. Похоже на
          перепутанный wildcard-DNS или прокси
        </Alert>
      );
    case "http-error":
      return (
        <Alert
          color="red"
          role="alert"
          data-testid="connect-probe-result"
          data-probe-kind="http-error"
        >
          Сервер ответил {result.status}: {result.message}
        </Alert>
      );
    case "network-error":
      return (
        <Alert
          color="red"
          role="alert"
          data-testid="connect-probe-result"
          data-probe-kind="network-error"
          title="Браузер не смог обратиться к воркспейсу. Возможные причины:"
        >
          <List size="sm">
            <List.Item>адрес не резолвится — проблема с DNS или wildcard-записью</List.Item>
            <List.Item>в этом браузере не установлен корневой сертификат контура</List.Item>
            <List.Item>воркспейс больше не существует</List.Item>
          </List>
        </Alert>
      );
    case "timeout":
      return (
        <Alert
          color="red"
          role="alert"
          data-testid="connect-probe-result"
          data-probe-kind="timeout"
        >
          Не дождались ответа
        </Alert>
      );
  }
}

// ServerProbe is what useProbeWorkspace() returns — named here rather than
// spelled out at every use site, since its generics carry the mutation's
// full success/error type union.
type ServerProbe = ReturnType<typeof useProbeWorkspace>;

// serverProbeResult reads the server's own probe verdict out of the mutation
// object, mirroring the defensive `.data.status !== 200` check this app's
// other screens already use for a generated mutation (SessionControls,
// RateIndicator above): the non-200 members of the response union exist for
// TypeScript's sake, but customFetch throws an ApiFailure for any of them
// before a caller ever sees one — the check is what makes narrowing to
// ServerProbeView compile, not a runtime possibility this file expects to hit.
function serverProbeResult(probe: ServerProbe): ServerProbeView | null {
  return probe.isSuccess && probe.data.status === 200 ? probe.data.data : null;
}

// ServerProbeKindView is serverProbeResult's rendering, one branch per
// serverProbeView.ts's "kind" — deliberately mirroring ProbeResultView
// above branch for branch, so the two stacked alerts read as the same
// vocabulary asked two different ways rather than two unrelated UIs.
function ServerProbeKindView({ view }: { view: ServerProbeView }) {
  switch (view.kind) {
    case "ok":
      return (
        <Alert
          color="teal"
          icon={<IconCheck size={18} />}
          data-testid="connect-server-probe-result"
          data-probe-kind="ok"
          title="Сервер тоже видит воркспейс"
        >
          <Text size="sm">
            Ревизия: <span data-testid="connect-server-probe-revision">{view.revision}</span>
          </Text>
        </Alert>
      );
    case "wrong-workspace":
      return (
        <Alert
          color="red"
          role="alert"
          data-testid="connect-server-probe-result"
          data-probe-kind="wrong-workspace"
        >
          Сервер видит другой воркспейс:{" "}
          <span data-testid="connect-server-probe-workspace">{view.workspace}</span>
        </Alert>
      );
    case "http-error":
      return (
        <Alert
          color="red"
          role="alert"
          data-testid="connect-server-probe-result"
          data-probe-kind="http-error"
        >
          Сервер получил {view.status}: {view.message}
        </Alert>
      );
    case "network-error":
      return (
        <Alert
          color="red"
          role="alert"
          data-testid="connect-server-probe-result"
          data-probe-kind="network-error"
        >
          Сервер не смог достучаться до воркспейса — проблема с DNS или маршрутизацией внутри
          контура
        </Alert>
      );
    case "timeout":
      return (
        <Alert
          color="red"
          role="alert"
          data-testid="connect-server-probe-result"
          data-probe-kind="timeout"
        >
          Сервер не дождался ответа
        </Alert>
      );
  }
}

// ServerProbeResultView renders whatever the server-side call produced. A
// failed CALL (401/403/404/415 — the mutation itself rejected) is a
// different failure from a successful call reporting a bad OUTCOME
// (network-error/timeout/http-error inside the body, see
// handleProbeWorkspace's own doc comment for why the target's failure lives
// in the body and never in this route's own status) — the two get visibly
// different copy so "mocker's admin API is unreachable" is never confused
// with "mocker could not reach the workspace".
function ServerProbeResultView({ probe }: { probe: ServerProbe }) {
  if (probe.isError) {
    return (
      <Alert
        color="red"
        role="alert"
        data-testid="connect-server-probe-result"
        data-probe-kind="call-error"
      >
        Не удалось выполнить серверную проверку: {describeApiFailure(probe.error)}
      </Alert>
    );
  }
  const view = serverProbeResult(probe);
  if (view === null) {
    return null;
  }
  return <ServerProbeKindView view={view} />;
}

// diagnoseCertMismatch is DESIGN §14 screen 4's whole reason a server-side
// probe exists next to the browser's own: mocker reaching the workspace
// while THIS browser cannot is not one more ambiguous network error, it is
// specifically "this browser does not trust the contour's root certificate"
// (occasionally a CORS misconfiguration instead) — named outright rather
// than left for ProbeResultView's generic three-cause list to cover.
function diagnoseCertMismatch(browser: ProbeResult, server: ServerProbeView | null): boolean {
  return server?.kind === "ok" && (browser.kind === "network-error" || browser.kind === "timeout");
}

export function ConnectPanel({
  workspace,
  config,
}: {
  workspace: WorkspaceView;
  config: ServerConfigView;
}) {
  const [probing, setProbing] = useState(false);
  // null before the first press, on purpose — the result region must not exist
  // in the DOM at all until «Проверить» has actually been pressed once, not
  // merely render empty placeholder copy.
  const [result, setResult] = useState<ProbeResult | null>(null);
  const serverProbe = useProbeWorkspace();

  async function handleProbe(): Promise<void> {
    setProbing(true);
    // Both sides dial the same target at once (DESIGN §14 screen 4: "работает
    // с двух сторон"), not one after the other — a slow or hung side must not
    // delay the other's answer. allSettled() rather than Promise.all(): the
    // server call is a mutation that can legitimately reject (401/403/404 —
    // see ServerProbeResultView's own doc comment), and that must not throw
    // away the browser's own already-finished result.
    const [browserOutcome] = await Promise.allSettled([
      runProbe(`${workspace.url}${config.reservedPrefix}/health`, workspace.slug),
      serverProbe.mutateAsync({ id: workspace.id }),
    ]);
    // runProbe never itself rejects — every failure mode it can hit is
    // already a ProbeResult variant — so the rejected branch below is
    // defensive, not an expected path.
    setResult(
      browserOutcome.status === "fulfilled" ? browserOutcome.value : { kind: "network-error" },
    );
    setProbing(false);
  }

  const recipes = buildRecipes(workspace, config);
  const serverView = serverProbeResult(serverProbe);

  return (
    <Card withBorder p="md" data-testid="connect-panel">
      <Stack gap="lg">
        <div>
          <Title order={2} mb="xs">
            Адрес воркспейса
          </Title>
          {/* ws.url exactly as the server sent it — never rebuilt from
              window.location and the slug: the admin host is forbidden from
              sitting under the base domain, and the port comes from the request
              the server already saw. */}
          <CopyField testId="connect-address-copy" label="Адрес" value={workspace.url} />
          {workspace.settings.basePath !== "" ? (
            // The address a FRONTEND needs is origin + basePath (apiAddress);
            // the origin alone 404s on every route of a workspace whose spec
            // declared a servers[] prefix. Shown only when there is a base
            // path, so the common case stays one field.
            <Stack gap={4} mt="xs" data-testid="connect-api-address">
              <CopyField
                testId="connect-api-address-copy"
                label="Адрес API (с базовым путём)"
                value={apiAddress(workspace)}
              />
              <Text size="xs" c="dimmed">
                Базовый путь {workspace.settings.basePath} — из спеки; фронтенд, который добавляет
                его сам, настраивают на «Адрес» выше.
                {workspace.settings.basePath.includes("{")
                  ? " Параметр в фигурных скобках подставляет клиент; допустимые значения — в настройках."
                  : ""}
              </Text>
            </Stack>
          ) : null}
        </div>

        <Stack gap="sm">
          <Title order={2}>Как подключить фронтенд</Title>
          <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="sm">
            {recipes.map((recipe) => (
              <RecipeCard key={recipe.id} recipe={recipe} />
            ))}
          </SimpleGrid>
        </Stack>

        <RateIndicator workspaceId={workspace.id} />

        <Stack gap="xs">
          <Button
            w="fit-content"
            leftSection={<IconPlayerPlay size={16} />}
            loading={probing}
            onClick={() => void handleProbe()}
            data-testid="connect-probe-button"
          >
            {probing ? "Проверяем…" : "Проверить"}
          </Button>
          {result !== null ? (
            <Stack gap="xs">
              <ProbeResultView result={result} />
              <ServerProbeResultView probe={serverProbe} />
              {diagnoseCertMismatch(result, serverView) ? (
                <Alert
                  color="yellow"
                  role="alert"
                  data-testid="connect-probe-diagnosis"
                  title="Сервер видит воркспейс, этот браузер — нет"
                >
                  Похоже, в этом браузере не установлен корневой сертификат контура (или неправильно
                  настроен CORS для этого источника) — сервер до воркспейса достучался, а браузер
                  нет.
                </Alert>
              ) : null}
            </Stack>
          ) : null}
        </Stack>
      </Stack>
    </Card>
  );
}
