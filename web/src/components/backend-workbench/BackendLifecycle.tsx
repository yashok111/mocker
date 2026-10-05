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
  BackendLifecyclePayload,
  BackendLifecycleDocument,
  BackendDiagramRef,
  BackendDiagramGap,
  BackendDiagramViewState,
} from "@/api/generated/schemas";
import { lifecycleGraph } from "./backendLifecycleGraph";
const StateGraph = lazy(() => import("../state-diagram/StateGraph"));
type Selection = BackendDiagramViewState["selection"];
export function BackendLifecycle({
  payload,
  gaps,
  selection,
  onSelect,
  onOpen,
  disabled,
  search = "",
  origin = "all",
  presentation = { positions: [], collapsedIds: [] },
}: {
  payload: BackendLifecyclePayload;
  gaps: BackendDiagramGap[];
  selection: Selection;
  onSelect: (s: NonNullable<Selection>) => void;
  onOpen: (r: BackendDiagramRef) => void;
  disabled: boolean;
  search?: string;
  origin?: string;
  presentation?: Pick<BackendDiagramViewState, "positions" | "collapsedIds">;
}) {
  const graph = useMemo(() => lifecycleGraph(payload, presentation), [payload, presentation]);
  const rows = [...payload.states, ...payload.transitions, ...payload.rules];
  const selected = rows.find((r) => r.id === selection?.id);
  const name = (id: string) => payload.states.find((s) => s.id === id)?.label ?? id;
  return (
    <Stack data-testid="backend-lifecycle">
      <Title order={3} tabIndex={-1}>
        Жизненный цикл сущности
      </Title>
      <Group>
        <Badge>Статическая модель</Badge>
        <Badge variant="outline">Исполнение не проверено</Badge>
      </Group>
      <Alert color="yellow">
        {payload.coverage === "partial"
          ? "Неполное покрытие: отсутствие перехода не доказывает запрет или недостижимость."
          : "Полнота объявлена автором или источником; исполнение не подтверждено."}
      </Alert>
      <Group>
        <Button variant="default" disabled={disabled} onClick={() => onOpen(payload.entity)}>
          Открыть сущность
        </Button>
        {payload.stateFields.map((ref, i) => (
          <Button key={i} variant="default" disabled={disabled} onClick={() => onOpen(ref)}>
            Поле состояния {i + 1}
          </Button>
        ))}
      </Group>
      {payload.compoundMappingReason && (
        <Text>Составное соответствие: {payload.compoundMappingReason}</Text>
      )}
      {graph.limited && (
        <Text>
          Canvas ограничен или часть состояний скрыта. Полный поиск и инспектор доступны в списке.
        </Text>
      )}
      <Suspense fallback={<Loader aria-label="Загрузка графа состояний" />}>
        <StateGraph
          readOnly
          diagram={graph.diagram}
          selection={
            selection
              ? {
                  kind: payload.transitions.some((t) => t.id === selection.id)
                    ? "transition"
                    : "state",
                  id: selection.id,
                }
              : null
          }
          onSelect={(s) => {
            if (s && !disabled)
              onSelect({ type: s.kind === "transition" ? "link" : "element", id: s.id });
          }}
          onMove={() => {}}
          onConnect={() => {}}
        />
      </Suspense>
      <Stack
        component="ul"
        aria-label="Состояния, переходы и правила"
        style={{ listStyle: "none", padding: 0 }}
      >
        {rows
          .filter(
            (r) =>
              (origin === "all" || r.origin.kind === origin) &&
              `${"label" in r ? r.label : r.verdict} ${r.id}`
                .toLowerCase()
                .includes(search.toLowerCase()),
          )
          .map((r) => (
            <Group component="li" key={r.id} wrap="wrap">
              <Button
                variant="default"
                disabled={disabled}
                mih={36}
                h="auto"
                styles={{ label: { whiteSpace: "normal", overflowWrap: "anywhere" } }}
                style={{ maxWidth: "100%" }}
                onClick={() =>
                  onSelect({
                    type: payload.transitions.some((t) => t.id === r.id) ? "link" : "element",
                    id: r.id,
                  })
                }
              >
                {"label" in r
                  ? r.label
                  : `Правило: ${name(r.from)} → ${name(r.to)} · ${r.verdict === "forbidden" ? "запрещён" : "разрешён"}`}
              </Button>
              <Badge variant="light">
                {r.origin.kind === "authored" ? "Авторский замысел" : "Исходное утверждение"}
              </Badge>
              {"initial" in r && (
                <Text size="sm">
                  {r.initial ? "Начальное · " : ""}
                  {r.terminal ? "Конечное" : "Состояние"}
                </Text>
              )}
            </Group>
          ))}
      </Stack>
      {selected && (
        <Paper withBorder p="md">
          <Stack aria-label="Lifecycle инспектор">
            <Title order={4} tabIndex={-1} id="lifecycle-inspector-title">
              Основания состояния или перехода
            </Title>
            <Text style={{ overflowWrap: "anywhere" }}>{selected.id}</Text>
            <Text>
              {selected.origin.kind === "authored"
                ? selected.origin.reason
                : "Статическое утверждение источника"}
            </Text>
            {selected.origin.kind === "source_assertion" &&
              selected.origin.evidence.map((e) => (
                <Text key={e.evidenceId} style={{ overflowWrap: "anywhere" }}>
                  Evidence {e.evidenceId} · revision {e.revisionId} · subject {e.subjectId}
                </Text>
              ))}
            {"value" in selected && selected.value && (
              <Text style={{ overflowWrap: "anywhere" }}>Значение JSON: {selected.value.json}</Text>
            )}
            {"from" in selected && (
              <Text>
                {name(selected.from)} → {name(selected.to)}
              </Text>
            )}
            {"guard" in selected && (
              <>
                <Text>
                  {selected.guard.kind === "opaque"
                    ? `Непрозрачное условие: ${selected.guard.text}`
                    : "Условие не задано"}
                </Text>
                {(["triggers", "writes", "events"] as const).map((key) => (
                  <Stack key={key} gap="xs">
                    <Text>
                      {{ triggers: "Операции", writes: "Записи данных", events: "События" }[key]}:{" "}
                      {selected[key].length || "нет подтверждённых связей"}
                    </Text>
                    {selected[key].map((ref, i) => (
                      <Button
                        key={i}
                        variant="default"
                        disabled={disabled}
                        onClick={() => onOpen(ref)}
                      >
                        Открыть {key} {i + 1}
                      </Button>
                    ))}
                  </Stack>
                ))}
              </>
            )}
            {"trigger" in selected && (
              <Button
                variant="default"
                disabled={disabled}
                onClick={() => onOpen(selected.trigger)}
              >
                Открыть операцию правила
              </Button>
            )}
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
export function BackendLifecycleEditor({
  document,
  onChange,
  onSave,
  onCancel,
  busy,
}: {
  document: BackendLifecycleDocument;
  onChange: (d: BackendLifecycleDocument) => void;
  onSave: () => void;
  onCancel: () => void;
  busy: boolean;
}) {
  const [raw, setRaw] = useState<string>();
  const [error, setError] = useState("");
  const [ruleError, setRuleError] = useState("");
  const [label, setLabel] = useState("");
  const [reason, setReason] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [trigger, setTrigger] = useState("");
  const [verdict, setVerdict] = useState<"allowed" | "forbidden">("forbidden");
  const update = (payload: BackendLifecyclePayload) => {
    setRaw(undefined);
    onChange({ ...document, payload });
  };
  const options = [
    { value: "", label: "Выберите состояние" },
    ...document.payload.states.map((s) => ({ value: s.id, label: s.label })),
  ];
  return (
    <Paper withBorder p="md">
      <Stack aria-label="Редактор lifecycle">
        <Title order={3}>Авторская модель жизненного цикла</Title>
        <Text>Правила выражают желаемое поведение. Они не создают исходные переходы.</Text>
        <Group grow>
          <TextInput
            label="Название состояния"
            value={label}
            onChange={(e) => setLabel(e.currentTarget.value)}
            disabled={busy}
          />
          <TextInput
            label="Основание lifecycle"
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
              states: [
                ...document.payload.states,
                {
                  id: crypto.randomUUID(),
                  label,
                  initial: false,
                  terminal: false,
                  origin: { kind: "authored", reason },
                  refs: [],
                },
              ],
            })
          }
        >
          Добавить состояние
        </Button>
        <Group grow>
          <NativeSelect
            label="Из состояния"
            data={options}
            value={from}
            onChange={(e) => setFrom(e.currentTarget.value)}
            disabled={busy}
          />
          <NativeSelect
            label="В состояние"
            data={options}
            value={to}
            onChange={(e) => setTo(e.currentTarget.value)}
            disabled={busy}
          />
          <NativeSelect
            label="Вердикт правила"
            data={[
              { value: "forbidden", label: "Запрещён" },
              { value: "allowed", label: "Разрешён" },
            ]}
            value={verdict}
            onChange={(e) => setVerdict(e.currentTarget.value as typeof verdict)}
            disabled={busy}
          />
        </Group>
        <TextInput
          label="Точная операция правила (Ref JSON)"
          value={trigger}
          onChange={(e) => {
            setTrigger(e.currentTarget.value);
            setRuleError("");
          }}
          disabled={busy}
        />
        <Button
          disabled={busy || !!error || !from || !to || !trigger || !reason.trim()}
          onClick={() => {
            try {
              const ref: BackendDiagramRef = JSON.parse(trigger);
              if (!ref || !["record", "artifact"].includes(ref.kind)) throw new Error();
              update({
                ...document.payload,
                rules: [
                  ...document.payload.rules,
                  {
                    id: crypto.randomUUID(),
                    from,
                    to,
                    trigger: ref,
                    verdict,
                    origin: { kind: "authored", reason },
                  },
                ],
              });
            } catch {
              setRuleError("Укажите точный Ref JSON операции");
            }
          }}
        >
          Добавить авторское правило
        </Button>
        <Textarea
          label="Lifecycle JSON"
          value={raw ?? JSON.stringify(document.payload, null, 2)}
          autosize
          minRows={8}
          maxRows={24}
          disabled={busy}
          onChange={(e) => {
            const text = e.currentTarget.value;
            setRaw(text);
            try {
              const p = JSON.parse(text) as BackendLifecyclePayload;
              if (
                !p ||
                !p.entity ||
                ![p.states, p.transitions, p.rules, p.stateFields].every(Array.isArray) ||
                !p.coverageOrigin ||
                ![...p.states, ...p.transitions, ...p.rules].every(
                  (r) =>
                    r && typeof r.id === "string" && r.origin && typeof r.origin.kind === "string",
                )
              )
                throw new Error();
              onChange({ ...document, payload: p });
              setError("");
            } catch {
              setError("Неверная структура lifecycle JSON; исправьте перед сохранением");
            }
          }}
        />
        {ruleError && (
          <Alert color="red" role="alert">
            {ruleError}
          </Alert>
        )}
        {error && (
          <Alert color="red" role="alert">
            {error}
          </Alert>
        )}
        <Group>
          <Button disabled={busy || !!error} onClick={onSave}>
            Сохранить lifecycle
          </Button>
          <Button variant="default" disabled={busy} onClick={onCancel}>
            Отмена
          </Button>
        </Group>
      </Stack>
    </Paper>
  );
}
