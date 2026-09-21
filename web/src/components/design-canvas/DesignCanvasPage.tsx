import { lazy, Suspense, useRef, useState, type ReactElement, type ReactNode } from "react";
import {
  ActionIcon,
  Alert,
  Button,
  Group,
  Loader,
  Menu,
  Text,
  TextInput,
  Tooltip,
  UnstyledButton,
} from "@mantine/core";
import { useMediaQuery } from "@mantine/hooks";
import {
  IconArrowBackUp,
  IconArrowForwardUp,
  IconBracketsContain,
  IconDeviceFloppy,
  IconFilePlus,
  IconListDetails,
  IconPlayerPlay,
  IconPlus,
  IconRoute,
  IconBox,
  IconCloudUpload,
  IconX,
} from "@tabler/icons-react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { describeApiFailureDetailed } from "@/api/errors";
import { CanvasInspector } from "./CanvasInspector";
import { CanvasApiPicker } from "./CanvasApiPicker";
import { createCanvasId } from "./canvasId";
import {
  emptyCanvas,
  exampleCanvas,
  moveMessage,
  moveParticipant,
  removeMessage,
  removeParticipant,
} from "./canvasModel";
import { useCanvasDraft, type CanvasDraftController } from "./useCanvasDraft";
import { designScenarioKeys, useCreateDesignScenario } from "./designScenarioApi";
import { messageLabels, participantLabels } from "./labels";
import type { CanvasContract, CanvasDocument, CanvasSelection } from "./types";
import classes from "./DesignCanvas.module.css";

const SequenceGraph = lazy(() => import("./SequenceGraph"));

export interface CanvasPersistenceControls {
  automatic?: boolean;
  status?: ReactNode;
  saveLabel?: string;
  saving?: boolean;
  saveDisabled?: boolean;
  onSave?: () => void;
  notice?: ReactNode;
  actions?: ReactNode;
}

export function DesignCanvasPage(): ReactElement {
  const draft = useCanvasDraft();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const submittedTransferFingerprint = useRef("");
  const [transferredScenarioID, setTransferredScenarioID] = useState<number | null>(null);
  const transfer = useCreateDesignScenario({
    onSuccess: (response) => {
      void queryClient.invalidateQueries({ queryKey: designScenarioKeys.all });
      if (draft.fingerprint !== submittedTransferFingerprint.current) {
        setTransferredScenarioID(response.data.scenario.id);
        return;
      }
      void navigate({
        to: "/design-scenarios/$id" as never,
        params: { id: response.data.scenario.id } as never,
        ignoreBlocker: true,
      });
    },
  });

  return (
    <DesignCanvasEditor
      draft={draft}
      persistence={{
        actions: (
          <Button
            size="sm"
            variant="default"
            leftSection={<IconCloudUpload size={16} />}
            loading={transfer.isPending}
            onClick={() => {
              submittedTransferFingerprint.current = draft.fingerprint;
              transfer.mutate({
                document: draft.document,
                formDrafts: { all: draft.serializeFormDrafts() },
                summary: "Перенос локального сценария",
              });
            }}
          >
            Перенести на сервер
          </Button>
        ),
        notice: (
          <>
            {transfer.isError ? (
              <Alert color="red" role="alert">
                {describeApiFailureDetailed(transfer.error)}
              </Alert>
            ) : null}
            {transferredScenarioID !== null ? (
              <Alert color="blue">
                Серверный сценарий создан, а новые локальные правки остались в этой вкладке.{" "}
                <Link
                  to="/design-scenarios/$id"
                  params={{ id: transferredScenarioID }}
                  target="_blank"
                >
                  Открыть созданный сценарий
                </Link>
              </Alert>
            ) : null}
          </>
        ),
      }}
    />
  );
}

export function DesignCanvasEditor({
  draft,
  persistence,
}: {
  draft: CanvasDraftController;
  persistence?: CanvasPersistenceControls;
}): ReactElement {
  const document = draft.document;
  const [requestedSelection, setSelection] = useState<CanvasSelection>(null);
  const selectedItems =
    requestedSelection?.kind === "participant"
      ? document.participants
      : requestedSelection?.kind === "message"
        ? document.messages
        : document.fragments;
  const selection = selectedItems.some((item) => item.id === requestedSelection?.id)
    ? requestedSelection
    : null;
  const [outline, setOutline] = useState(false);
  const [apiPicker, setApiPicker] = useState(false);
  const narrow = useMediaQuery("(max-width: 1023px)", false, { getInitialValueInEffect: false });

  if (narrow) {
    return (
      <Alert title="Проектировщик рассчитан на большой экран" color="gray">
        Откройте его на компьютере и увеличьте ширину окна хотя бы до 1024 px.
      </Alert>
    );
  }

  function guarded(change: () => CanvasDocument): void {
    try {
      draft.update(change());
      draft.setError("");
    } catch (error) {
      draft.setError(error instanceof Error ? error.message : "Не удалось изменить сценарий.");
    }
  }

  function addParticipant(): void {
    const id = createCanvasId();
    draft.update({
      ...document,
      participants: [
        ...document.participants,
        {
          id,
          name: `Объект ${document.participants.length + 1}`,
          kind: "service",
          description: "",
        },
      ],
    });
    setSelection({ kind: "participant", id });
  }

  function addMessage(): void {
    const first = document.participants[0];
    if (!first) return;
    const selected =
      selection?.kind === "participant"
        ? document.participants.find((item) => item.id === selection.id)
        : undefined;
    const from = selected ?? first;
    const to = document.participants.find((item) => item.id !== from.id) ?? from;
    const id = createCanvasId();
    draft.update({
      ...document,
      messages: [
        ...document.messages,
        {
          id,
          fromId: from.id,
          toId: to.id,
          kind: "request",
          label: "Новый вызов",
          description: "",
        },
      ],
    });
    setSelection({ kind: "message", id });
  }

  function addFragment(): void {
    const first = document.messages[0];
    const last = document.messages.at(-1);
    if (!first || !last) return;
    const id = createCanvasId();
    const from = selection?.kind === "message" ? selection.id : first.id;
    draft.update({
      ...document,
      fragments: [
        ...document.fragments,
        { id, kind: "opt", label: "Условие выполнения", fromMessageId: from, toMessageId: last.id },
      ],
    });
    setSelection({ kind: "fragment", id });
  }

  function deleteSelection(): void {
    if (!selection) return;
    if (
      selection.kind === "participant" &&
      document.messages.some(
        (item) => item.fromId === selection.id || item.toId === selection.id,
      ) &&
      !window.confirm("Удалить объект и все связанные сообщения?")
    )
      return;
    guarded(() =>
      selection.kind === "participant"
        ? removeParticipant(document, selection.id)
        : selection.kind === "message"
          ? removeMessage(document, selection.id)
          : {
              ...document,
              fragments: document.fragments.filter((item) => item.id !== selection.id),
            },
    );
    setSelection(null);
  }

  function moveSelection(offset: number): void {
    if (selection?.kind === "participant")
      guarded(() =>
        moveParticipant(
          document,
          selection.id,
          document.participants.findIndex((item) => item.id === selection.id) + offset,
        ),
      );
    if (selection?.kind === "message")
      guarded(() =>
        moveMessage(
          document,
          selection.id,
          document.messages.findIndex((item) => item.id === selection.id) + offset,
        ),
      );
  }

  function importApi(contract: CanvasContract): void {
    draft.update((current) => ({ ...current, contracts: [...current.contracts, contract] }));
  }

  const inspector = (
    <CanvasInspector
      key={selection ? `${selection.kind}:${selection.id}` : "none"}
      document={document}
      selection={selection}
      onChange={draft.update}
      onClose={() => setSelection(null)}
      onDelete={deleteSelection}
      onMove={moveSelection}
      onImportApi={() => setApiPicker(true)}
      formStore={draft.formStore}
    />
  );

  return (
    <section className={classes.page} data-testid="design-canvas-page">
      <header className={classes.header}>
        <Group gap="sm" wrap="nowrap" className={classes.heading}>
          <span className={classes.mark}>
            <IconRoute size={23} stroke={1.5} />
          </span>
          <div className={classes.titleGroup}>
            <Text size="xs" c="dimmed" tt="uppercase" lts="0.08em" fw={650}>
              Сценарии взаимодействия
            </Text>
            <TextInput
              variant="unstyled"
              aria-label="Название сценария"
              value={document.title}
              classNames={{ input: classes.titleInput }}
              onChange={(event) => draft.update({ ...document, title: event.currentTarget.value })}
            />
          </div>
        </Group>
        <div className={classes.headerActions}>
          <Group gap="xs" wrap="nowrap">
            <Text component="output" aria-label="Состояние сохранения" size="xs" c="dimmed">
              {persistence?.status ??
                (draft.saved
                  ? "Сохранено в браузере"
                  : draft.dirty
                    ? "Есть несохранённые изменения"
                    : "Локальный прототип")}
            </Text>
            {persistence?.actions}
            {!persistence?.automatic ? (
              <Button
                size="sm"
                leftSection={<IconDeviceFloppy size={16} />}
                loading={persistence?.saving}
                disabled={persistence?.saveDisabled}
                onClick={persistence?.onSave ?? draft.save}
              >
                {persistence?.saveLabel ?? "Сохранить в браузере"}
              </Button>
            ) : null}
          </Group>
          <Group gap={4} wrap="nowrap" className={classes.historyTools}>
            <Tooltip label="Отменить · ⌘/Ctrl Z">
              <ActionIcon
                variant="default"
                size={36}
                aria-label="Отменить"
                disabled={!draft.canUndo}
                onClick={draft.undo}
              >
                <IconArrowBackUp size={18} />
              </ActionIcon>
            </Tooltip>
            <Tooltip label="Повторить · ⌘/Ctrl Shift Z">
              <ActionIcon
                variant="default"
                size={36}
                aria-label="Повторить"
                disabled={!draft.canRedo}
                onClick={draft.redo}
              >
                <IconArrowForwardUp size={18} />
              </ActionIcon>
            </Tooltip>
            <Tooltip label="Структура сценария">
              <ActionIcon
                variant={outline ? "light" : "subtle"}
                size={36}
                aria-label="Структура сценария"
                aria-pressed={outline}
                onClick={() => setOutline(!outline)}
              >
                <IconListDetails size={18} />
              </ActionIcon>
            </Tooltip>
            {!persistence?.automatic ? (
              <>
                <Button
                  variant="subtle"
                  color="gray"
                  size="xs"
                  h={36}
                  leftSection={<IconFilePlus size={18} />}
                  onClick={() => {
                    if (draft.reset(emptyCanvas())) setSelection(null);
                  }}
                >
                  С нуля
                </Button>
                <Button
                  variant="subtle"
                  color="gray"
                  size="xs"
                  h={36}
                  leftSection={<IconPlayerPlay size={18} />}
                  onClick={() => {
                    if (draft.reset(exampleCanvas())) setSelection(null);
                  }}
                >
                  Пример
                </Button>
              </>
            ) : null}
          </Group>
        </div>
      </header>

      {persistence?.notice}

      <div className={classes.workspace} data-inspecting={selection !== null || undefined}>
        <div className={classes.canvasArea}>
          <Suspense
            fallback={
              <div className={classes.loading}>
                <Loader aria-label="Загружаем канвас" />
              </div>
            }
          >
            <SequenceGraph
              document={document}
              selection={selection}
              onSelect={setSelection}
              onMoveParticipant={(id, index) => guarded(() => moveParticipant(document, id, index))}
              onMoveMessage={(id, index) => guarded(() => moveMessage(document, id, index))}
            />
          </Suspense>
          {!document.participants.length ? (
            <div className={classes.empty}>
              <IconRoute size={38} stroke={1.2} />
              <Text fw={650} size="lg">
                Добавьте объекты сценария
              </Text>
              <Text c="dimmed" size="sm" maw={340} ta="center">
                Добавьте приложение, сервис, базу данных или пользователя, затем свяжите их
                сообщениями.
              </Text>
              <Button onClick={addParticipant} mt="sm">
                Добавить первый объект
              </Button>
            </div>
          ) : null}
          <div className={classes.topOverlay}>
            <Menu position="bottom-start" width={260} shadow="md" withinPortal>
              <Menu.Target>
                <ActionIcon
                  variant="default"
                  size={44}
                  radius="md"
                  aria-label="Добавить на канвас"
                  title="Добавить на канвас"
                  className={classes.addButton}
                >
                  <IconPlus size={22} />
                </ActionIcon>
              </Menu.Target>
              <Menu.Dropdown>
                <Menu.Item
                  leftSection={<IconBox size={16} />}
                  title="Приложение, сервис, база данных или пользователь"
                  onClick={addParticipant}
                >
                  Добавить объект
                </Menu.Item>
                <Menu.Item
                  leftSection={<IconPlus size={16} />}
                  onClick={addMessage}
                  disabled={!document.participants.length}
                >
                  Добавить сообщение
                </Menu.Item>
                <Menu.Item
                  leftSection={<IconBracketsContain size={16} />}
                  onClick={addFragment}
                  disabled={!document.messages.length}
                >
                  Условный блок
                </Menu.Item>
                <Menu.Divider />
                <Menu.Item leftSection={<IconRoute size={16} />} onClick={() => setApiPicker(true)}>
                  Добавить существующий API
                </Menu.Item>
              </Menu.Dropdown>
            </Menu>

            {draft.error ? (
              <Alert color="red" role="alert" withCloseButton onClose={() => draft.setError("")}>
                {draft.error}
              </Alert>
            ) : null}
            {draft.pendingForms ? (
              <Alert color="yellow">
                <Group justify="space-between" gap="xs">
                  <Text size="sm">
                    Есть незавершённые поля API. Их текст сохранится вместе со сценарием; undo/redo
                    доступно после завершения ввода.
                  </Text>
                  <Button
                    color="yellow"
                    variant="subtle"
                    size="xs"
                    onClick={() => {
                      if (window.confirm("Удалить незавершённый текст полей API?"))
                        draft.formStore.clear();
                    }}
                  >
                    Сбросить незавершённые поля
                  </Button>
                </Group>
              </Alert>
            ) : null}
            {outline ? (
              <aside className={classes.outline} aria-label="Структура сценария">
                <Group justify="space-between" px="sm" py="xs">
                  <Text size="sm" fw={650}>
                    Структура
                  </Text>
                  <ActionIcon
                    variant="subtle"
                    size="sm"
                    aria-label="Закрыть структуру"
                    onClick={() => setOutline(false)}
                  >
                    <IconX size={15} />
                  </ActionIcon>
                </Group>
                <div className={classes.outlineScroll}>
                  <Text className={classes.sectionCaption}>Объекты</Text>
                  {document.participants.map((item) => (
                    <UnstyledButton
                      key={item.id}
                      className={classes.outlineItem}
                      data-selected={selection?.id === item.id || undefined}
                      onClick={() => {
                        setSelection({ kind: "participant", id: item.id });
                      }}
                    >
                      <Text size="sm" truncate>
                        {item.name || "Без имени"}
                      </Text>
                      <Text size="xs" c="dimmed">
                        {participantLabels[item.kind]}
                      </Text>
                    </UnstyledButton>
                  ))}
                  <Text className={classes.sectionCaption}>Сообщения</Text>
                  {document.messages.map((item, index) => (
                    <UnstyledButton
                      key={item.id}
                      className={classes.outlineItem}
                      data-selected={selection?.id === item.id || undefined}
                      onClick={() => {
                        setSelection({ kind: "message", id: item.id });
                      }}
                    >
                      <Text size="sm" truncate>
                        {index + 1}. {item.label || "Без названия"}
                      </Text>
                      <Text size="xs" c="dimmed">
                        {messageLabels[item.kind]}
                      </Text>
                    </UnstyledButton>
                  ))}
                  {document.fragments.length ? (
                    <Text className={classes.sectionCaption}>Блоки</Text>
                  ) : null}
                  {document.fragments.map((item) => (
                    <UnstyledButton
                      key={item.id}
                      className={classes.outlineItem}
                      onClick={() => {
                        setSelection({ kind: "fragment", id: item.id });
                      }}
                    >
                      <Text size="sm" truncate>
                        {item.kind} · {item.label}
                      </Text>
                    </UnstyledButton>
                  ))}
                </div>
              </aside>
            ) : null}
          </div>
        </div>
        {selection ? inspector : null}
      </div>
      {apiPicker ? (
        <CanvasApiPicker onClose={() => setApiPicker(false)} onImport={importApi} />
      ) : null}
    </section>
  );
}
