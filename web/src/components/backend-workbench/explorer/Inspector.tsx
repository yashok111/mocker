import { useEffect, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { ActionIcon, Alert, Badge, Button, Group, Loader, Text } from "@mantine/core";
import { IconArrowRight, IconCopy, IconX } from "@tabler/icons-react";
import type { BackendReadTarget } from "@/api/generated/schemas";
import { readBackendNode, readBackendEvidence, readBackendGraph } from "../backendGraphReads";
import { backendReadTargetKey } from "../backendReadTargets";
import type { BackendWorkspaceSearch } from "../backendWorkspaceSearch";
import { nodeOf } from "./reads";
import { resolvedTargetSearch } from "./navigation";
import { kindName, nodeSubtitle, type MapNode, type MapEdge } from "./model";
import { nativeSources } from "./nativeSources";
import styles from "./Explorer.module.css";
export function Inspector({
  projectId,
  target,
  search,
  node,
  edge: providedEdge,
  id,
  onClose,
  onNavigate,
  onEnter,
  targetHash,
  canEnter,
  enterLabel,
  navigationActions,
}: {
  targetHash?: string;
  canEnter?: boolean;
  enterLabel?: string;
  navigationActions?: ReactNode;
  projectId: string;
  target: BackendReadTarget;
  search: BackendWorkspaceSearch;
  node?: MapNode;
  edge?: MapEdge;
  id: string;
  onClose: () => void;
  onNavigate: (s: BackendWorkspaceSearch) => void;
  onEnter: (n: MapNode) => void;
}) {
  useEffect(() => {
    const closeOnEscape = (event: KeyboardEvent) => {
      if (
        event.key !== "Escape" ||
        event.defaultPrevented ||
        document.querySelector('[role="dialog"]')
      )
        return;
      event.preventDefault();
      onClose();
    };
    window.addEventListener("keydown", closeOnEscape);
    return () => window.removeEventListener("keydown", closeOnEscape);
  }, [onClose]);
  const evidenceTarget = node?.evidenceTarget ?? providedEdge?.evidenceTarget ?? target;
  const [copied, setCopied] = useState(false);
  const synthetic = id.includes(":");
  const source = !targetHash && !node?.refs && !providedEdge?.refs && !synthetic;
  const edgeQuery = useQuery({
    queryKey: ["workbench-edge", projectId, backendReadTargetKey(evidenceTarget), id],
    enabled: source && search.recordType === "edge" && !providedEdge,
    retry: false,
    staleTime: Infinity,
    queryFn: ({ signal }) =>
      readBackendGraph(projectId, evidenceTarget, { recordType: "edges", id, limit: 1 }, signal),
  });
  const edgeRecord = edgeQuery.data?.edges[0];
  const edge: MapEdge | undefined =
    providedEdge ??
    (edgeRecord
      ? {
          id: edgeRecord.id,
          kind: edgeRecord.kind,
          from: edgeRecord.from,
          to: edgeRecord.to,
          label:
            "label" in edgeRecord.attributes
              ? String(edgeRecord.attributes.label)
              : edgeRecord.kind,
          witness: edgeRecord,
        }
      : undefined);
  const detail = useQuery({
    queryKey: ["workbench-inspect", projectId, backendReadTargetKey(evidenceTarget), id],
    enabled: source && search.recordType !== "edge",
    retry: false,
    staleTime: Infinity,
    queryFn: ({ signal }) => readBackendNode(projectId, evidenceTarget, id, signal),
  });
  const raw = detail.data && ("node" in detail.data ? detail.data.node : detail.data);
  const selected = node ?? (raw && "attributes" in raw && "kind" in raw ? nodeOf(raw) : undefined);
  const title = selected?.name ?? edge?.label ?? "Выбранный объект";
  const snippets = nativeSources(
    raw && "attributes" in raw ? raw.attributes : selected?.attributes,
  );
  const showSource = snippets.length > 0 || selected?.kind === "flow_step";
  const evidence = useQuery({
    queryKey: ["workbench-evidence", projectId, backendReadTargetKey(evidenceTarget), id],
    enabled: source && showSource,
    retry: false,
    staleTime: Infinity,
    queryFn: ({ signal }) =>
      readBackendEvidence(projectId, evidenceTarget, { subjectId: id, limit: 20 }, signal),
  });
  const locations = [
    ...new Map(
      evidence.data?.items.map((item) => [JSON.stringify(item.source), item.source]),
    ).values(),
  ];
  const pin = resolvedTargetSearch(evidenceTarget);
  return (
    <aside className={styles.inspector} aria-label="Сведения об объекте">
      <Group justify="space-between" align="flex-start" wrap="nowrap">
        <Badge color="gray" variant="light">
          {selected ? kindName(selected.kind) : "Связь"}
        </Badge>
        <ActionIcon aria-label="Закрыть сведения" variant="subtle" onClick={onClose}>
          <IconX size={17} />
        </ActionIcon>
      </Group>
      <h2 className={styles.inspectorTitle}>{title}</h2>
      {selected?.description && selected.description !== title && (
        <Text size="sm" c="dimmed">
          {selected.description}
        </Text>
      )}
      {selected && nodeSubtitle(selected) && (
        <Text size="xs" mt="sm" ff="monospace" style={{ overflowWrap: "anywhere" }}>
          {nodeSubtitle(selected)}
        </Text>
      )}
      {detail.isError && (
        <Alert mt="md" color="red">
          Объект в этой точной версии недоступен.
        </Alert>
      )}
      {!selected && !edge && detail.isPending && !synthetic && <Loader size="sm" />}
      {selected?.attributes.stepKind === "opaque" && (
        <Alert color="yellow" mt="md">
          Внутренняя логика ещё не детализирована.
        </Alert>
      )}
      {selected && (
        <div className={styles.inspectorActions}>
          {navigationActions}
          {selected.change !== "removed" &&
            (canEnter ??
              (selected.childCount > 0 ||
                selected.group ||
                [
                  "http_operation",
                  "handler",
                  "symbol",
                  "job",
                  "flow",
                  "table",
                  "datastore",
                  "data_store",
                  "service",
                  "application",
                  "component",
                  "system",
                  "software_system",
                ].includes(selected.kind))) && (
              <Button
                variant="light"
                rightSection={<IconArrowRight size={15} />}
                onClick={() => onEnter(selected)}
              >
                {enterLabel ??
                  (selected.kind === "symbol"
                    ? "Связи по исходникам"
                    : ["http_operation", "handler", "job"].includes(selected.kind)
                      ? "Логика операции"
                      : selected.kind === "table"
                        ? "Связи и поля"
                        : selected.kind === "flow"
                          ? "Открыть логику"
                          : "Что внутри")}
              </Button>
            )}
          {["column", "api_field", "representation_field"].includes(selected.kind) && (
            <Button
              variant="subtle"
              onClick={() =>
                onNavigate({
                  ...pin,
                  wbView: "data",
                  wbMode: "lineage",
                  recordId: selected.id,
                  wbScope: selected.id,
                })
              }
            >
              Происхождение значения
            </Button>
          )}
          {selected.kind === "http_operation" && (
            <Button
              variant="subtle"
              onClick={() =>
                onNavigate({
                  ...pin,
                  wbView: "structure",
                  wbMode: "children",
                  wbScope: selected.id,
                  recordId: undefined,
                })
              }
            >
              Поля запроса и ответа
            </Button>
          )}
          {!!selected.attributes.valueRef &&
            typeof selected.attributes.valueRef === "object" &&
            "nodeId" in selected.attributes.valueRef && (
              <Button
                variant="subtle"
                onClick={() =>
                  onNavigate({
                    ...pin,
                    wbView: "data",
                    wbMode: "lineage",
                    wbScope: String((selected.attributes.valueRef as { nodeId: string }).nodeId),
                    wbLineageSeed: JSON.stringify(selected.attributes.valueRef),
                  })
                }
              >
                Происхождение этого значения
              </Button>
            )}
          {selected.origin && (!showSource || selected.origin !== "Исходный код") && (
            <Text size="xs" c="dimmed">
              Основание: {selected.origin}
            </Text>
          )}
        </div>
      )}
      {showSource && (
        <section className={styles.sourceCode} aria-label="Исходный код">
          <Text component="h3" size="xs" fw={600} mb={6}>
            Исходный код
          </Text>
          {evidence.isFetching && <Loader size="xs" aria-label="Загружаем место в исходнике" />}
          {evidence.isError && (
            <Text size="xs" c="dimmed">
              Не удалось загрузить файл и строки.{" "}
              <Button variant="subtle" size="compact-xs" onClick={() => void evidence.refetch()}>
                Повторить
              </Button>
            </Text>
          )}
          {snippets.map(({ field, label, text }) => (
            <details
              key={`${id}:${backendReadTargetKey(evidenceTarget)}:${field}`}
              className={styles.sourceCodeDetails}
            >
              <summary>
                {snippets.length === 1 ? "Показать код" : `Показать код · ${label}`}
              </summary>
              {/* oxlint-disable jsx-a11y/no-noninteractive-tabindex -- The bounded code pane must support keyboard scrolling. */}
              <pre
                className={styles.sourceCodeBlock}
                tabIndex={0}
                aria-label="Фрагмент исходного кода"
              >
                <code>{text}</code>
              </pre>
              {/* oxlint-enable jsx-a11y/no-noninteractive-tabindex */}
            </details>
          ))}
          {locations.map((location, index) => (
            <Text key={index} size="xs" ff="monospace" style={{ overflowWrap: "anywhere" }}>
              {location.file}
              {location.startLine
                ? `:${location.startLine}${location.endLine && location.endLine !== location.startLine ? `–${location.endLine}` : ""}`
                : ""}
            </Text>
          ))}
          {!evidence.isFetching && !evidence.isError && locations.length === 0 && (
            <Text size="xs" c="dimmed">
              Файл и строки не указаны.
            </Text>
          )}
        </section>
      )}
      {selected?.details && (
        <dl className={styles.metadata}>
          {Object.entries(selected.details).map(([label, value]) => (
            <div key={label} style={{ display: "contents" }}>
              <dt>{label}</dt>
              <dd>{value}</dd>
            </div>
          ))}
        </dl>
      )}
      {edge?.details && (
        <dl className={styles.metadata}>
          {Object.entries(edge.details).map(([label, value]) => (
            <div key={label} style={{ display: "contents" }}>
              <dt>{label}</dt>
              <dd>{value}</dd>
            </div>
          ))}
        </dl>
      )}
      {selected?.change && (
        <div className={styles.inspectorSection}>
          <Badge
            color={
              selected.change === "removed"
                ? "red"
                : selected.change === "added"
                  ? "teal"
                  : "yellow"
            }
          >
            {selected.badge}
          </Badge>
          <dl className={styles.metadata}>
            <dt>До</dt>
            <dd>
              {selected.before ? (
                <>
                  <Text size="sm" fw={600}>
                    {selected.before.name}
                  </Text>
                  <Text size="xs">{selected.before.description}</Text>
                  <Text size="xs">{nodeSubtitle(selected.before)}</Text>
                </>
              ) : (
                <Text size="sm">Объекта не было</Text>
              )}
            </dd>
            <dt>После</dt>
            <dd>
              {selected.after ? (
                <>
                  <Text size="sm" fw={600}>
                    {selected.after.name}
                  </Text>
                  <Text size="xs">{selected.after.description}</Text>
                  <Text size="xs">{nodeSubtitle(selected.after)}</Text>
                </>
              ) : (
                <Text size="sm">Удалён из предложения</Text>
              )}
            </dd>
          </dl>
          {selected.change === "removed" && evidenceTarget.revisionId && (
            <Button
              variant="subtle"
              size="compact-xs"
              onClick={() =>
                onNavigate({
                  revisionId: evidenceTarget.revisionId!,
                  wbMode: "neighborhood",
                  wbScope: id,
                  recordId: id,
                })
              }
            >
              Открыть исходный объект
            </Button>
          )}
        </div>
      )}
      <Button
        variant="subtle"
        size="compact-sm"
        leftSection={<IconCopy size={14} />}
        mt="xl"
        onClick={() =>
          void navigator.clipboard.writeText(window.location.href).then(() => setCopied(true))
        }
      >
        {copied ? "Ссылка скопирована" : "Скопировать ссылку"}
      </Button>
    </aside>
  );
}
