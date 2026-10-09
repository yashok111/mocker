import { lazy, Suspense, useMemo, useState } from "react";
import {
  Alert,
  Badge,
  Button,
  Group,
  Loader,
  NativeSelect,
  Paper,
  Stack,
  Text,
  Textarea,
  TextInput,
  Title,
} from "@mantine/core";
import type {
  BackendInteractionDocument,
  BackendInteractionPayload,
  BackendDiagramGap,
  BackendDiagramRef,
  BackendDiagramViewState,
} from "@/api/generated/schemas";
import { interactionCanvas } from "./backendInteractionCanvas";
const BackendInteractionGraph = lazy(() =>
  import("./BackendInteractionGraph").then((m) => ({ default: m.BackendInteractionGraph })),
);
type Selection = BackendDiagramViewState["selection"];
export function BackendInteractions({
  payload,
  gaps,
  selection,
  onSelect,
  onOpen,
  disabled,
  search = "",
  origin = "all",
  presentation,
}: {
  payload: BackendInteractionPayload;
  gaps: BackendDiagramGap[];
  selection: Selection;
  onSelect: (s: NonNullable<Selection>) => void;
  onOpen: (r: BackendDiagramRef) => void;
  disabled: boolean;
  search?: string;
  origin?: string;
  presentation?: Pick<BackendDiagramViewState, "positions" | "collapsedIds">;
}) {
  const canvas = useMemo(
    () => interactionCanvas(payload, gaps, presentation),
    [payload, gaps, presentation],
  );
  const rows = [...payload.participants, ...payload.steps, ...payload.branches, ...payload.order];
  const selected = rows.find((r) => r.id === selection?.id);
  return (
    <Stack data-testid="backend-interactions">
      <Title order={3} tabIndex={-1}>
        Динамические взаимодействия
      </Title>
      <Group>
        <Badge>Статическая модель</Badge>
        <Badge variant="outline">Исполнение не проверено</Badge>
      </Group>
      <Text>
        Порядок отображения не означает порядок исполнения. Ветви и неизвестная доставка сохраняются
        явно.
      </Text>
      {canvas.partialOrder && (
        <Alert color="yellow">
          Ветви, частичный порядок, скрытые элементы или лимит canvas: сообщения показаны в
          доступном списке, без придуманного линейного пути.
        </Alert>
      )}
      {!canvas.partialOrder && (
        <Suspense fallback={<Loader aria-label="Загрузка статической диаграммы" />}>
          <BackendInteractionGraph document={canvas.document} />
        </Suspense>
      )}
      <Stack aria-label="Элементы interactions">
        {rows
          .filter(
            (r) =>
              (origin === "all" || r.origin.kind === origin) &&
              `${"label" in r ? r.label : r.id} ${r.id}`
                .toLowerCase()
                .includes(search.toLowerCase()),
          )
          .map((row) => (
            <Group key={row.id} wrap="wrap">
              <Button
                variant="default"
                h="auto"
                mih={36}
                style={{ whiteSpace: "normal", overflowWrap: "anywhere", maxWidth: "100%" }}
                styles={{ label: { whiteSpace: "normal", overflowWrap: "anywhere" } }}
                disabled={disabled}
                onClick={() =>
                  onSelect({
                    type: payload.order.some((o) => o.id === row.id) ? "link" : "element",
                    id: row.id,
                  })
                }
              >
                {"label" in row
                  ? row.label
                  : `Порядок: ${payload.steps.find((s) => s.id === row.from)?.label ?? row.from} → ${payload.steps.find((s) => s.id === row.to)?.label ?? row.to}`}
              </Button>
              <Badge variant="light">
                {row.origin.kind === "authored" ? "Авторский замысел" : "Исходное утверждение"}
              </Badge>
              {"branchPath" in row && row.branchPath.length > 0 && (
                <Text size="sm">
                  Ветвь:{" "}
                  {row.branchPath
                    .map((id) => payload.branches.find((b) => b.id === id)?.label ?? id)
                    .join(" / ")}
                </Text>
              )}
              {"branchPath" in row && row.kind !== "action" && !row.to && (
                <Text size="sm">Получатель не установлен</Text>
              )}
            </Group>
          ))}
      </Stack>
      {selected && (
        <Paper withBorder p="md">
          <Stack aria-label="Interactions инспектор">
            <Title order={4} tabIndex={-1} id="interactions-inspector-title">
              Основания взаимодействия
            </Title>
            <Text style={{ overflowWrap: "anywhere" }}>{selected.id}</Text>
            <Text>
              {selected.origin.kind === "authored"
                ? selected.origin.reason
                : "Статическое утверждение из исходников"}
            </Text>
            {selected.origin.kind === "source_assertion" &&
              selected.origin.evidence.map((e) => (
                <Text
                  key={`${e.revisionId}:${e.evidenceId}`}
                  size="sm"
                  style={{ overflowWrap: "anywhere" }}
                >
                  Evidence {e.evidenceId} · revision {e.revisionId} · subject {e.subjectId}
                </Text>
              ))}
            {"refs" in selected &&
              selected.refs.map((ref, i) => (
                <Button key={i} variant="default" disabled={disabled} onClick={() => onOpen(ref)}>
                  Открыть точное основание {i + 1}
                </Button>
              ))}
            {gaps
              .filter((g) => g.subjectId === selected.id)
              .map((g) => (
                <Text key={g.id}>
                  {g.code}: {g.explanation}
                </Text>
              ))}
          </Stack>
        </Paper>
      )}
    </Stack>
  );
}
export function BackendInteractionsEditor({
  document,
  onChange,
  onSave,
  onCancel,
  busy,
}: {
  document: BackendInteractionDocument;
  onChange: (d: BackendInteractionDocument) => void;
  onSave: () => void;
  onCancel: () => void;
  busy: boolean;
}) {
  const [raw, setRaw] = useState<string>();
  const [error, setError] = useState("");
  const [label, setLabel] = useState("");
  const [reason, setReason] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const update = (payload: BackendInteractionPayload) => {
    setRaw(undefined);
    onChange({ ...document, payload });
  };
  return (
    <Paper withBorder p="md">
      <Stack aria-label="Редактор interactions">
        <Title order={3}>Авторская модель взаимодействий</Title>
        <Group grow>
          <TextInput
            label="Название участника или шага"
            value={label}
            onChange={(e) => setLabel(e.currentTarget.value)}
            disabled={busy}
          />
          <TextInput
            label="Основание interactions"
            value={reason}
            onChange={(e) => setReason(e.currentTarget.value)}
            disabled={busy}
          />
        </Group>
        <Button
          disabled={busy || !!error || !label.trim() || !reason.trim()}
          onClick={() =>
            update({
              ...document.payload,
              participants: [
                ...document.payload.participants,
                { id: crypto.randomUUID(), label, origin: { kind: "authored", reason }, refs: [] },
              ],
            })
          }
        >
          Добавить участника
        </Button>
        <Group grow>
          <NativeSelect
            label="Отправитель"
            value={from}
            onChange={(e) => setFrom(e.currentTarget.value)}
            data={[
              { value: "", label: "Выберите участника" },
              ...document.payload.participants.map((p) => ({ value: p.id, label: p.label })),
            ]}
            disabled={busy}
          />
          <NativeSelect
            label="Получатель"
            value={to}
            onChange={(e) => setTo(e.currentTarget.value)}
            data={[
              { value: "", label: "Получатель неизвестен" },
              ...document.payload.participants.map((p) => ({ value: p.id, label: p.label })),
            ]}
            disabled={busy}
          />
        </Group>
        <Button
          disabled={busy || !!error || !from || !label.trim() || !reason.trim()}
          onClick={() =>
            update({
              ...document.payload,
              steps: [
                ...document.payload.steps,
                {
                  id: crypto.randomUUID(),
                  label,
                  origin: { kind: "authored", reason },
                  refs: [],
                  kind: to ? "request" : "send",
                  from,
                  ...(to ? { to } : {}),
                  branchPath: [],
                },
              ],
            })
          }
        >
          Добавить шаг
        </Button>
        <Textarea
          label="Interactions JSON"
          description="Ветви, частичный порядок, replies и точные source refs. Сервер проверяет всю модель перед сохранением."
          value={raw ?? JSON.stringify(document.payload, null, 2)}
          minRows={8}
          maxRows={18}
          autosize
          disabled={busy}
          error={error}
          onChange={(e) => {
            const value = e.currentTarget.value;
            setRaw(value);
            try {
              const payload = JSON.parse(value);
              if (!interactionEditorPayload(payload)) throw Error();
              onChange({ ...document, payload });
              setError("");
            } catch {
              setError("Исправьте JSON перед сохранением");
            }
          }}
        />
        <Group>
          <Button onClick={onSave} disabled={busy || !!error}>
            Сохранить interactions
          </Button>
          <Button variant="default" onClick={onCancel} disabled={busy}>
            Отменить редактирование
          </Button>
        </Group>
      </Stack>
    </Paper>
  );
}

function interactionEditorPayload(value: unknown): value is BackendInteractionPayload {
  const object = (v: unknown): v is Record<string, unknown> =>
    !!v && typeof v === "object" && !Array.isArray(v);
  const origin = (v: unknown) =>
    object(v) &&
    (v.kind === "authored"
      ? typeof v.reason === "string"
      : v.kind === "source_assertion" &&
        Array.isArray(v.evidence) &&
        v.evidence.every(
          (e) =>
            object(e) &&
            typeof e.revisionId === "string" &&
            typeof e.evidenceId === "string" &&
            typeof e.subjectId === "string",
        ));
  const ref = (v: unknown) =>
    object(v) &&
    (v.kind === "record"
      ? typeof v.id === "string" && ["node", "edge"].includes(String(v.recordType))
      : v.kind === "artifact" && object(v.locator) && typeof v.rowId === "string");
  const base = (v: unknown): v is Record<string, unknown> =>
    object(v) &&
    typeof v.id === "string" &&
    typeof v.label === "string" &&
    origin(v.origin) &&
    Array.isArray(v.refs) &&
    v.refs.every(ref);
  if (
    !object(value) ||
    !Array.isArray(value.participants) ||
    !Array.isArray(value.steps) ||
    !Array.isArray(value.branches) ||
    !Array.isArray(value.order) ||
    !Array.isArray(value.scopeRefs)
  )
    return false;
  return (
    value.participants.every(base) &&
    value.scopeRefs.every(ref) &&
    value.steps.every(
      (v) =>
        base(v) &&
        typeof v.from === "string" &&
        (v.to === undefined || typeof v.to === "string") &&
        typeof v.kind === "string" &&
        Array.isArray(v.branchPath) &&
        v.branchPath.every((id) => typeof id === "string"),
    ) &&
    value.branches.every(
      (v) =>
        object(v) &&
        typeof v.id === "string" &&
        typeof v.label === "string" &&
        typeof v.groupId === "string" &&
        typeof v.kind === "string" &&
        typeof v.guardText === "string" &&
        origin(v.origin),
    ) &&
    value.order.every(
      (v) =>
        object(v) &&
        typeof v.id === "string" &&
        typeof v.from === "string" &&
        typeof v.to === "string" &&
        origin(v.origin),
    )
  );
}
