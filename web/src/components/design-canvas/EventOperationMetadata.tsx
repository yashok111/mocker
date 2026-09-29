import { useMemo, useState } from "react";
import { Alert, Button, Group, NativeSelect, Stack, Text } from "@mantine/core";
import { listCanvasOperations } from "./canvasOperations";
import { readDiagrams } from "../state-diagram/model";
import type { CanvasDocument } from "./types";
import type { EventOperation } from "./eventTypes";

type APILink = NonNullable<EventOperation["apiLinks"]>[number];
type StateLink = NonNullable<EventOperation["stateLinks"]>[number];
type Choice<T> = { value: string; label: string; link: T };

function operationChoices(document: CanvasDocument): Choice<APILink>[] {
  return document.contracts.flatMap((contract) =>
    listCanvasOperations(contract.document).flatMap((item) =>
      typeof item.key === "string"
        ? [
            {
              value: JSON.stringify([contract.id, item.key]),
              label: `${contract.name} · ${item.location.method.toUpperCase()} ${item.location.path}`,
              link: { contractId: contract.id, operationKey: item.key },
            },
          ]
        : [],
    ),
  );
}

function transitionChoices(document: CanvasDocument): Choice<StateLink>[] {
  return document.contracts.flatMap((contract) => {
    try {
      return readDiagrams(contract.document).flatMap((diagram) =>
        diagram.transitions.map((transition) => ({
          value: JSON.stringify([contract.id, diagram.id, transition.id]),
          label: `${contract.name} · ${diagram.name} · ${transition.name || transition.id}`,
          link: { contractId: contract.id, diagramId: diagram.id, transitionId: transition.id },
        })),
      );
    } catch {
      return [];
    }
  });
}

export function EventOperationMetadata({
  document,
  operation,
  onPatch,
}: {
  document: CanvasDocument;
  operation: EventOperation;
  onPatch: (patch: Partial<EventOperation>) => void;
}) {
  const [apiSelection, setAPISelection] = useState("");
  const [stateSelection, setStateSelection] = useState("");
  const apiChoices = useMemo(() => operationChoices(document), [document]);
  const stateChoices = useMemo(() => transitionChoices(document), [document]);
  const channels = document.eventModel?.channels ?? [];
  const routes = operation.failureRoutes;
  const routeChoices = (other?: string, current?: string) => {
    const available = channels
      .filter((channel) => channel.id !== operation.channelId && channel.id !== other)
      .map((channel) => ({
        value: channel.id,
        label: `${channel.name || channel.id} · ${channel.address}`,
      }));
    return [
      { value: "", label: "Не задан" },
      ...available,
      ...(current && !available.some((item) => item.value === current)
        ? [{ value: current, label: `Недоступный topic · ${current}` }]
        : []),
    ];
  };
  const updateRoute = (field: "retryChannelId" | "deadLetterChannelId", value: string) => {
    const next = { ...routes, [field]: value || undefined };
    onPatch({
      failureRoutes: next.retryChannelId || next.deadLetterChannelId ? next : undefined,
    });
  };
  const addAPI = () => {
    const choice = apiChoices.find((item) => item.value === apiSelection);
    if (
      !choice ||
      (operation.apiLinks ?? []).some(
        (item) =>
          item.contractId === choice.link.contractId &&
          item.operationKey === choice.link.operationKey,
      )
    )
      return;
    onPatch({ apiLinks: [...(operation.apiLinks ?? []), choice.link] });
    setAPISelection("");
  };
  const addState = () => {
    const choice = stateChoices.find((item) => item.value === stateSelection);
    if (
      !choice ||
      (operation.stateLinks ?? []).some(
        (item) =>
          item.contractId === choice.link.contractId &&
          item.diagramId === choice.link.diagramId &&
          item.transitionId === choice.link.transitionId,
      )
    )
      return;
    onPatch({ stateLinks: [...(operation.stateLinks ?? []), choice.link] });
    setStateSelection("");
  };
  return (
    <Stack gap="xs">
      {operation.action === "receive" ? (
        <>
          <Text fw={600} size="sm">
            Маршруты при ошибке обработки
          </Text>
          <Text size="xs" c="dimmed">
            Это объявленная топология. Доставка, задержка и создание topic здесь не выполняются.
          </Text>
          <Group grow align="end">
            <NativeSelect
              label="Retry topic"
              value={routes?.retryChannelId ?? ""}
              data={routeChoices(routes?.deadLetterChannelId, routes?.retryChannelId)}
              onChange={(event) => updateRoute("retryChannelId", event.currentTarget.value)}
            />
            <NativeSelect
              label="Dead-letter topic"
              value={routes?.deadLetterChannelId ?? ""}
              data={routeChoices(routes?.retryChannelId, routes?.deadLetterChannelId)}
              onChange={(event) => updateRoute("deadLetterChannelId", event.currentTarget.value)}
            />
          </Group>
        </>
      ) : null}
      <Text fw={600} size="sm">
        Связанные операции API
      </Text>
      {(operation.apiLinks ?? []).map((link) => {
        const value = JSON.stringify([link.contractId, link.operationKey]);
        const choice = apiChoices.find((item) => item.value === value);
        return (
          <Group key={value} justify="space-between" wrap="wrap">
            <Text size="sm">
              {choice?.label ?? `${link.contractId} · ${link.operationKey} (цель недоступна)`}
            </Text>
            <Button
              size="xs"
              variant="subtle"
              color="red"
              onClick={() =>
                onPatch({ apiLinks: operation.apiLinks?.filter((item) => item !== link) })
              }
            >
              Удалить связь API
            </Button>
          </Group>
        );
      })}
      <Group align="end" wrap="wrap">
        <NativeSelect
          label="Операция API для связи"
          value={apiSelection}
          data={[{ value: "", label: "Выберите операцию" }, ...apiChoices]}
          onChange={(event) => setAPISelection(event.currentTarget.value)}
          style={{ flex: "1 1 220px" }}
        />
        <Button
          size="xs"
          variant="light"
          disabled={!apiSelection || (operation.apiLinks?.length ?? 0) >= 100}
          onClick={addAPI}
        >
          Добавить связь API
        </Button>
      </Group>
      <Text fw={600} size="sm">
        Связанные переходы состояний
      </Text>
      {(operation.stateLinks ?? []).map((link) => {
        const value = JSON.stringify([link.contractId, link.diagramId, link.transitionId]);
        const choice = stateChoices.find((item) => item.value === value);
        return (
          <Group key={value} justify="space-between" wrap="wrap">
            <Text size="sm">
              {choice?.label ??
                `${link.contractId} · ${link.diagramId} · ${link.transitionId} (цель недоступна)`}
            </Text>
            <Button
              size="xs"
              variant="subtle"
              color="red"
              onClick={() =>
                onPatch({ stateLinks: operation.stateLinks?.filter((item) => item !== link) })
              }
            >
              Удалить связь с переходом
            </Button>
          </Group>
        );
      })}
      <Group align="end" wrap="wrap">
        <NativeSelect
          label="Переход для связи"
          value={stateSelection}
          data={[{ value: "", label: "Выберите переход" }, ...stateChoices]}
          onChange={(event) => setStateSelection(event.currentTarget.value)}
          style={{ flex: "1 1 220px" }}
        />
        <Button
          size="xs"
          variant="light"
          disabled={!stateSelection || (operation.stateLinks?.length ?? 0) >= 100}
          onClick={addState}
        >
          Добавить связь с переходом
        </Button>
      </Group>
      {document.contracts.length === 0 ? (
        <Alert color="gray">Для связей добавьте HTTP-контракт в сценарий.</Alert>
      ) : null}
    </Stack>
  );
}
