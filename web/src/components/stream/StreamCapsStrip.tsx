import type { ReactElement } from "react";
import { useState } from "react";
import { Alert, Button, Card, Group, Stack, Table, Text } from "@mantine/core";
import { IconAlertTriangle, IconCalculator } from "@tabler/icons-react";
import { usePreviewEndpoint } from "@/api/generated/endpoints/endpoints.ts";
import type { ServerConfigViewLimits, StreamPreviewView } from "@/api/generated/schemas";
import { describeApiFailureDetailed } from "@/api/errors";
import { formatBytes, formatBytesPerSec } from "@/format";
import { STREAM_CAPS, draftToDefinition, type StreamDraft, type StreamKind } from "./streamDraft";

// StreamCapsStrip is not part of the editor and never was: it reads the
// draft, asks the server to price it and shows nothing the form can change.
// It shared StreamEditor.tsx only because both arrived in P6e.

/** StreamCapsStrip is §30.14's read-only strip: the server's effective caps
 * and, on request, the draft's own first frames and the maximum output one
 * connection would produce — POST .../endpoints/preview writes nothing, so
 * pressing the button is free. The number is the amplifier §30.12 wants
 * shown BEFORE a loop is saved: a 4 MiB frame every 100 ms is 40 MiB/s per
 * connection, and the cap on connections multiplies it. Both formatters it
 * renders with live in @/format now — this file's rule is the one that
 * survived the merge with AssetsPage's. */
export function StreamCapsStrip({
  workspaceId,
  path,
  kind,
  draft,
  limits,
  testIdPrefix,
}: {
  workspaceId: number;
  path: string;
  kind: StreamKind;
  draft: StreamDraft;
  /** The server's effective limits (A9, from the session's config). Absent
   * — a component mounted without a session — the strip names the
   * variables instead of inventing numbers. */
  limits?: ServerConfigViewLimits;
  testIdPrefix: string;
}): ReactElement {
  const t = (name: string) => `${testIdPrefix}-${name}`;
  const [result, setResult] = useState<StreamPreviewView | null>(null);
  const [draftError, setDraftError] = useState<string | null>(null);
  const preview = usePreviewEndpoint();

  function run(): void {
    const def = draftToDefinition(kind, draft);
    if ("error" in def) {
      setDraftError(def.error);
      setResult(null);
      return;
    }
    setDraftError(null);
    preview.mutate(
      {
        id: workspaceId,
        data: { method: "GET", path: path.trim() || "/", kind, stream: def.stream },
      },
      { onSuccess: (res) => setResult(res.status === 200 ? res.data : null) },
    );
  }

  return (
    <Card withBorder p="sm" data-testid={t("caps")}>
      <Stack gap="xs">
        <Text size="xs" c="dimmed" data-testid={t("caps-text")}>
          Лимиты сервера: до {STREAM_CAPS.maxFrames} кадров в расписании, пауза до{" "}
          {STREAM_CAPS.maxFrameDelayMs.toLocaleString("ru-RU")} мс, интервал от{" "}
          {STREAM_CAPS.minTickIntervalMs} мс, до {STREAM_CAPS.maxRules} правил
          {limits
            ? `, кадр не больше ${formatBytes(limits.maxResponseBytes)}. Соединений на воркспейс — ${limits.streamMaxConns}, время жизни — ${limits.streamMaxLifetimeSec} с` +
              (kind === "ws"
                ? `, входящий кадр до ${formatBytes(limits.streamMaxFrameBytes)}, очередь ответов ${formatBytes(limits.streamSendBudgetBytes)}`
                : "")
            : ", кадр не больше MOCKER_MAX_RESPONSE. Соединений на воркспейс — MOCKER_STREAM_MAX_CONNS, время жизни — MOCKER_STREAM_MAX_LIFETIME"}
          .
        </Text>
        <Group gap="sm">
          <Button
            variant="default"
            size="xs"
            leftSection={<IconCalculator size={14} />}
            loading={preview.isPending}
            onClick={run}
            data-testid={t("preview-run")}
          >
            Рассчитать кадры
          </Button>
          {result ? (
            <Text size="sm" data-testid={t("preview-rate")}>
              {result.nominalRate
                ? "Оценка по одному запуску функции, не потолок: "
                : "Максимум на одно соединение: "}
              <strong>{formatBytesPerSec(result.maxBytesPerSec)}</strong>
              {result.truncated ? " · показаны первые кадры, поток длиннее" : ""}
              {kind === "ws" ? ` · правил: ${result.rules}${result.echo ? ", эхо" : ""}` : ""}
            </Text>
          ) : null}
        </Group>
        {draftError ? (
          <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
            {draftError}
          </Alert>
        ) : null}
        {preview.isError ? (
          <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
            {describeApiFailureDetailed(preview.error)}
          </Alert>
        ) : null}
        {result && result.frames.length > 0 ? (
          <Table.ScrollContainer minWidth={400} mah={240}>
            <Table fz="xs" data-testid={t("preview-frames")}>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>мс</Table.Th>
                  <Table.Th>событие</Table.Th>
                  <Table.Th>данные</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {result.frames.map((f, i) => (
                  // eslint-disable-next-line react/no-array-index-key
                  <Table.Tr key={i}>
                    <Table.Td>{f.atMs}</Table.Td>
                    <Table.Td>{f.event ?? "—"}</Table.Td>
                    <Table.Td>
                      {f.notRun ? (
                        <Text size="xs" c="dimmed" component="span">
                          тело не вычислялось
                        </Text>
                      ) : (
                        <code>{JSON.stringify(f.data)}</code>
                      )}
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        ) : null}
      </Stack>
    </Card>
  );
}
