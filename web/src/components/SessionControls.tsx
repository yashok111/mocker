import { useState } from "react";
import type { ReactElement } from "react";
import { Alert, Badge, Button, Card, Group, Loader, NumberInput, Stack, Text } from "@mantine/core";
import { modals } from "@mantine/modals";
import {
  IconAlertTriangle,
  IconBan,
  IconBolt,
  IconClock,
  IconEraser,
  IconPlayerPause,
} from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import {
  getListSessionDirectivesQueryKey,
  useClearSessionDirectives,
  useListSessionDirectives,
  useSetSessionDirective,
} from "@/api/generated/session/session.ts";
import type { Directive, DirectiveTarget } from "@/api/generated/schemas";
import { describeApiFailure } from "@/api/errors";

// SessionControls is the session-directive strip DESIGN §14 screen 5 puts on
// the same page as the operation editor (§3.4/§12 of the phase context):
// «сломать следующий запрос», «отвечать 503», «задержать», «поставить на
// паузу», and a clear-all. These live entirely in RAM on the mock plane's
// server process — they die on restart and, unlike everything else on this
// screen, never bump the workspace revision, which is why §3.9 has them
// invalidate ONLY the session-directive list and nothing else. That
// difference is also why it is said out loud on screen rather than left to
// be discovered the first time a restart erases what looked like a saved
// setting.
//
// "status", "fail", "delay" and "pause" are the four actions the server
// accepts (internal/admin/livestate_handlers.go, mirrored byte-for-byte by
// the mock plane's own POST {prefix}/state). `scenario` is no longer a 501
// on either plane (P2b): the mock plane's own {prefix}/state now activates/
// deactivates by name, and this admin route answers 400 for it instead
// (A17 — screen 9's dedicated activate/deactivate routes are what a client
// is told to use). This component still does not offer scenario switching
// — that is screen 9 («Сценарии»), not a directive on this strip — so only
// the REASON changed, not the omission itself.
export function SessionControls({
  id,
  target,
}: {
  id: number;
  target: { method: string; path: string } | null;
}): ReactElement {
  const directives = useListSessionDirectives(id);
  const queryClient = useQueryClient();
  const [failStatus, setFailStatus] = useState<number | "">(500);
  const [delayMs, setDelayMs] = useState<number | "">(300);

  const wireTarget: DirectiveTarget = target === null ? "*" : target;
  const targetLabel = target === null ? "весь воркспейс" : `${target.method} ${target.path}`;

  function invalidate(): void {
    void queryClient.invalidateQueries({ queryKey: getListSessionDirectivesQueryKey(id) });
  }

  const setDirective = useSetSessionDirective({
    mutation: { onSuccess: () => invalidate() },
  });
  const clearDirectives = useClearSessionDirectives({
    mutation: { onSuccess: () => invalidate() },
  });

  function handleFailNext(): void {
    const status = failStatus === "" ? 500 : failStatus;
    const directive: Directive = { target: wireTarget, action: "fail", status, once: true };
    setDirective.mutate({ id, data: directive });
  }

  function handleForce503(): void {
    const directive: Directive = { target: wireTarget, action: "status", status: 503 };
    setDirective.mutate({ id, data: directive });
  }

  function handleDelay(): void {
    const ms = delayMs === "" ? 300 : delayMs;
    const directive: Directive = { target: wireTarget, action: "delay", ms };
    setDirective.mutate({ id, data: directive });
  }

  function handlePause(): void {
    const directive: Directive = { target: wireTarget, action: "pause" };
    setDirective.mutate({ id, data: directive });
  }

  function handleClearAll(): void {
    modals.openConfirmModal({
      title: "Очистить директивы сессии",
      children: (
        <Text size="sm">
          Убрать все директивы сессии для этого воркспейса (для всех целей, не только для «
          {targetLabel}»)? Мок вернётся к обычным ответам.
        </Text>
      ),
      labels: { confirm: "Очистить", cancel: "Отмена" },
      confirmProps: { color: "red", "data-testid": "session-clear-confirm" },
      onConfirm: () => clearDirectives.mutate({ id }),
    });
  }

  return (
    <Card withBorder p="md" data-testid="session-controls">
      <Stack gap="sm">
        <Group justify="space-between">
          <Text fw={600} size="sm">
            Session — цель: {targetLabel}
          </Text>
        </Group>
        <Text size="xs" c="dimmed">
          Директивы живут только в памяти процесса mocker: они пропадают при перезапуске и, в
          отличие от остального на этой странице, не двигают ревизию воркспейса.
        </Text>

        {setDirective.isError ? (
          <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
            {describeApiFailure(setDirective.error)}
          </Alert>
        ) : null}
        {clearDirectives.isError ? (
          <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
            {describeApiFailure(clearDirectives.error)}
          </Alert>
        ) : null}

        <Group align="flex-end" wrap="wrap">
          <NumberInput
            label="Статус для «сломать следующий запрос»"
            min={100}
            max={599}
            w={220}
            data-testid="session-fail-status"
            value={failStatus}
            onChange={(v) => setFailStatus(typeof v === "number" ? v : v === "" ? "" : Number(v))}
          />
          <Button
            variant="default"
            leftSection={<IconBolt size={16} />}
            loading={setDirective.isPending}
            onClick={handleFailNext}
            data-testid="session-fail-next"
          >
            Сломать следующий запрос
          </Button>
          <Button
            variant="default"
            leftSection={<IconBan size={16} />}
            loading={setDirective.isPending}
            onClick={handleForce503}
            data-testid="session-force-503"
          >
            Отвечать 503
          </Button>
          <Button
            variant="default"
            color="red"
            leftSection={<IconEraser size={16} />}
            loading={clearDirectives.isPending}
            onClick={handleClearAll}
            data-testid="session-clear-all"
          >
            Очистить всё
          </Button>
        </Group>

        <Group align="flex-end" wrap="wrap">
          <NumberInput
            label="Задержка (мс)"
            min={1}
            max={30000}
            w={220}
            data-testid="session-delay-ms"
            value={delayMs}
            onChange={(v) => setDelayMs(typeof v === "number" ? v : v === "" ? "" : Number(v))}
          />
          <Button
            variant="default"
            leftSection={<IconClock size={16} />}
            loading={setDirective.isPending}
            onClick={handleDelay}
            data-testid="session-delay"
          >
            Задержать ответ
          </Button>
          <Button
            variant="default"
            leftSection={<IconPlayerPause size={16} />}
            loading={setDirective.isPending}
            onClick={handlePause}
            data-testid="session-pause"
          >
            Поставить на паузу
          </Button>
        </Group>
        <Text size="xs" c="dimmed">
          Задержка добавляется один раз перед обычным ответом. Пауза держит ответ, пока директиву не
          очистят или пока сервер сам не отпустит его по истечении времени ожидания — как и
          остальные директивы сессии, обе пропадают при перезапуске.
        </Text>

        {directives.isPending ? (
          <Group gap="xs">
            <Loader size="sm" />
            <Text size="sm" component="output">
              Загрузка…
            </Text>
          </Group>
        ) : directives.isError ? (
          <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
            {describeApiFailure(directives.error)}
          </Alert>
        ) : directives.data.status !== 200 ? (
          <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
            {describeApiFailure(null)}
          </Alert>
        ) : directives.data.data.directives.length === 0 ? (
          <Text size="xs" c="dimmed" data-testid="session-directives-empty">
            Сейчас ни одной директивы не установлено
          </Text>
        ) : (
          <Stack gap={4} data-testid="session-directives-list">
            {directives.data.data.directives.map((d) => (
              <Group
                key={`${directiveTargetLabel(d.target)}:${d.action}`}
                gap="xs"
                data-testid="session-directive-row"
              >
                <Badge size="sm" variant="light">
                  {directiveTargetLabel(d.target)}
                </Badge>
                <Text size="xs">
                  {d.action === "fail"
                    ? `сломать ${d.status} — осталось ${d.n ?? 0} запрос(ов)`
                    : d.action === "status"
                      ? `отвечать ${d.status} до очистки`
                      : d.action === "delay"
                        ? `задержка ${d.ms} мс`
                        : `пауза до очистки`}
                </Text>
              </Group>
            ))}
          </Stack>
        )}
      </Stack>
    </Card>
  );
}

function directiveTargetLabel(target: DirectiveTarget): string {
  return target === "*" ? "*" : `${target.method} ${target.path}`;
}
