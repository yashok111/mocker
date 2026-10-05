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
  BackendBusinessMapPayload,
  BackendBusinessMapDocument,
  BackendBusinessElement,
  BackendBusinessLink,
  BackendDiagramRef,
  BackendDiagramPin,
  BackendDiagramGap,
  BackendDiagramViewState,
} from "@/api/generated/schemas";
import {
  businessMapGraph,
  businessRoles,
  businessRelations,
  businessRolePair,
} from "./backendBusinessMapGraph";
const Graph = lazy(() =>
  import("./BackendArchitectureGraph").then((m) => ({ default: m.BackendArchitectureGraph })),
);
type Selection = BackendDiagramViewState["selection"];
export function BackendBusinessMap({
  payload,
  gaps,
  selection,
  onSelect,
  onOpen,
  onArchitecture,
  onDesign,
  disabled,
  search = "",
  origin = "all",
  presentation = { positions: [], collapsedIds: [] },
}: {
  payload: BackendBusinessMapPayload;
  gaps: BackendDiagramGap[];
  selection: Selection;
  onSelect: (s: NonNullable<Selection>) => void;
  onOpen: (r: BackendDiagramRef) => void;
  onDesign?: (refs: BackendDiagramRef[]) => void;
  onArchitecture: (pin: BackendDiagramPin, elementId?: string) => void;
  disabled: boolean;
  search?: string;
  origin?: string;
  presentation?: Pick<BackendDiagramViewState, "positions" | "collapsedIds">;
}) {
  const graph = useMemo(
    () => businessMapGraph(payload, { search, origin }),
    [payload, search, origin],
  );
  const rows = [...payload.elements, ...payload.links];
  const selected = rows.find((r) => r.id === selection?.id);
  const name = (id: string) => payload.elements.find((e) => e.id === id)?.label ?? id;
  const visible = (r: BackendBusinessElement | BackendBusinessLink) =>
    (origin === "all" || r.origin.kind === origin) &&
    `${r.id} ${r.label} ${"responsibility" in r ? `${r.role} ${r.responsibility}` : r.relation}`
      .toLowerCase()
      .includes(search.toLowerCase());
  const groups = [...new Set(graph.elements.filter(visible).map((e) => e.responsibility))];
  return (
    <Stack data-testid="backend-business-map" style={{ minWidth: 0, overflowWrap: "anywhere" }}>
      <Title order={3} tabIndex={-1}>
        Карта бизнес-событий
      </Title>
      <Group aria-label="Роли карты">
        {Object.entries(businessRoles).map(([role, label]) => (
          <Badge key={role} variant="outline">
            {label}
          </Badge>
        ))}
      </Group>
      <Text>
        Расположение карточек не задаёт причинность. Политики описывают замысел; исполнение не
        проверено.
      </Text>
      {payload.architecture && (
        <Button
          variant="default"
          disabled={disabled}
          onClick={() => onArchitecture(payload.architecture!)}
        >
          Открыть точную C4 архитектуру
        </Button>
      )}
      <Suspense fallback={<Loader aria-label="Загрузка карты бизнес-событий" />}>
        <Graph
          elements={graph.elements}
          links={graph.links}
          state={{
            ...presentation,
            diagram: payload.architecture ?? { id: "", version: 1, contentHash: "" },
            search,
            origin: origin as BackendDiagramViewState["origin"],
            selection,
          }}
          onSelect={(s) => {
            if (!disabled) onSelect(s);
          }}
          graphName="Business map"
        />
      </Suspense>
      <Stack aria-label="Карточки по ответственности">
        {groups.map((group) => (
          <section key={group}>
            <Title order={4}>{group || "Ответственность не указана"}</Title>
            <Stack component="ul" style={{ listStyle: "none", padding: 0 }}>
              {graph.elements
                .filter((e) => e.responsibility === group && visible(e))
                .map((e) => (
                  <Group component="li" key={e.id}>
                    <Button
                      variant="default"
                      disabled={disabled}
                      h="auto"
                      mih={36}
                      styles={{ label: { whiteSpace: "normal", overflowWrap: "anywhere" } }}
                      style={{ maxWidth: "100%" }}
                      onClick={() => onSelect({ type: "element", id: e.id })}
                    >
                      {e.label}
                    </Button>
                    <Text size="sm">
                      {businessRoles[e.role]} ·{" "}
                      {e.origin.kind === "authored" ? "Авторский замысел" : "Исходное утверждение"}
                    </Text>
                  </Group>
                ))}
            </Stack>
          </section>
        ))}
      </Stack>
      <Stack
        component="ul"
        aria-label="Явные связи карты"
        style={{ listStyle: "none", padding: 0 }}
      >
        {payload.links.filter(visible).map((l) => (
          <li key={l.id}>
            <Button
              variant="subtle"
              disabled={disabled}
              h="auto"
              mih={36}
              styles={{ label: { whiteSpace: "normal", overflowWrap: "anywhere" } }}
              style={{ maxWidth: "100%" }}
              onClick={() => onSelect({ type: "link", id: l.id })}
            >
              {name(l.from)} → {name(l.to)} · {businessRelations[l.relation]}
            </Button>
          </li>
        ))}
      </Stack>
      {selected && (
        <Paper withBorder p="md">
          <Stack aria-label="Business map инспектор">
            <Title order={4} tabIndex={-1} id="business-map-inspector-title">
              Основания бизнес-карты
            </Title>
            <Text>
              {selected.label} · {selected.id}
            </Text>
            <Text>
              {selected.origin.kind === "authored"
                ? selected.origin.reason
                : "Статическое утверждение источника"}
            </Text>
            {"role" in selected && selected.role === "question" && (
              <Text>Вопрос автора, не исходный факт и не диагностическая находка.</Text>
            )}
            {"role" in selected && selected.role === "business_event" && (
              <Text>
                Бизнес-событие имеет собственную идентичность; технические сообщения — его явные
                соответствия.
              </Text>
            )}
            {selected.origin.kind === "source_assertion" &&
              selected.origin.evidence.map((e) => (
                <Text key={`${e.revisionId}:${e.evidenceId}`}>
                  Evidence {e.evidenceId} · revision {e.revisionId} · subject {e.subjectId}
                </Text>
              ))}
            {"architectureElementId" in selected &&
              selected.architectureElementId &&
              payload.architecture && (
                <Button
                  variant="default"
                  disabled={disabled}
                  onClick={() =>
                    onArchitecture(payload.architecture!, selected.architectureElementId)
                  }
                >
                  Открыть C4 элемент
                </Button>
              )}
            {onDesign && (
              <Button
                disabled={disabled || selected.refs.length === 0}
                onClick={() => onDesign(selected.refs)}
              >
                Спроектировать изменение
              </Button>
            )}
            {selected.refs.length === 0 && (
              <Text>Реализация не установлена — нет точных соответствий.</Text>
            )}
            {selected.refs.map((ref, i) => (
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

export function BackendBusinessMapEditor({
  document,
  onChange,
  onSave,
  onCancel,
  busy,
}: {
  document: BackendBusinessMapDocument;
  onChange: (d: BackendBusinessMapDocument) => void;
  onSave: () => void;
  onCancel: () => void;
  busy: boolean;
}) {
  const [label, setLabel] = useState(""),
    [reason, setReason] = useState(""),
    [responsibility, setResponsibility] = useState("");
  const [role, setRole] = useState<BackendBusinessElement["role"]>("actor");
  const [from, setFrom] = useState(""),
    [to, setTo] = useState(""),
    [relation, setRelation] = useState<BackendBusinessLink["relation"]>("initiates");
  const [raw, setRaw] = useState<string>(),
    [error, setError] = useState(""),
    [linkError, setLinkError] = useState("");
  const [editing, setEditing] = useState("");
  const update = (payload: BackendBusinessMapPayload) => {
    setRaw(undefined);
    onChange({ ...document, payload });
  };
  const options = [
    { value: "", label: "Выберите элемент" },
    ...document.payload.elements.map((e) => ({ value: e.id, label: e.label })),
  ];
  return (
    <Paper withBorder p="md">
      <Stack aria-label="Редактор business map">
        <Title order={3}>Авторская карта бизнес-событий</Title>
        <Text>Изменение карты не меняет API, сценарий или proposal. Связи задаются явно.</Text>
        <NativeSelect
          label="Редактировать карточку"
          data={[{ value: "", label: "Новая карточка" }, ...options.slice(1)]}
          value={editing}
          disabled={busy || !!error}
          onChange={(e) => {
            const id = e.currentTarget.value;
            setEditing(id);
            const item = document.payload.elements.find((x) => x.id === id);
            setLabel(item?.label ?? "");
            setRole(item?.role ?? "actor");
            setResponsibility(item?.responsibility ?? "");
            setReason(item?.origin.kind === "authored" ? item.origin.reason : "");
          }}
        />
        <Group grow>
          <TextInput
            label="Название карточки"
            value={label}
            onChange={(e) => setLabel(e.currentTarget.value)}
            disabled={busy}
          />
          <NativeSelect
            label="Роль карточки"
            data={Object.entries(businessRoles).map(([value, label]) => ({ value, label }))}
            value={role}
            onChange={(e) => setRole(e.currentTarget.value as typeof role)}
            disabled={busy}
          />
        </Group>
        <TextInput
          label="Ответственность"
          value={responsibility}
          onChange={(e) => setResponsibility(e.currentTarget.value)}
          disabled={busy}
        />
        <TextInput
          label="Основание замысла"
          value={reason}
          onChange={(e) => setReason(e.currentTarget.value)}
          disabled={busy}
        />
        <Button
          disabled={busy || !!error || !label.trim() || !reason.trim()}
          onClick={() => {
            const existing = document.payload.elements.find((e) => e.id === editing);
            const element: BackendBusinessElement = {
              ...(existing ?? { id: crypto.randomUUID(), refs: [] }),
              label,
              role,
              responsibility,
              origin: { kind: "authored", reason },
            };
            update({
              ...document.payload,
              elements: existing
                ? document.payload.elements.map((e) => (e.id === editing ? element : e))
                : [...document.payload.elements, element],
            });
          }}
        >
          {editing ? "Обновить карточку" : "Добавить карточку"}
        </Button>
        <Group grow>
          <NativeSelect
            label="От элемента"
            data={options}
            value={from}
            onChange={(e) => setFrom(e.currentTarget.value)}
            disabled={busy}
          />
          <NativeSelect
            label="К элементу"
            data={options}
            value={to}
            onChange={(e) => setTo(e.currentTarget.value)}
            disabled={busy}
          />
          <NativeSelect
            label="Отношение"
            data={Object.entries(businessRelations).map(([value, label]) => ({ value, label }))}
            value={relation}
            onChange={(e) => {
              setRelation(e.currentTarget.value as typeof relation);
              setLinkError("");
            }}
            disabled={busy}
          />
        </Group>
        <Button
          disabled={busy || !!error || !from || !to || !reason.trim()}
          onClick={() => {
            const a = document.payload.elements.find((e) => e.id === from),
              b = document.payload.elements.find((e) => e.id === to);
            if (
              !a ||
              !b ||
              !businessRolePair(a.role, b.role, relation) ||
              (relation === "questions" && from === to)
            ) {
              setLinkError(
                "Связь не разрешена для выбранной пары ролей (роль источника и назначения)",
              );
              return;
            }
            setLinkError("");
            update({
              ...document.payload,
              links: [
                ...document.payload.links,
                {
                  id: crypto.randomUUID(),
                  label: businessRelations[relation],
                  from,
                  to,
                  relation,
                  origin: { kind: "authored", reason },
                  refs: [],
                },
              ],
            });
          }}
        >
          Добавить связь
        </Button>
        {linkError && (
          <Alert color="red" role="alert">
            {linkError}
          </Alert>
        )}
        <Textarea
          label="Business map JSON"
          value={raw ?? JSON.stringify(document.payload, null, 2)}
          autosize
          minRows={8}
          maxRows={24}
          disabled={busy}
          onChange={(e) => {
            const text = e.currentTarget.value;
            setRaw(text);
            try {
              const p = JSON.parse(text) as BackendBusinessMapPayload;
              if (
                !p ||
                ![p.elements, p.links].every(Array.isArray) ||
                ![...p.elements, ...p.links].every(
                  (r) =>
                    r &&
                    typeof r.id === "string" &&
                    typeof r.label === "string" &&
                    r.origin &&
                    Array.isArray(r.refs),
                )
              )
                throw new Error();
              onChange({ ...document, payload: p });
              setError("");
            } catch {
              setError("Неверная структура business map JSON; исправьте перед сохранением");
            }
          }}
        />
        {error && (
          <Alert color="red" role="alert">
            {error}
          </Alert>
        )}
        <Group>
          <Button disabled={busy || !!error} onClick={onSave}>
            Сохранить business map
          </Button>
          <Button variant="default" disabled={busy} onClick={onCancel}>
            Отмена
          </Button>
        </Group>
      </Stack>
    </Paper>
  );
}
