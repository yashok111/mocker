import { useState, useSyncExternalStore } from "react";
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
import type { ResponseRuleStep } from "@/api/generated/schemas";
import type { FormDraftStore } from "../api-designer/forms/formDraftStore";
import { hasUnsafeJsonNumber } from "../api-designer/jsonNumberPrecision";
import {
  blankRule,
  EXTENSION,
  headerTemplate,
  makeNode,
  newID,
  nodeNames,
  portNames,
  ports,
  readRules,
  removeNode,
  writeRules,
  type GraphSelection,
  type ResponseRule,
  type ResponseRuleEdge,
  type ResponseRuleNode,
} from "./model";
import RuleInspector, { inspectorPointer } from "./RuleInspector";
import SimulationPanel from "./SimulationPanel";
import ResponseRuleGraph from "./ResponseRuleGraph";
import { autoLayout } from "./layout";
import { useDiagramLayout } from "../diagram/useDiagramLayout";
import { applyRuleLayout, ruleLayoutInput } from "./elk";
import ExecutionPanel, { type ExecutionControls } from "./ExecutionPanel";
import styles from "./ResponseRules.module.css";

export type ResponseRulesEditorProps = {
  designId: number;
  document: string;
  blocked: string | null;
  formStore: FormDraftStore;
  onChange: (document: string) => void;
  onSource?: (pointer: string) => void;
  execution?: ExecutionControls;
};
export default function ResponseRulesEditor(props: ResponseRulesEditorProps) {
  let parsed: ReturnType<typeof readRules>;
  try {
    parsed = readRules(props.document);
  } catch (cause) {
    return (
      <Stack data-testid="response-rules-editor">
        <Alert color="red" role="alert">
          {cause instanceof SyntaxError
            ? "Исправьте JSON в исходнике API, чтобы открыть правила ответа."
            : cause instanceof Error
              ? cause.message
              : "Неверный формат правил ответа"}
        </Alert>
        {props.onSource && (
          <Button variant="default" onClick={() => props.onSource?.(`/${EXTENSION}`)}>
            Открыть исходник
          </Button>
        )}
      </Stack>
    );
  }
  return <Editor key={props.designId} {...props} parsed={parsed} />;
}
function Editor(props: ResponseRulesEditorProps & { parsed: ReturnType<typeof readRules> }) {
  const { parsed, formStore, document, designId } = props;
  const [selectedId, setSelectedId] = useState("");
  const [selection, setSelection] = useState<GraphSelection>(null);
  const [nodeType, setNodeType] = useState<ResponseRuleNode["type"]>("condition");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [port, setPort] = useState<ResponseRuleEdge["port"]>("next");
  const [error, setError] = useState<string | null>(null);
  const [undo, setUndo] = useState<{ before: string; after: string } | null>(null);
  const [fitRequest, setFitRequest] = useState(0);
  const [active, setActive] = useState<{ signature: string; trace: ResponseRuleStep[] } | null>(
    null,
  );
  const snapshot = useSyncExternalStore(
    formStore.subscribe,
    formStore.getSnapshot,
    formStore.getSnapshot,
  );
  const generation = formStore.serialize();
  const rule = parsed.rules.find((item) => item.id === selectedId) ?? parsed.rules[0];
  const automatic = useDiagramLayout(ruleLayoutInput(rule));
  const signature = JSON.stringify([document, rule?.id, generation]);
  const onTrace = (trace: ResponseRuleStep[]) => setActive({ signature, trace });
  const trace = active?.signature === signature ? active.trace : [];
  const unsafe = hasUnsafeJsonNumber(document);
  const blocked =
    props.blocked ||
    (unsafe
      ? "Документ содержит числа вне точности JavaScript. Для изменений используйте исходник или MCP. Проверка и симуляция доступны с точным исходным текстом."
      : null);
  const disabled = !!blocked || snapshot.dirty;
  const layoutPointer = `/${EXTENSION}/$layout/${rule?.id ?? ""}`;
  const layoutDraft = formStore.get(layoutPointer);
  let preview = rule;
  try {
    if (layoutDraft) preview = JSON.parse(layoutDraft.source) as ResponseRule;
  } catch {
    /* Cancellation remains available. */
  }
  let otherForms = snapshot.dirty;
  try {
    otherForms = Object.keys(JSON.parse(generation)).some(
      (key) => key !== inspectorPointer(rule?.id ?? ""),
    );
  } catch {
    /* Corrupt drafts block mutations. */
  }
  function write(rules: ResponseRule[]) {
    try {
      const next = writeRules(document, rules);
      props.onChange(next);
      setError(null);
      return next;
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Не удалось обновить правило");
      return null;
    }
  }
  function update(next: ResponseRule) {
    return write(parsed.rules.map((item) => (item.id === next.id ? next : item)));
  }
  function create(template: boolean) {
    if (disabled) return;
    const next = autoLayout(template ? headerTemplate() : blankRule());
    if (write([...parsed.rules, next])) {
      setSelectedId(next.id);
      setSelection(null);
    }
  }
  function connect(source: string, output: ResponseRuleEdge["port"], target: string) {
    if (!rule || disabled || rule.edges.length >= 200) return;
    const edge = { id: newID(), from: source, port: output, to: target };
    update({ ...rule, edges: [...rule.edges, edge] });
    setSelection({ kind: "edge", id: edge.id });
  }
  const possibleSources = rule?.nodes.filter((node) => ports(node).length > 0) ?? [];
  const source = possibleSources.find((node) => node.id === from) ?? possibleSources[0];
  const outputs = source ? ports(source) : [];
  const output = outputs.includes(port) ? port : outputs[0];
  const target =
    rule?.nodes.find((node) => node.id === to) ?? rule?.nodes.find((node) => node.type !== "start");
  return (
    <Stack className={styles.root} gap="md" data-testid="response-rules-editor">
      <Text size="sm" c="dimmed">
        Создайте и проверьте правило, сохраните черновик API и примените правило к моку.
      </Text>
      {blocked && (
        <Alert color="yellow">
          {blocked}
          {props.onSource && (
            <Button
              mt="xs"
              display="block"
              size="xs"
              variant="default"
              onClick={() => props.onSource?.(`/${EXTENSION}`)}
            >
              Открыть исходник
            </Button>
          )}
        </Alert>
      )}
      {error && (
        <Alert color="red" role="alert">
          {error}
        </Alert>
      )}
      <Group justify="space-between" align="end">
        {rule ? (
          <NativeSelect
            label="Правило ответа"
            value={rule.id}
            data={parsed.rules.map((item) => ({
              value: item.id,
              label: item.name || "Без названия",
            }))}
            onChange={(event) => {
              setSelectedId(event.currentTarget.value);
              setSelection(null);
            }}
          />
        ) : (
          <Text fw={600}>Правила ответа</Text>
        )}
        <Group gap="xs">
          <Button
            size="xs"
            variant="default"
            disabled={disabled || parsed.rules.length >= 20}
            onClick={() => create(false)}
          >
            Новое правило
          </Button>
          <Button
            size="xs"
            variant="light"
            disabled={disabled || parsed.rules.length >= 20}
            onClick={() => create(true)}
          >
            Проверка заголовка
          </Button>
        </Group>
      </Group>
      {props.execution && <ExecutionPanel execution={props.execution} ruleId={rule?.id} />}
      {!rule ? (
        <Stack align="center" py={40}>
          <Text fw={600}>Выберите ответ по условиям запроса</Text>
          <Text size="sm" c="dimmed">
            Начните с пустого правила или примера с заголовком Authorization.
          </Text>
        </Stack>
      ) : (
        <>
          <Group justify="space-between">
            <Group gap="xs">
              <NativeSelect
                aria-label="Тип нового узла"
                value={nodeType}
                data={Object.entries(nodeNames).map(([value, label]) => ({ value, label }))}
                onChange={(event) =>
                  setNodeType(event.currentTarget.value as ResponseRuleNode["type"])
                }
              />
              <Button
                size="xs"
                disabled={disabled || rule.nodes.length >= 100}
                onClick={() => {
                  const node = makeNode(
                    nodeType,
                    40 + (rule.nodes.length % 3) * 340,
                    60 + Math.floor(rule.nodes.length / 3) * 190,
                  );
                  update({ ...rule, nodes: [...rule.nodes, node] });
                  setSelection({ kind: "node", id: node.id });
                }}
              >
                Добавить узел
              </Button>
              <Button size="xs" variant="subtle" onClick={() => setSelection(null)}>
                Свойства правила
              </Button>
            </Group>
            <Button
              size="xs"
              variant="subtle"
              color="red"
              disabled={disabled}
              onClick={() => {
                if (window.confirm(`Удалить правило «${rule.name}»?`)) {
                  write(parsed.rules.filter((item) => item.id !== rule.id));
                  setSelection(null);
                }
              }}
            >
              Удалить правило
            </Button>
          </Group>
          <Group gap="xs">
            <Button
              size="xs"
              variant="default"
              disabled={disabled || !automatic.layout || !rule.nodes.length}
              loading={automatic.pending}
              onClick={() => {
                if (!automatic.layout) return;
                formStore.set(layoutPointer, {
                  source: JSON.stringify(applyRuleLayout(rule, automatic.layout)),
                  propertySource: JSON.stringify(rule),
                });
                setFitRequest((request) => request + 1);
              }}
            >
              Автораскладка
            </Button>
            {layoutDraft && (
              <>
                <Text size="sm">Предпросмотр расстановки</Text>
                <Button
                  size="xs"
                  disabled={!!blocked || layoutDraft.propertySource !== JSON.stringify(rule)}
                  onClick={() => {
                    if (!preview) return;
                    const next = update(preview);
                    if (next) {
                      setUndo({ before: document, after: next });
                      formStore.remove(layoutPointer);
                    }
                  }}
                >
                  Применить расстановку
                </Button>
                <Button
                  size="xs"
                  variant="default"
                  onClick={() => {
                    formStore.remove(layoutPointer);
                    setFitRequest((request) => request + 1);
                  }}
                >
                  Отменить расстановку
                </Button>
              </>
            )}
            {!layoutDraft && undo?.after === document && (
              <Button
                size="xs"
                variant="subtle"
                disabled={disabled}
                onClick={() => {
                  props.onChange(undo.before);
                  setUndo(null);
                  setFitRequest((request) => request + 1);
                }}
              >
                Вернуть прежнюю расстановку
              </Button>
            )}
          </Group>
          {automatic.error && <Alert color="red">{automatic.error}</Alert>}
          <Text size="xs" c="dimmed">
            Перетаскивайте узлы и соединяйте выходы. Все действия доступны в списках и формах.
            Перетаскивание фона левой кнопкой — перемещение холста; колесо — масштаб.
          </Text>
          <div className={styles.workspace}>
            <div>
              <ResponseRuleGraph
                rule={preview ?? rule}
                layout={automatic.layout}
                fitRequest={fitRequest}
                selection={selection}
                trace={trace}
                blocked={disabled}
                onSelect={setSelection}
                onMove={(positions) => {
                  if (disabled) return;
                  const moved = new Map(positions.map((position) => [position.nodeId, position]));
                  update({
                    ...rule,
                    nodes: rule.nodes.map((node) => {
                      const position = moved.get(node.id);
                      return position ? { ...node, x: position.x, y: position.y } : node;
                    }),
                  });
                }}
                onConnect={connect}
              />
              <div className={styles.lists}>
                <section aria-label="Узлы правила">
                  <Text fw={600} size="sm">
                    Узлы · {rule.nodes.length}
                  </Text>
                  <ul className={styles.list}>
                    {rule.nodes.map((node) => (
                      <li key={node.id}>
                        <UnstyledButton
                          className={styles.row}
                          aria-pressed={selection?.kind === "node" && selection.id === node.id}
                          onClick={() => setSelection({ kind: "node", id: node.id })}
                        >
                          {node.name || "Без названия"}{" "}
                          <Badge size="xs" variant="light">
                            {nodeNames[node.type]}
                          </Badge>
                          {trace.some((step) => step.nodeId === node.id) && (
                            <Text span size="xs">
                              {" "}
                              · ✓ пройден
                            </Text>
                          )}
                        </UnstyledButton>
                      </li>
                    ))}
                  </ul>
                </section>
                <section aria-label="Связи правила">
                  <Text fw={600} size="sm">
                    Связи · {rule.edges.length}
                  </Text>
                  <ul className={styles.list}>
                    {rule.edges.map((edge) => (
                      <li key={edge.id}>
                        <UnstyledButton
                          className={styles.row}
                          aria-pressed={selection?.kind === "edge" && selection.id === edge.id}
                          onClick={() => setSelection({ kind: "edge", id: edge.id })}
                        >
                          {rule.nodes.find((node) => node.id === edge.from)?.name ?? edge.from} →{" "}
                          {rule.nodes.find((node) => node.id === edge.to)?.name ?? edge.to} ·{" "}
                          {portNames[edge.port] ?? edge.port}
                          {trace.some((step) => step.edgeId === edge.id) && " · ✓ пройдена"}
                        </UnstyledButton>
                      </li>
                    ))}
                  </ul>
                </section>
              </div>
              <Group align="end" mt="md" gap="xs">
                <NativeSelect
                  label="Из узла"
                  value={source?.id ?? ""}
                  data={possibleSources.map((node) => ({
                    value: node.id,
                    label: node.name || node.id,
                  }))}
                  onChange={(event) => setFrom(event.currentTarget.value)}
                />
                <NativeSelect
                  label="Выход"
                  value={output ?? ""}
                  data={outputs.map((value) => ({ value, label: portNames[value] ?? value }))}
                  onChange={(event) =>
                    setPort(event.currentTarget.value as ResponseRuleEdge["port"])
                  }
                />
                <NativeSelect
                  label="В узел"
                  value={target?.id ?? ""}
                  data={rule.nodes.map((node) => ({ value: node.id, label: node.name || node.id }))}
                  onChange={(event) => setTo(event.currentTarget.value)}
                />
                <Button
                  size="xs"
                  variant="default"
                  disabled={disabled || !source || !target || !output || rule.edges.length >= 200}
                  onClick={() => {
                    if (source && target && output) connect(source.id, output, target.id);
                  }}
                >
                  Добавить связь
                </Button>
              </Group>
            </div>
            <aside className={styles.inspector} aria-label="Свойства выбранного элемента">
              <RuleInspector
                key={rule.id}
                rule={rule}
                document={parsed.document}
                selection={selection}
                formStore={formStore}
                blocked={!!blocked || otherForms}
                onChange={(next) => update(next) !== null}
                onRemove={() => {
                  if (disabled || !selection) return;
                  update(
                    selection.kind === "node"
                      ? removeNode(rule, selection.id)
                      : { ...rule, edges: rule.edges.filter((edge) => edge.id !== selection.id) },
                  );
                  setSelection(null);
                }}
              />
            </aside>
          </div>
          {otherForms && !layoutDraft && (
            <Alert color="yellow">
              Есть неприменённая форма. Вернитесь к её правилу или вкладке и примените либо отмените
              изменения.
            </Alert>
          )}
          <SimulationPanel
            designId={designId}
            document={document}
            rule={rule}
            generation={generation}
            pendingForm={snapshot.dirty}
            onSelect={setSelection}
            onTrace={onTrace}
            onSource={props.onSource}
            onChangeRule={(next) => !!update(next)}
            examplesBlocked={blocked}
          />
        </>
      )}
    </Stack>
  );
}
