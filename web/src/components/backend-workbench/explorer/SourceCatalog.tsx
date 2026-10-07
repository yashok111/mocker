import { useEffect, useMemo, useRef } from "react";
import { usePinnedValue } from "../backendFlowReads";
import { Badge, Text, TextInput } from "@mantine/core";
import { IconChevronRight, IconSearch } from "@tabler/icons-react";
import type { BackendWorkspaceSearch } from "../backendWorkspaceSearch";
import { kindName, nodeSubtitle, type MapData, type MapNode } from "./model";
import styles from "./Explorer.module.css";
import { useCardClicks } from "./cardClicks";

export function catalogScope(search: BackendWorkspaceSearch) {
  return JSON.stringify({
    wbMode: search.wbMode,
    wbKind: search.wbKind,
    wbScope: search.wbScope,
    wbGroup: search.wbGroup,
    wbQuery: search.wbQuery,
    wbFilter: search.wbFilter,
  });
}
export function restoreCatalog(encoded?: string): BackendWorkspaceSearch | undefined {
  if (!encoded) return;
  try {
    const value = JSON.parse(encoded);
    if (!value || !["children", "collections", "objects", "flow"].includes(value.wbMode)) return;
    const result: BackendWorkspaceSearch = { wbMode: value.wbMode };
    for (const key of ["wbKind", "wbScope", "wbGroup", "wbQuery", "wbFilter"] as const)
      if (typeof value[key] === "string") result[key] = value[key];
    return result;
  } catch {
    return;
  }
}
function known(value: unknown): string | undefined {
  if (typeof value === "string") return value;
  if (
    value &&
    typeof value === "object" &&
    "status" in value &&
    value.status === "known" &&
    "value" in value &&
    typeof value.value === "string"
  )
    return value.value;
}
export function triggerLabel(node: MapNode) {
  const trigger = node.attributes.trigger;
  if (!trigger || typeof trigger !== "object" || !("kind" in trigger))
    return node.kind === "job" ? "Запуск не уточнён" : nodeSubtitle(node);
  if (trigger.kind === "cron" && "expression" in trigger) {
    const expression = known(trigger.expression);
    const daily = expression?.match(/^(\d+) (\d+) \* \* \*$/);
    if (daily && Number(daily[1]) < 60 && Number(daily[2]) < 24)
      return `Ежедневно в ${daily[2]!.padStart(2, "0")}:${daily[1]!.padStart(2, "0")}`;
    const periodic = expression?.match(/^\*\/(\d+) (\*|\d+-\d+) \* \* \*$/);
    if (periodic)
      return `${periodic[1] === "1" ? "Каждую минуту" : `Каждые ${periodic[1]} мин`}${periodic[2] === "*" ? "" : ` · ${periodic[2]} ч`}`;
    if (expression === "* * * * *") return "Каждую минуту";
    return expression ? `Cron · ${expression}` : "Расписание не уточнено";
  }
  return trigger.kind === "manual" ? "По команде" : String(trigger.kind);
}
export function purposeText(node: MapNode) {
  if (["symbol", "handler", "flow_step"].includes(node.kind)) return;
  const text =
    node.description ||
    (typeof node.attributes.description === "string" ? node.attributes.description : "");
  if (!text || /^(The exact CLI Name\/Action|Static crontab|Go function|Go method)/.test(text))
    return;
  return text;
}

export function SourceCatalog({
  data,
  onOpen,
  selected,
  compact = false,
  scroll = 0,
  onScroll,
  filter = "",
  onFilter,
}: {
  data: MapData;
  onOpen: (node: MapNode) => void;
  selected?: string;
  compact?: boolean;
  scroll?: number;
  onScroll?: (value: number) => void;
  filter?: string;
  onFilter?: (value: string) => void;
}) {
  const [query, setQuery] = usePinnedValue(filter, filter);
  const open = (id: string) => {
    const node = data.nodes.find((n) => n.id === id);
    if (node) onOpen(node);
  };
  const clicks = useCardClicks(open, open, data);
  const host = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (host.current) host.current.scrollTop = scroll;
  }, [data, scroll]);
  const nodes = useMemo(() => {
    const text = query.trim().toLocaleLowerCase();
    return data.nodes.filter(
      (n) =>
        !text ||
        `${n.name} ${nodeSubtitle(n)} ${triggerLabel(n)}`.toLocaleLowerCase().includes(text),
    );
  }, [data.nodes, query]);
  return (
    <section className={compact ? styles.catalogCompact : styles.catalog} aria-label={data.title}>
      <div className={styles.catalogTools}>
        <Text fw={600} size="sm">
          {data.title}
        </Text>
        <Text size="xs" c="dimmed">
          {nodes.length} объектов
        </Text>
        <TextInput
          value={query}
          onChange={(e) => {
            setQuery(e.currentTarget.value);
            onFilter?.(e.currentTarget.value);
          }}
          placeholder="Найти в разделе"
          aria-label="Поиск в каталоге"
          size="xs"
          leftSection={<IconSearch size={14} />}
        />
      </div>
      <div
        ref={host}
        className={styles.catalogRows}
        onScroll={(e) => onScroll?.(e.currentTarget.scrollTop)}
      >
        {nodes.map((node) => (
          <button
            key={node.id}
            className={styles.catalogRow}
            onClick={(e) => clicks.click(node.id, e.detail)}
            onDoubleClick={() => clicks.doubleClick(node.id)}
            aria-current={selected === node.id ? "page" : undefined}
          >
            <span className={styles.catalogIdentity}>
              <strong>{node.name}</strong>
              <span>{node.group ? node.description : triggerLabel(node)}</span>
              {!node.group && purposeText(node) && <span>{purposeText(node)}</span>}
            </span>
            {!compact && (
              <Badge variant="light" color="gray">
                {node.group ? node.badge : kindName(node.kind)}
              </Badge>
            )}
            <IconChevronRight size={14} aria-hidden="true" />
          </button>
        ))}
        {!nodes.length && (
          <Text size="sm" c="dimmed" p="md">
            Нет подходящих объектов.
          </Text>
        )}
      </div>
      {data.partial && (
        <details className={styles.catalogNotes}>
          <summary>Детализация модели</summary>
          <Text size="xs">{data.partial}</Text>
        </details>
      )}
    </section>
  );
}
