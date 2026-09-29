import { useState } from "react";
import {
  Alert,
  Badge,
  Button,
  Group,
  NativeSelect,
  Stack,
  Text,
  UnstyledButton,
} from "@mantine/core";
import type { ApiDocument } from "../api-designer/documentModel";
import { useDiagramLayout } from "../diagram/useDiagramLayout";
import { applyStateLayout, stateLayoutInput } from "./layout";
import {
  blankDiagram,
  newID,
  orderTemplate,
  readDiagrams,
  writeDiagrams,
  type Selection,
  type StateDiagram,
} from "./model";
import StateGraph from "./StateGraph";
import StateInspector from "./StateInspector";
import StateSimulation from "./StateSimulation";
import styles from "./StateDiagram.module.css";

export default function StateDiagramEditor({
  designId,
  document,
  blocked,
  onChange,
}: {
  designId: number;
  document: ApiDocument | null;
  blocked: boolean;
  onChange: (d: ApiDocument) => void;
}) {
  if (!document)
    return (
      <Alert color="yellow">Исправьте JSON в редакторе, чтобы открыть диаграммы состояний.</Alert>
    );
  if (blocked)
    return (
      <Alert color="yellow">
        Завершите редактирование формы. Если документ содержит числа вне точности JavaScript,
        используйте исходник или MCP.
      </Alert>
    );
  let diagrams: StateDiagram[];
  try {
    diagrams = readDiagrams(document);
  } catch (e) {
    return (
      <Alert color="red" role="alert">
        {e instanceof Error ? e.message : "Неверный формат диаграмм"}
      </Alert>
    );
  }
  return <Editor designId={designId} document={document} diagrams={diagrams} onChange={onChange} />;
}
function Editor({
  designId,
  document,
  diagrams,
  onChange,
}: {
  designId: number;
  document: ApiDocument;
  diagrams: StateDiagram[];
  onChange: (d: ApiDocument) => void;
}) {
  const [selectedID, setSelectedID] = useState("");
  const [selection, setSelection] = useState<Selection>(null);
  const [active, setActive] = useState<{ signature: string; id?: string } | null>(null);
  const diagram = diagrams.find((d) => d.id === selectedID) ?? diagrams[0];
  const automatic = useDiagramLayout(stateLayoutInput(diagram));
  const signature = JSON.stringify(diagram);
  function create(template: boolean) {
    const next = template ? orderTemplate() : blankDiagram();
    onChange(writeDiagrams(document, [...diagrams, next]));
    setSelectedID(next.id);
    setSelection(null);
  }
  function update(next: StateDiagram) {
    onChange(
      writeDiagrams(
        document,
        diagrams.map((d) => (d.id === next.id ? next : d)),
      ),
    );
  }
  function addState() {
    if (!diagram) return;
    const id = newID();
    update({
      ...diagram,
      initialStateId: diagram.initialStateId || id,
      states: [
        ...diagram.states,
        {
          id,
          name: `Состояние ${diagram.states.length + 1}`,
          x: 60 + (diagram.states.length % 3) * 250,
          y: 70 + Math.floor(diagram.states.length / 3) * 170,
          terminal: false,
        },
      ],
    });
    setSelection({ kind: "state", id });
  }
  function connect(from: string, to: string) {
    if (!diagram || diagram.transitions.length >= 300) return;
    const id = newID();
    update({
      ...diagram,
      transitions: [
        ...diagram.transitions,
        { id, name: "Новый переход", from, to, patchJSON: "{}", responseStatus: 200 },
      ],
    });
    setSelection({ kind: "transition", id });
  }
  return (
    <Stack className={styles.root} gap="md" data-testid="state-diagram-editor">
      <Group justify="space-between" align="end">
        {diagram ? (
          <NativeSelect
            label="Диаграмма состояний"
            value={diagram.id}
            data={diagrams.map((d) => ({ value: d.id, label: d.name || "Без названия" }))}
            onChange={(e) => {
              setSelectedID(e.currentTarget.value);
              setSelection(null);
            }}
          />
        ) : (
          <Text fw={650}>Диаграммы состояний</Text>
        )}
        <Group gap="xs">
          <Button
            variant="default"
            size="xs"
            disabled={diagrams.length >= 20}
            onClick={() => create(false)}
          >
            Новая диаграмма
          </Button>
          <Button
            variant="light"
            size="xs"
            disabled={diagrams.length >= 20}
            onClick={() => create(true)}
          >
            Пример заказа
          </Button>
        </Group>
      </Group>
      {!diagram ? (
        <Stack align="center" py={60}>
          <Text fw={600}>Спроектируйте жизненный цикл сущности</Text>
          <Text c="dimmed" ta="center" maw={420}>
            Добавьте состояния, соедините их переходами и свяжите действия с операциями API.
          </Text>
        </Stack>
      ) : (
        <>
          <Group justify="space-between">
            <Group gap="xs">
              <Button size="xs" disabled={diagram.states.length >= 100} onClick={addState}>
                Добавить состояние
              </Button>
              <Button
                variant="default"
                size="xs"
                disabled={!diagram.states.length || diagram.transitions.length >= 300}
                onClick={() => {
                  const from = selection?.kind === "state" ? selection.id : diagram.states[0]!.id;
                  connect(from, diagram.states.find((s) => s.id !== from)?.id ?? from);
                }}
              >
                Добавить переход
              </Button>
              <Button variant="subtle" size="xs" onClick={() => setSelection(null)}>
                Настройки диаграммы
              </Button>
            </Group>
            <Button
              size="xs"
              color="red"
              variant="subtle"
              onClick={() => {
                if (window.confirm(`Удалить диаграмму «${diagram.name}»?`)) {
                  onChange(
                    writeDiagrams(
                      document,
                      diagrams.filter((d) => d.id !== diagram.id),
                    ),
                  );
                  setSelection(null);
                }
              }}
            >
              Удалить диаграмму
            </Button>
          </Group>
          <Group gap="xs">
            <Button
              size="xs"
              variant="default"
              loading={automatic.pending}
              disabled={!diagram.states.length || !automatic.layout}
              onClick={() => {
                if (automatic.layout) update(applyStateLayout(diagram, automatic.layout));
              }}
            >
              Автораскладка
            </Button>
          </Group>
          {automatic.error && <Alert color="red">{automatic.error}</Alert>}
          <Text size="xs" c="dimmed">
            Перетаскивайте состояния. Соедините точки на фигурах или добавьте переход кнопкой.
            Перетаскивание фона левой кнопкой — перемещение холста; колесо — масштаб.
          </Text>
          <div className={styles.workspace}>
            <div>
              <StateGraph
                diagram={diagram}
                layout={automatic.layout}
                selection={selection}
                activeState={active?.signature === signature ? active.id : undefined}
                onSelect={setSelection}
                onConnect={connect}
                onMove={(id, x, y) =>
                  update({
                    ...diagram,
                    states: diagram.states.map((s) => (s.id === id ? { ...s, x, y } : s)),
                  })
                }
              />
              <div className={styles.lists}>
                <section aria-label="Состояния">
                  <Text fw={600} size="sm" mb={4}>
                    Состояния · {diagram.states.length}
                  </Text>
                  <ul className={styles.list}>
                    {diagram.states.map((s) => (
                      <li key={s.id}>
                        <UnstyledButton
                          className={styles.row}
                          aria-pressed={selection?.kind === "state" && selection.id === s.id}
                          onClick={() => setSelection({ kind: "state", id: s.id })}
                        >
                          <Text size="sm" span>
                            {s.name}
                          </Text>{" "}
                          {s.id === diagram.initialStateId ? (
                            <Badge size="xs" variant="light">
                              начальное
                            </Badge>
                          ) : null}{" "}
                          {s.terminal ? (
                            <Badge size="xs" color="gray">
                              конечное
                            </Badge>
                          ) : null}
                        </UnstyledButton>
                      </li>
                    ))}
                  </ul>
                </section>
                <section aria-label="Переходы">
                  <Text fw={600} size="sm" mb={4}>
                    Переходы · {diagram.transitions.length}
                  </Text>
                  <ul className={styles.list}>
                    {diagram.transitions.map((t) => (
                      <li key={t.id}>
                        <UnstyledButton
                          className={styles.row}
                          aria-pressed={selection?.kind === "transition" && selection.id === t.id}
                          onClick={() => setSelection({ kind: "transition", id: t.id })}
                        >
                          <Text size="sm">{t.name}</Text>
                          <Text size="xs" c="dimmed">
                            {diagram.states.find((s) => s.id === t.from)?.name ?? "?"} →{" "}
                            {diagram.states.find((s) => s.id === t.to)?.name ?? "?"}
                          </Text>
                        </UnstyledButton>
                      </li>
                    ))}
                  </ul>
                </section>
              </div>
            </div>
            <aside className={styles.inspector} aria-label="Свойства диаграммы">
              <StateInspector
                diagram={diagram}
                document={document}
                selection={selection}
                onChange={update}
                onSelect={setSelection}
              />
            </aside>
          </div>
          <StateSimulation
            key={diagram.id + JSON.stringify(document)}
            designId={designId}
            diagram={diagram}
            document={document}
            onActiveState={(id) => setActive({ signature, id })}
          />
        </>
      )}
    </Stack>
  );
}
