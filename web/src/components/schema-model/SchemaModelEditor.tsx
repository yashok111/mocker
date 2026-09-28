import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { Alert, Badge, Button, Group, Loader, Stack, Text } from "@mantine/core";
import { previewSchemaModel } from "@/api/generated/schema-model/schema-model";
import type { ApiDesignDiagnostic, SchemaModel, SchemaModelCommand } from "@/api/generated/schemas";
import { describeApiFailureDetailed } from "@/api/errors";
import { ApiFailure } from "@/api/client";
import type { FormDraftStore } from "../api-designer/forms/formDraftStore";
import {
  DRAFT_KEY,
  formCommands,
  makeForm,
  objectCompatible,
  record,
  type Form,
  type Selection,
} from "./form";
import SchemaGraph from "./SchemaGraph";
import SchemaInspector from "./SchemaInspector";
import styles from "./SchemaModel.module.css";

type Props = {
  designId: number;
  document: string;
  blocked: boolean;
  formStore: FormDraftStore;
  onChange: (document: string) => void;
};
function describeSchemaModelFailure(reason: unknown): string {
  if (
    reason instanceof ApiFailure &&
    record(reason.details) &&
    Array.isArray(reason.details.diagnostics)
  ) {
    const diagnostics = reason.details.diagnostics
      .flatMap((diagnostic: unknown) => {
        if (!record(diagnostic) || typeof diagnostic.message !== "string") return [];
        const pointer = typeof diagnostic.pointer === "string" ? diagnostic.pointer : "";
        return [pointer ? `${pointer}: ${diagnostic.message}` : diagnostic.message];
      })
      .filter(Boolean);
    if (diagnostics.length) return diagnostics.join("\n");
  }
  return describeApiFailureDetailed(reason);
}

export default function SchemaModelEditor(props: Props) {
  const { designId, document, blocked, formStore, onChange } = props;
  const snapshot = useSyncExternalStore(
    formStore.subscribe,
    formStore.getSnapshot,
    formStore.getSnapshot,
  );
  const draft = formStore.get(DRAFT_KEY);
  const [form, setForm] = useState<Form | undefined>(() => {
    try {
      return draft ? (JSON.parse(draft.source) as Form) : undefined;
    } catch {
      return undefined;
    }
  });
  const [result, setResult] = useState<{
    document: string;
    model: SchemaModel;
    diagnostics: ApiDesignDiagnostic[];
  }>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [retry, setRetry] = useState(0);
  const generation = useRef(0);
  const current = useRef(props);
  useEffect(() => {
    current.current = props;
  });
  const model = result?.document === document ? result.model : undefined;
  const pending = draft !== undefined;
  const otherDraft = snapshot.dirty && !pending;

  useEffect(() => {
    const controller = new AbortController();
    const token = ++generation.current;
    if (!document || blocked) return;
    void previewSchemaModel(designId, { document }, { signal: controller.signal }).then(
      (response) => {
        if (generation.current !== token || response.status !== 200) return;
        setResult({ document, model: response.data.model, diagnostics: response.data.diagnostics });
        setError("");
      },
      (reason: unknown) => {
        if (!controller.signal.aborted && generation.current === token)
          setError(describeSchemaModelFailure(reason));
      },
    );
    return () => {
      controller.abort();
      // oxlint-disable-next-line react-hooks/exhaustive-deps -- Increment the shared request generation to fence commands after unmount.
      generation.current++;
    };
  }, [designId, document, blocked, retry]);

  function edit(next: Form) {
    generation.current++;
    setForm(next);
    setError("");
    formStore.set(DRAFT_KEY, {
      source: JSON.stringify(next),
      propertySource: draft?.propertySource ?? document,
    });
  }
  function select(selection: Selection) {
    if (pending) {
      setError("Примените изменения или сбросьте ввод перед выбором другой схемы или поля.");
      return;
    }
    setError("");
    const next = makeForm(
      selection,
      model?.schemas.find((s) => s.name === selection.schema),
    );
    if (selection.create && selection.property === undefined && model?.schemas.length)
      next.y = String(
        Math.max(
          ...model.schemas.map((s) => s.y + 46 + Math.max(1, s.properties.length) * 30 + 68),
        ),
      );
    setForm(next);
    if (selection.create) edit(next);
  }
  async function submit(commands: SchemaModelCommand[], nextSelection?: Selection) {
    if (busy || blocked || otherDraft) return;
    if (draft && draft.propertySource !== document) {
      setError(
        "Документ изменился после начала ввода. Скопируйте изменения и сбросьте ввод перед повторным применением.",
      );
      return;
    }
    const token = ++generation.current;
    setBusy(true);
    setError("");
    try {
      const response = await previewSchemaModel(designId, { document, commands });
      if (token !== generation.current || current.current.document !== document) return;
      if (response.status !== 200) throw new Error("Не удалось применить изменения.");
      formStore.remove(DRAFT_KEY);
      setResult({
        document: response.data.document,
        model: response.data.model,
        diagnostics: response.data.diagnostics,
      });
      setForm(
        nextSelection
          ? makeForm(
              nextSelection,
              response.data.model.schemas.find((s) => s.name === nextSelection.schema),
            )
          : undefined,
      );
      onChange(response.data.document);
    } catch (reason) {
      if (token === generation.current) setError(describeSchemaModelFailure(reason));
    } finally {
      setBusy(false);
    }
  }
  function apply() {
    if (!form) return;
    try {
      if (
        form.selection.create &&
        (form.selection.property === undefined
          ? model?.schemas.some((s) => s.name === form.name.trim())
          : model?.schemas
              .find((s) => s.name === form.selection.schema)
              ?.properties.some((p) => p.name === form.name.trim()))
      )
        throw new Error("Имя уже используется.");
      const commands = formCommands(form);
      void submit(commands, {
        schema: form.selection.property === undefined ? form.name.trim() : form.selection.schema,
        ...(form.selection.property === undefined ? {} : { property: form.name.trim() }),
      });
    } catch (reason) {
      const message = reason instanceof Error ? reason.message : "Проверьте значения формы.";
      setError(message);
      if (draft) formStore.set(DRAFT_KEY, { ...draft, error: message });
    }
  }
  const selectedSchema = model?.schemas.find((s) => s.name === form?.selection.schema);
  const canInspect =
    form &&
    (pending ||
      form.selection.create ||
      (selectedSchema &&
        (form.selection.property === undefined ||
          selectedSchema.properties.some((p) => p.name === form.selection.property))));
  const affected =
    model?.operations.filter((operation) =>
      operation.schemas.includes(selectedSchema?.name ?? ""),
    ) ?? [];
  const incoming =
    model?.references.filter((ref) => ref.targetSchema === selectedSchema?.name) ?? [];
  return (
    <Stack gap="md" data-testid="schema-model-editor">
      {blocked ? (
        <Alert color="yellow">
          Исправьте JSON или небезопасные числа в исходнике перед визуальным редактированием.
        </Alert>
      ) : otherDraft ? (
        <Alert color="yellow">Завершите редактирование полей в форме API.</Alert>
      ) : (
        <>
          <Group justify="space-between">
            <Text size="sm" c="dimmed">
              * — обязательное поле. Связь: перетащите точку поля к схеме или выберите её в
              инспекторе.
            </Text>
            <Button
              size="xs"
              disabled={pending || busy || !model}
              onClick={() => select({ schema: "", create: true })}
            >
              Добавить схему
            </Button>
          </Group>
          {pending && (
            <Alert color="yellow">
              Есть неприменённый ввод. Примените его или нажмите «Сбросить ввод» перед сохранением
              API.
            </Alert>
          )}
          {error && (
            <Alert
              color="red"
              role="alert"
              styles={{ message: { whiteSpace: "pre-wrap", overflowWrap: "anywhere" } }}
            >
              {error}
              {!model && (
                <Button variant="subtle" onClick={() => setRetry((n) => n + 1)}>
                  Повторить
                </Button>
              )}
            </Alert>
          )}
          {!model ? (
            !error && <Loader aria-label="Загрузка модели схем" />
          ) : (
            <>
              {result?.diagnostics.length ? (
                <Alert color="yellow" title="Проверка документа">
                  <Stack gap={4}>
                    {result.diagnostics.map((d, i) => (
                      <Text key={i} size="xs" className={styles.wrap}>
                        {d.pointer}: {d.message}
                      </Text>
                    ))}
                  </Stack>
                </Alert>
              ) : null}
              <div className={styles.layout}>
                <div>
                  <SchemaGraph
                    model={model}
                    selection={form?.selection}
                    disabled={pending || busy}
                    onSelect={select}
                    onMove={(schema, x, y) => {
                      if (!pending) {
                        const moved = makeForm(
                          { schema },
                          model.schemas.find((s) => s.name === schema),
                        );
                        moved.x = String(x);
                        moved.y = String(y);
                        edit(moved);
                        void submit([{ kind: "move_schema", schemaName: schema, x, y }], {
                          schema,
                        });
                      }
                    }}
                    onConnect={(schema, property, target) => {
                      if (pending) return;
                      try {
                        const linked = makeForm(
                          { schema, property },
                          model.schemas.find((s) => s.name === schema),
                        );
                        linked.target = target;
                        linked.referenceChanged = true;
                        edit(linked);
                        void submit(
                          [
                            {
                              kind: "set_reference",
                              schemaName: schema,
                              propertyName: property,
                              targetSchema: target,
                              array: linked.array,
                            },
                          ],
                          { schema, property },
                        );
                      } catch (reason) {
                        setError(
                          reason instanceof Error ? reason.message : "Не удалось связать схемы.",
                        );
                      }
                    }}
                  />
                  <Stack gap="sm" mt="md" component="section" aria-label="Схемы и поля">
                    {model.schemas.length === 0 && (
                      <Text c="dimmed">Схем пока нет. Добавьте первую схему.</Text>
                    )}
                    {model.schemas.map((schema) => (
                      <div key={schema.name}>
                        <Group gap="xs" mb={4}>
                          <Button
                            variant={
                              form?.selection.schema === schema.name &&
                              form?.selection.property === undefined
                                ? "light"
                                : "subtle"
                            }
                            size="compact-sm"
                            onClick={() => select({ schema: schema.name })}
                            className={styles.wrap}
                          >
                            {schema.name}
                          </Button>
                          <Badge variant="light" color="gray">
                            {schema.type || "schema"}
                          </Badge>
                          <Button
                            size="compact-xs"
                            variant="subtle"
                            disabled={pending || busy || !objectCompatible(schema.schemaJSON)}
                            onClick={() =>
                              select({ schema: schema.name, property: "", create: true })
                            }
                            aria-label={`Добавить поле в ${schema.name}`}
                          >
                            + поле
                          </Button>
                        </Group>
                        <div className={styles.list}>
                          {schema.properties.map((property) => (
                            <Button
                              size="compact-xs"
                              variant="default"
                              key={property.name}
                              onClick={() =>
                                select({ schema: schema.name, property: property.name })
                              }
                              aria-label={`Поле ${schema.name}.${property.name}`}
                            >
                              {property.name}
                              {property.required ? " *" : "?"} · {property.type}
                            </Button>
                          ))}
                        </div>
                      </div>
                    ))}
                  </Stack>
                </div>
                <Stack gap="md">
                  {canInspect && form ? (
                    <SchemaInspector
                      form={pending ? form : makeForm(form.selection, selectedSchema)}
                      model={model}
                      busy={busy}
                      pending={pending}
                      onChange={edit}
                      onApply={apply}
                      onDiscard={() => {
                        generation.current++;
                        formStore.remove(DRAFT_KEY);
                        setError("");
                        setForm(undefined);
                      }}
                      onDelete={() => {
                        if (!form.selection.create)
                          void submit([
                            {
                              kind:
                                form.selection.property === undefined
                                  ? "delete_schema"
                                  : "delete_property",
                              schemaName: form.selection.schema,
                              ...(form.selection.property === undefined
                                ? {}
                                : { propertyName: form.selection.property }),
                            },
                          ]);
                      }}
                    />
                  ) : (
                    <Text c="dimmed" size="sm">
                      Выберите схему или поле для редактирования.
                    </Text>
                  )}
                  {selectedSchema && (
                    <Stack gap="xs" className={styles.inspector}>
                      <Text fw={650}>Использование {selectedSchema.name}</Text>
                      <Text size="sm" fw={600}>
                        Операции API, включая косвенные ссылки
                      </Text>
                      {affected.length ? (
                        affected.map((op) => (
                          <Text key={op.pointer} size="xs" className={styles.wrap}>
                            {op.method.toUpperCase()} {op.path}
                          </Text>
                        ))
                      ) : (
                        <Text size="xs" c="dimmed">
                          Нет связанных операций
                        </Text>
                      )}
                      <Text size="sm" fw={600}>
                        Ссылки на схему
                      </Text>
                      {incoming.length ? (
                        incoming.map((ref, i) => (
                          <Text key={i} size="xs" className={styles.wrap}>
                            {ref.sourceSchema || "API"}
                            {ref.sourceProperty ? `.${ref.sourceProperty}` : ""} · {ref.pointer}
                          </Text>
                        ))
                      ) : (
                        <Text size="xs" c="dimmed">
                          Нет входящих ссылок
                        </Text>
                      )}
                      {model.references
                        .filter(
                          (ref) =>
                            ref.sourceSchema === selectedSchema.name &&
                            !ref.ref.startsWith("#/components/schemas/"),
                        )
                        .map((ref, i) => (
                          <Text key={i} size="xs" className={styles.wrap}>
                            Внешняя ссылка: {ref.ref}
                          </Text>
                        ))}
                    </Stack>
                  )}
                </Stack>
              </div>
            </>
          )}
        </>
      )}
    </Stack>
  );
}
