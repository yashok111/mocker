import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import {
  Alert,
  Badge,
  Button,
  Group,
  Loader,
  NativeSelect,
  Stack,
  Text,
  Textarea,
  TextInput,
  Title,
} from "@mantine/core";
import { describeApiFailureDetailed } from "@/api/errors";
import type { FormDraftStore } from "../api-designer/forms/formDraftStore";
import ResourceGraph from "./ResourceGraph";
import { getSavedResourceMap, previewResourceMap } from "./adapter";
import {
  editableResource,
  type Command,
  type Model,
  type Preview,
  type Relation,
  type Resource,
  type SavedMap,
} from "./model";
import styles from "./ResourceMap.module.css";
import { layoutDiagram } from "../diagram/elkLayout";
import { previewLayoutCommands } from "../diagram/previewLayout";
import { resourceLayoutInput } from "./layout";

const DRAFT_KEY = "/x-mocker-resource-map/editor";
type ResourceForm = {
  kind: "resource";
  id: string;
  name: string;
  service: string;
  description: string;
  x: string;
  y: string;
  create?: boolean;
};
type RelationForm = {
  kind: "relation";
  id: string;
  fromResourceId: string;
  toResourceId: string;
  label: string;
  create?: boolean;
};
type Form = ResourceForm | RelationForm;
type Props = {
  designId: number;
  document: string;
  blocked: boolean;
  formStore: FormDraftStore;
  onChange: (document: string) => void;
  onOperation: (path: string, method: string, sourcePointer?: string) => void;
  onSchema: (name: string) => void;
  onScenario?: (id: number) => void;
  onAddOperation?: () => void;
  onLayoutPendingChange?: (pending: boolean) => void;
};
function resourceForm(resource: Resource): ResourceForm {
  return {
    kind: "resource",
    id: resource.id,
    name: resource.name,
    service: resource.service,
    description: resource.description,
    x: String(resource.x),
    y: String(resource.y),
  };
}
function relationForm(relation: Relation): RelationForm {
  return { kind: "relation", ...relation };
}
function readForm(source: string | undefined): Form | undefined {
  if (!source) return;
  try {
    const parsed: unknown = JSON.parse(source);
    if (
      typeof parsed === "object" &&
      parsed !== null &&
      "kind" in parsed &&
      (parsed.kind === "resource" || parsed.kind === "relation")
    )
      return parsed as Form;
  } catch {
    /* A corrupt persisted form is handled by its store snapshot. */
  }
}
function coordinate(value: string): number {
  const number = Number(value);
  if (value.trim() === "" || !Number.isFinite(number) || number < -100000 || number > 100000)
    throw new Error("Координата должна быть числом от −100000 до 100000.");
  return number;
}
function fieldLimit(value: string, max: number, label: string): void {
  if ([...value].length > max) throw new Error(`${label}: не более ${max} символов.`);
}
function invalidateGeneration(ref: { current: number }): void {
  ref.current++;
}
function errorMessage(reason: unknown): string {
  return reason instanceof Error && !("code" in reason)
    ? reason.message
    : describeApiFailureDetailed(reason);
}

export default function ResourceMapEditor({
  designId,
  document,
  blocked,
  formStore,
  onChange,
  onOperation,
  onSchema,
  onScenario,
  onAddOperation,
  onLayoutPendingChange,
}: Props) {
  const snapshot = useSyncExternalStore(
    formStore.subscribe,
    formStore.getSnapshot,
    formStore.getSnapshot,
  );
  const draft = formStore.get(DRAFT_KEY);
  const [form, setForm] = useState<Form | undefined>(() => readForm(draft?.source));
  const [preview, setPreview] = useState<Preview & { source: string }>();
  const [saved, setSaved] = useState<SavedMap>();
  const [error, setError] = useState("");
  const [savedError, setSavedError] = useState("");
  const [busy, setBusy] = useState(false);
  const [retry, setRetry] = useState(0);
  const [layout, setLayout] = useState<{ source: string; proposal?: Preview }>();
  const [layoutError, setLayoutError] = useState("");
  const [layoutFit, setLayoutFit] = useState(0);
  const layoutController = useRef<AbortController | null>(null);
  const generation = useRef(0);
  const commandSequence = useRef(0);
  const commandOwner = useRef<number | null>(null);
  const currentDocument = useRef(document);
  useEffect(() => {
    currentDocument.current = document;
  }, [document]);
  const layoutActive = layout?.source === document && !blocked;
  const proposal = layoutActive ? layout.proposal : undefined;
  const model: Model | undefined =
    proposal?.model ?? (preview?.source === document ? preview.model : undefined);
  const savedMap = saved?.designId === designId ? saved : undefined;
  const pending = draft !== undefined;
  const otherDraft = snapshot.dirty && !pending;
  const selectedResource =
    form?.kind === "resource"
      ? model?.resources.find((resource) => resource.id === form.id)
      : undefined;
  const selectedRelation =
    form?.kind === "relation"
      ? model?.relations.find((relation) => relation.id === form.id)
      : undefined;

  useEffect(() => {
    const controller = new AbortController();
    const token = ++generation.current;
    layoutController.current?.abort();
    layoutController.current = null;
    // oxlint-disable-next-line react/set-state-in-effect -- Changing the request input cancels the external proposal and clears its transient state.
    setLayout(undefined);
    setLayoutError("");
    commandOwner.current = null;
    setBusy(false);
    if (!document || blocked) {
      return () => invalidateGeneration(generation);
    }
    void previewResourceMap(designId, { document }, controller.signal).then(
      (response) => {
        if (generation.current !== token) return;
        setPreview({ ...response, source: document });
        setError("");
      },
      (reason: unknown) => {
        if (controller.signal.aborted || generation.current !== token) return;
        setPreview(undefined);
        setError(errorMessage(reason));
      },
    );
    return () => {
      controller.abort();
      layoutController.current?.abort();
      invalidateGeneration(generation);
    };
  }, [designId, document, blocked, retry]);
  useEffect(() => {
    onLayoutPendingChange?.(layoutActive);
  }, [layoutActive, onLayoutPendingChange]);
  useEffect(() => () => onLayoutPendingChange?.(false), [onLayoutPendingChange]);
  useEffect(() => {
    const controller = new AbortController();
    void getSavedResourceMap(designId, controller.signal).then(setSaved, (reason: unknown) => {
      if (!controller.signal.aborted) setSavedError(errorMessage(reason));
    });
    return () => controller.abort();
  }, [designId]);

  function edit(next: Form) {
    if (layoutActive) return;
    generation.current++;
    setForm(next);
    setError("");
    formStore.set(DRAFT_KEY, {
      source: JSON.stringify(next),
      propertySource: draft?.propertySource ?? document,
    });
  }
  function discard(next?: Form) {
    generation.current++;
    layoutController.current?.abort();
    layoutController.current = null;
    setLayout(undefined);
    commandOwner.current = null;
    formStore.remove(DRAFT_KEY);
    setForm(next);
    setError("");
    setBusy(false);
  }
  function select(next: Form) {
    if (layoutActive) return;
    if (pending) {
      setError("Примените изменения или сбросьте ввод перед выбором другого ресурса или связи.");
      return;
    }
    setError("");
    setForm(next);
    if (next.create) edit(next);
  }
  async function submit(commands: Command[], nextForm?: Form) {
    if (
      busy ||
      layoutActive ||
      commandOwner.current !== null ||
      blocked ||
      otherDraft ||
      !model ||
      !preview
    )
      return;
    if (draft && draft.propertySource !== document) {
      setError(
        "Документ изменился после начала ввода. Скопируйте изменения и сбросьте ввод перед повторным применением.",
      );
      return;
    }
    const token = ++generation.current;
    const owner = ++commandSequence.current;
    commandOwner.current = owner;
    setBusy(true);
    setError("");
    try {
      // The server preview normalizes missing operation keys. Carry its document
      // into commands so successive edits keep the same operation identity.
      const response = await previewResourceMap(designId, { document: preview.document, commands });
      if (generation.current !== token || currentDocument.current !== document) return;
      formStore.remove(DRAFT_KEY);
      setPreview({ ...response, source: response.document });
      setForm(nextForm);
      onChange(response.document);
    } catch (reason) {
      if (generation.current === token) {
        const message = errorMessage(reason);
        setError(message);
        if (draft) formStore.set(DRAFT_KEY, { ...draft, error: message });
      }
    } finally {
      if (commandOwner.current === owner) {
        commandOwner.current = null;
        setBusy(false);
      }
    }
  }
  async function arrange() {
    if (
      busy ||
      layoutActive ||
      commandOwner.current !== null ||
      blocked ||
      pending ||
      otherDraft ||
      !model?.resources.length ||
      !preview
    )
      return;
    const token = ++generation.current;
    const owner = ++commandSequence.current;
    const controller = new AbortController();
    commandOwner.current = owner;
    layoutController.current = controller;
    setBusy(true);
    setLayout({ source: document });
    setLayoutError("");
    try {
      const arranged = await layoutDiagram(resourceLayoutInput(model));
      if (
        controller.signal.aborted ||
        generation.current !== token ||
        currentDocument.current !== document
      )
        return;
      const positions = new Map(arranged.nodes.map((node) => [node.id, node]));
      const commands: Command[] = model.resources.map((resource) => {
        const point = positions.get(`resource:${resource.id}`)!;
        return { kind: "move_resource", resourceId: resource.id, x: point.x, y: point.y };
      });
      const response = await previewLayoutCommands(
        preview.document,
        commands,
        (document, commands, signal) =>
          previewResourceMap(designId, { document, commands }, signal),
        controller.signal,
      );
      if (
        controller.signal.aborted ||
        generation.current !== token ||
        currentDocument.current !== document
      )
        return;
      setLayout({ source: document, proposal: response });
      setLayoutFit((identity) => identity + 1);
    } catch (reason) {
      if (controller.signal.aborted || generation.current !== token) return;
      setLayout(undefined);
      setLayoutError(errorMessage(reason));
    } finally {
      if (commandOwner.current === owner) {
        commandOwner.current = null;
        layoutController.current = null;
        setBusy(false);
      }
    }
  }
  function cancelLayout() {
    if (proposal) setLayoutFit((identity) => identity + 1);
    generation.current++;
    layoutController.current?.abort();
    layoutController.current = null;
    commandOwner.current = null;
    setBusy(false);
    setLayout(undefined);
    setLayoutError("");
  }
  function applyLayout() {
    if (!proposal || busy || layout?.source !== currentDocument.current || pending || otherDraft)
      return;
    generation.current++;
    setLayout(undefined);
    setPreview({ ...proposal, source: proposal.document });
    if (form?.kind === "resource") {
      const resource = proposal.model.resources.find((resource) => resource.id === form.id);
      setForm(resource ? resourceForm(resource) : undefined);
    }
    onChange(proposal.document);
  }
  function apply() {
    if (!form || !model || layoutActive) return;
    try {
      if (form.kind === "resource") {
        const name = form.name.trim();
        if (!name) throw new Error("Укажите название ресурса.");
        fieldLimit(name, 200, "Название");
        fieldLimit(form.service, 200, "Сервис");
        fieldLimit(form.description, 2000, "Описание");
        const resource = selectedResource ?? {
          id: form.id,
          name,
          service: "",
          description: "",
          operationKeys: [],
          x: 40,
          y: 40,
          inferred: false,
        };
        const next = {
          ...editableResource(resource),
          name,
          service: form.service.trim(),
          description: form.description,
          x: coordinate(form.x),
          y: coordinate(form.y),
        };
        void submit([{ kind: "upsert_resource", resource: next }], {
          ...form,
          name,
          create: false,
        });
      } else {
        if (!form.toResourceId) throw new Error("Выберите связанный ресурс.");
        if (form.fromResourceId === form.toResourceId)
          throw new Error("Ресурс нельзя связать с самим собой.");
        fieldLimit(form.label, 200, "Подпись связи");
        void submit(
          [
            {
              kind: "upsert_relation",
              relation: {
                id: form.id,
                fromResourceId: form.fromResourceId,
                toResourceId: form.toResourceId,
                label: form.label.trim(),
              },
            },
          ],
          { ...form, create: false },
        );
      }
    } catch (reason) {
      const message = errorMessage(reason);
      setError(message);
      if (draft) formStore.set(DRAFT_KEY, { ...draft, error: message });
    }
  }
  function selectResource(id: string) {
    const resource = model?.resources.find((item) => item.id === id);
    if (resource) select(resourceForm(resource));
  }
  const selectedOps =
    selectedResource && model
      ? model.operations.filter((operation) =>
          selectedResource.operationKeys.includes(operation.key),
        )
      : [];
  const selectedUsages = selectedOps.flatMap((operation) =>
    (savedMap?.scenarioUsages ?? []).filter((usage) => usage.operationKey === operation.key),
  );
  return (
    <Stack gap="md" data-testid="resource-map-editor">
      {blocked ? (
        <Alert color="yellow">
          Исправьте JSON или небезопасные числа в исходнике перед редактированием карты.
        </Alert>
      ) : otherDraft ? (
        <Alert color="yellow">Завершите редактирование полей в форме API.</Alert>
      ) : (
        <>
          <Group justify="space-between" align="start">
            <div>
              <Title order={3}>Ресурсы API</Title>
              <Text size="sm" c="dimmed">
                Перетащите карточку для размещения. Для навигации и правок используйте список и
                инспектор.
              </Text>
            </div>
            <Group gap="xs">
              <Button
                size="xs"
                variant="default"
                disabled={pending || busy || layoutActive || !model?.resources.length}
                onClick={() => void arrange()}
              >
                Расставить ресурсы
              </Button>
              <Button
                size="xs"
                disabled={pending || busy || layoutActive || !model}
                onClick={() =>
                  select({
                    kind: "resource",
                    id: crypto.randomUUID(),
                    name: "",
                    service: "",
                    description: "",
                    x: "40",
                    y: "40",
                    create: true,
                  })
                }
              >
                Добавить ресурс
              </Button>
            </Group>
          </Group>
          {layoutActive && (
            <Alert title="Расположение ресурсов" color="blue">
              <Stack gap="sm">
                <Text size="sm">
                  {proposal
                    ? "Проверьте расположение на карте. Примените его, чтобы обновить черновик API."
                    : "Подбираем расположение ресурсов…"}
                </Text>
                <Group gap="xs">
                  {proposal && (
                    <Button size="xs" onClick={applyLayout}>
                      Применить расположение
                    </Button>
                  )}
                  <Button size="xs" variant="default" onClick={cancelLayout}>
                    Отменить
                  </Button>
                </Group>
              </Stack>
            </Alert>
          )}
          {layoutError && (
            <Alert color="red" role="alert">
              {layoutError}
            </Alert>
          )}
          {pending && (
            <Alert color="yellow">
              Есть неприменённый ввод. Примените его или нажмите «Сбросить ввод» перед сохранением
              API.
            </Alert>
          )}
          {error && (
            <Alert color="red" role="alert" className={styles.wrap}>
              {error}
              {!model && (
                <Button size="xs" variant="subtle" onClick={() => setRetry((n) => n + 1)}>
                  Повторить
                </Button>
              )}
            </Alert>
          )}
          {!model ? (
            !error && <Loader aria-label="Загрузка карты ресурсов" />
          ) : (
            <>
              {model.diagnostics.length > 0 && (
                <Alert
                  color={
                    model.diagnostics.some((diagnostic) => diagnostic.severity === "error")
                      ? "red"
                      : "yellow"
                  }
                  title="Диагностика карты"
                >
                  <Stack gap={4}>
                    {model.diagnostics.map((diagnostic, index) => (
                      <Text key={`${diagnostic.code}:${index}`} size="xs" className={styles.wrap}>
                        {diagnostic.message}
                      </Text>
                    ))}
                  </Stack>
                </Alert>
              )}
              <div className={styles.layout}>
                <div className={styles.mapColumn}>
                  <ResourceGraph
                    model={model}
                    fitIdentity={layoutFit}
                    selectedId={form?.kind === "resource" ? form.id : undefined}
                    disabled={pending || busy || layoutActive}
                    onSelect={selectResource}
                    onMove={(id, x, y) => {
                      if (!pending && !layoutActive)
                        void submit(
                          [{ kind: "move_resource", resourceId: id, x, y }],
                          model.resources.find((r) => r.id === id)
                            ? {
                                ...resourceForm(model.resources.find((r) => r.id === id)!),
                                x: String(x),
                                y: String(y),
                              }
                            : undefined,
                        );
                    }}
                  />
                  <section aria-label="Список ресурсов" className={styles.resourceList}>
                    {model.resources.length === 0 ? (
                      <Text size="sm" c="dimmed">
                        Операций пока нет. Создайте ресурс для планирования или добавьте операцию.
                      </Text>
                    ) : (
                      model.resources.map((resource) => (
                        <Button
                          key={resource.id}
                          size="compact-sm"
                          variant={
                            form?.kind === "resource" && form.id === resource.id
                              ? "light"
                              : "subtle"
                          }
                          className={styles.resourceButton}
                          disabled={layoutActive}
                          onClick={() => selectResource(resource.id)}
                        >
                          Ресурс {resource.name}{" "}
                          <Badge size="xs" ml="xs" variant="light">
                            {resource.operationKeys.length}
                          </Badge>
                        </Button>
                      ))
                    )}
                  </section>
                  {model.operations.length === 0 && onAddOperation && (
                    <Button
                      size="xs"
                      variant="light"
                      mt="xs"
                      disabled={pending || busy || layoutActive}
                      onClick={onAddOperation}
                    >
                      Добавить операцию
                    </Button>
                  )}
                  {model.relations.length > 0 && (
                    <section aria-label="Связи ресурсов">
                      <Text size="sm" fw={600}>
                        Связи
                      </Text>
                      <div className={styles.resourceList}>
                        {model.relations.map((relation) => (
                          <Button
                            key={relation.id}
                            size="compact-sm"
                            variant="subtle"
                            className={styles.resourceButton}
                            disabled={layoutActive}
                            onClick={() => select(relationForm(relation))}
                          >
                            {model.resources.find((r) => r.id === relation.fromResourceId)?.name ??
                              relation.fromResourceId}{" "}
                            →{" "}
                            {model.resources.find((r) => r.id === relation.toResourceId)?.name ??
                              relation.toResourceId}
                            {relation.label ? ` · ${relation.label}` : ""}
                          </Button>
                        ))}
                      </div>
                    </section>
                  )}
                </div>
                <section className={styles.inspector} aria-label="Инспектор ресурса">
                  {!form ? (
                    <Text size="sm" c="dimmed">
                      Выберите ресурс в списке или на карте.
                    </Text>
                  ) : form.kind === "resource" ? (
                    <Stack gap="sm">
                      <Title order={4}>
                        {form.create ? "Новый ресурс" : (selectedResource?.name ?? "Ресурс")}
                      </Title>
                      <TextInput
                        label="Название ресурса"
                        disabled={layoutActive}
                        value={form.name}
                        maxLength={200}
                        onChange={(event) => edit({ ...form, name: event.currentTarget.value })}
                      />
                      <TextInput
                        label="Сервис"
                        disabled={layoutActive}
                        value={form.service}
                        maxLength={200}
                        onChange={(event) => edit({ ...form, service: event.currentTarget.value })}
                      />
                      <Textarea
                        label="Описание"
                        disabled={layoutActive}
                        value={form.description}
                        maxLength={2000}
                        autosize
                        minRows={2}
                        maxRows={5}
                        onChange={(event) =>
                          edit({ ...form, description: event.currentTarget.value })
                        }
                      />
                      <Group grow>
                        <TextInput
                          label="X"
                          disabled={layoutActive}
                          inputMode="decimal"
                          value={form.x}
                          onChange={(event) => edit({ ...form, x: event.currentTarget.value })}
                        />
                        <TextInput
                          label="Y"
                          disabled={layoutActive}
                          inputMode="decimal"
                          value={form.y}
                          onChange={(event) => edit({ ...form, y: event.currentTarget.value })}
                        />
                      </Group>
                      <Group>
                        <Button
                          size="xs"
                          loading={busy}
                          disabled={!pending || layoutActive}
                          onClick={apply}
                        >
                          Применить
                        </Button>
                        {pending && (
                          <Button
                            size="xs"
                            variant="default"
                            onClick={() =>
                              discard(selectedResource ? resourceForm(selectedResource) : undefined)
                            }
                          >
                            Сбросить ввод
                          </Button>
                        )}
                      </Group>
                      {selectedResource && !form.create && (
                        <>
                          <Text size="sm" fw={600}>
                            Операции
                          </Text>
                          {selectedOps.length === 0 && (
                            <Text size="xs" c="dimmed">
                              Операций нет.
                            </Text>
                          )}
                          {selectedOps.map((operation) => (
                            <div key={operation.key} className={styles.operationRow}>
                              <Group gap="xs" wrap="nowrap">
                                <Badge size="xs" variant="light">
                                  {operation.method.toUpperCase()}
                                </Badge>
                                <Button
                                  size="compact-sm"
                                  variant="subtle"
                                  className={styles.wrapButton}
                                  disabled={pending || busy || layoutActive}
                                  onClick={() =>
                                    onOperation(
                                      operation.path,
                                      operation.method,
                                      operation.sourcePointer,
                                    )
                                  }
                                >
                                  {operation.path}
                                  {operation.summary ? ` · ${operation.summary}` : ""}
                                </Button>
                              </Group>
                              <NativeSelect
                                label={`Ресурс для ${operation.method.toUpperCase()} ${operation.path}`}
                                value={selectedResource.id}
                                disabled={pending || busy || layoutActive}
                                onChange={(event) =>
                                  void submit(
                                    [
                                      {
                                        kind: "assign_operation",
                                        operationKey: operation.key,
                                        resourceId: event.currentTarget.value,
                                      },
                                    ],
                                    model.resources.find(
                                      (resource) => resource.id === event.currentTarget.value,
                                    )
                                      ? resourceForm(
                                          model.resources.find(
                                            (resource) => resource.id === event.currentTarget.value,
                                          )!,
                                        )
                                      : undefined,
                                  )
                                }
                                data={[
                                  { value: "", label: "Автогруппировка" },
                                  ...model.resources.map((r) => ({ value: r.id, label: r.name })),
                                ]}
                              />
                              {operation.schemas.length > 0 && (
                                <Group gap="xs">
                                  {operation.schemas.map((name) => (
                                    <Button
                                      key={name}
                                      size="compact-xs"
                                      variant="subtle"
                                      disabled={pending || busy || layoutActive}
                                      onClick={() => onSchema(name)}
                                    >
                                      Схема {name}
                                    </Button>
                                  ))}
                                </Group>
                              )}
                            </div>
                          ))}
                          <Button
                            size="xs"
                            variant="light"
                            disabled={pending || busy || layoutActive || model.resources.length < 2}
                            onClick={() =>
                              select({
                                kind: "relation",
                                id: crypto.randomUUID(),
                                fromResourceId: selectedResource.id,
                                toResourceId: "",
                                label: "",
                                create: true,
                              })
                            }
                          >
                            Добавить связь
                          </Button>
                          {!selectedResource.inferred && (
                            <Button
                              size="xs"
                              color="red"
                              variant="subtle"
                              disabled={pending || busy || layoutActive}
                              onClick={() =>
                                void submit([
                                  { kind: "remove_resource", resourceId: selectedResource.id },
                                ])
                              }
                            >
                              Вернуть автогруппировку
                            </Button>
                          )}
                          {selectedUsages.length > 0 && (
                            <section aria-label="Сценарии сохранённого черновика">
                              <Text size="sm" fw={600}>
                                Сценарии сохранённого черновика
                              </Text>
                              {selectedUsages.map((usage) => (
                                <Button
                                  key={`${usage.scenarioId}:${usage.messageId}`}
                                  size="compact-sm"
                                  variant="subtle"
                                  className={styles.wrapButton}
                                  disabled={pending || busy || layoutActive}
                                  onClick={() => onScenario?.(usage.scenarioId)}
                                >
                                  {usage.scenarioName} ·{" "}
                                  {usage.mode === "copy" ? "копия" : "связанный контракт"} · ревизия
                                  API {usage.contractRevisionId ?? "не указана"} · ревизия сценария{" "}
                                  {usage.revisionId}
                                </Button>
                              ))}
                            </section>
                          )}
                        </>
                      )}
                    </Stack>
                  ) : (
                    <Stack gap="sm">
                      <Title order={4}>{form.create ? "Новая связь" : "Связь ресурсов"}</Title>
                      <Text size="sm">
                        {model.resources.find((r) => r.id === form.fromResourceId)?.name ??
                          form.fromResourceId}{" "}
                        →
                      </Text>
                      <NativeSelect
                        label="Связанный ресурс"
                        disabled={layoutActive}
                        value={form.toResourceId}
                        onChange={(event) =>
                          edit({ ...form, toResourceId: event.currentTarget.value })
                        }
                        data={[
                          { value: "", label: "Выберите ресурс" },
                          ...model.resources
                            .filter((r) => r.id !== form.fromResourceId)
                            .map((r) => ({ value: r.id, label: r.name })),
                        ]}
                      />
                      <TextInput
                        label="Подпись связи"
                        disabled={layoutActive}
                        value={form.label}
                        maxLength={200}
                        onChange={(event) => edit({ ...form, label: event.currentTarget.value })}
                      />
                      <Group>
                        <Button
                          size="xs"
                          loading={busy}
                          disabled={!pending || layoutActive}
                          onClick={apply}
                        >
                          Применить
                        </Button>
                        {pending && (
                          <Button
                            size="xs"
                            variant="default"
                            onClick={() =>
                              discard(selectedRelation ? relationForm(selectedRelation) : undefined)
                            }
                          >
                            Сбросить ввод
                          </Button>
                        )}
                      </Group>
                      {selectedRelation && (
                        <Button
                          size="xs"
                          color="red"
                          variant="subtle"
                          disabled={pending || busy || layoutActive}
                          onClick={() =>
                            void submit([
                              { kind: "remove_relation", relationId: selectedRelation.id },
                            ])
                          }
                        >
                          Удалить связь
                        </Button>
                      )}
                    </Stack>
                  )}
                </section>
              </div>
              {savedError && (
                <Alert color="yellow">
                  Не удалось загрузить сценарии сохранённого черновика: {savedError}
                </Alert>
              )}
              {savedMap?.usagesTruncated && (
                <Alert color="yellow">
                  Список сценариев сокращён. Откройте сценарии для полного просмотра.
                </Alert>
              )}
            </>
          )}
        </>
      )}
    </Stack>
  );
}
