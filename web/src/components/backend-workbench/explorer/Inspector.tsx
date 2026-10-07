import { useEffect, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { ActionIcon, Alert, Badge, Button, Group, Loader, Stack, Tabs, Text } from "@mantine/core";
import { IconArrowRight, IconCopy, IconX } from "@tabler/icons-react";
import type { BackendReadTarget, BackendDiagramRef } from "@/api/generated/schemas";
import { listBackendDiagrams } from "@/api/generated/backend-projects/backend-projects";
import { diagramSearch } from "../backendWorkspaceSearch";
import { ok } from "./catalogs";
import { diagramKindNames } from "./model";
import { readBackendNode, readBackendEvidence, readBackendGraph } from "../backendGraphReads";
import { backendReadTargetKey } from "../backendReadTargets";
import type { BackendWorkspaceSearch } from "../backendWorkspaceSearch";
import { readExplore, readExploreNodes, nodeOf } from "./reads";
import { resolvedTargetSearch } from "./navigation";
import {
  kindName,
  nodeSubtitle,
  relationNames,
  sourceNode,
  type MapNode,
  type MapEdge,
} from "./model";
import { Contracts } from "./Contracts";
import { diagramArtifactLink } from "./artifactLinks";
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
  extraRefs,
  moreRefs,
  canEnter,
  enterLabel,
  referencesStatus,
}: {
  targetHash?: string;
  extraRefs?: BackendDiagramRef[];
  moreRefs?: () => void;
  canEnter?: boolean;
  enterLabel?: string;
  referencesStatus?: ReactNode;
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
  const [tab, setTab] = useState<string | null>("relations");
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
  const references = [
    ...new Map(
      [...(selected?.refs ?? edge?.refs ?? []), ...(extraRefs ?? [])].map((ref) => [
        JSON.stringify(ref),
        ref,
      ]),
    ).values(),
  ];
  const relations = useQuery({
    queryKey: ["workbench-relations", projectId, backendReadTargetKey(evidenceTarget), id],
    enabled: source && search.recordType !== "edge" && tab === "relations",
    retry: false,
    staleTime: Infinity,
    queryFn: ({ signal }) =>
      readExplore(
        projectId,
        { target: evidenceTarget, mode: "neighborhood", scopeId: id, limit: 40 },
        signal,
      ),
  });
  const ref = references.find((ref) => ref.kind === "record" && ref.recordType === "node");
  const relatedID = source ? id : ref?.kind === "record" ? ref.id : undefined;
  const relatedHash = targetHash ?? relations.data?.targetHash;
  const maps = useQuery({
    queryKey: ["workbench-related-maps", projectId, relatedHash, relatedID],
    enabled: !!relatedHash && !!relatedID && tab === "relations",
    retry: false,
    queryFn: async ({ signal }) =>
      ok(
        await listBackendDiagrams(
          projectId,
          { subjectId: relatedID, targetHash: relatedHash, limit: 20, order: "desc" },
          { signal },
        ),
      ),
  });
  const sourceRefs = references.flatMap((r) =>
    r.kind === "record" && r.recordType === "node" ? [r.id] : [],
  );
  const refNames = useQuery({
    queryKey: ["workbench-ref-names", projectId, backendReadTargetKey(evidenceTarget), sourceRefs],
    enabled: !source && sourceRefs.length > 0 && tab === "relations",
    retry: false,
    staleTime: Infinity,
    queryFn: async ({ signal }) => ({
      nodes: await readExploreNodes(projectId, evidenceTarget, sourceRefs, signal),
    }),
  });
  const title = selected?.name ?? edge?.label ?? "Выбранный объект";
  const nativeValue =
    raw && "attributes" in raw
      ? (raw.attributes as Record<string, unknown>).nativeText
      : selected?.attributes.nativeText;
  const nativeText =
    typeof nativeValue === "string" && nativeValue.trim() ? nativeValue : undefined;
  const showSource = !!nativeText || selected?.kind === "flow_step";
  const evidence = useQuery({
    queryKey: ["workbench-evidence", projectId, backendReadTargetKey(evidenceTarget), id],
    enabled: source && (tab === "sources" || showSource),
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
  const jump = (n: MapNode) =>
    onNavigate({
      ...pin,
      wbView: ["table", "column", "datastore"].includes(n.kind) ? "data" : "structure",
      wbMode: "neighborhood",
      wbScope: n.id,
      recordId: n.id,
    });
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
          {nativeText && (
            <details
              key={`${id}:${backendReadTargetKey(evidenceTarget)}`}
              className={styles.sourceCodeDetails}
            >
              <summary>Показать код</summary>
              {/* oxlint-disable jsx-a11y/no-noninteractive-tabindex -- The bounded code pane must support keyboard scrolling. */}
              <pre
                className={styles.sourceCodeBlock}
                tabIndex={0}
                aria-label="Фрагмент исходного кода"
              >
                <code>{nativeText}</code>
              </pre>
              {/* oxlint-enable jsx-a11y/no-noninteractive-tabindex */}
            </details>
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
      <Tabs value={tab} onChange={setTab} mt="xl">
        <Tabs.List grow>
          <Tabs.Tab value="relations">Связи</Tabs.Tab>
          <Tabs.Tab value="sources">Источники</Tabs.Tab>
          {source && search.recordType !== "edge" && (
            <Tabs.Tab value="contracts">Контракты</Tabs.Tab>
          )}
        </Tabs.List>
      </Tabs>
      <div className={styles.inspectorSection}>
        {tab === "contracts" ? (
          <Contracts projectId={projectId} target={evidenceTarget} nodeId={id} />
        ) : tab === "relations" ? (
          <Stack gap="xs">
            {referencesStatus}
            {maps.data?.items.map((map) => (
              <Button
                key={map.id}
                variant="light"
                size="compact-sm"
                onClick={() =>
                  onNavigate({
                    ...diagramSearch(map.pin),
                    wbView:
                      map.kind === "architecture"
                        ? "structure"
                        : map.kind === "lifecycle"
                          ? "data"
                          : "scenarios",
                  })
                }
              >
                {map.name ?? diagramKindNames[map.kind]} · {diagramKindNames[map.kind]}
              </Button>
            ))}
            {synthetic && (
              <Text size="sm">
                Коллекция для навигации по импортированным объектам. Не обозначает отдельный сервис.
              </Text>
            )}
            {relations.isPending && source && <Loader size="sm" />}
            {relations.isError && (
              <Text size="sm" c="red">
                Связи недоступны.{" "}
                <Button size="compact-xs" variant="subtle" onClick={() => void relations.refetch()}>
                  Повторить
                </Button>
              </Text>
            )}
            {relations.data?.edges
              .filter((e) => e.from === id || e.to === id)
              .map((e) => {
                const other = relations.data!.nodes.find(
                  (n) => n.id === (e.from === id ? e.to : e.from),
                );
                return (
                  <div key={e.id}>
                    <Text size="xs" c="dimmed">
                      {relationNames[e.kind] ?? e.kind}
                      {e.to === id ? " · входящая связь" : ""}
                    </Text>
                    {other ? (
                      <Button
                        variant="subtle"
                        size="compact-sm"
                        onClick={() => jump(sourceNode(other))}
                      >
                        {other.name}
                      </Button>
                    ) : (
                      <Text size="xs">Связанный объект вне этой страницы</Text>
                    )}
                  </div>
                );
              })}
            {relations.data?.edges.length === 0 && !synthetic && (
              <Text size="sm" c="dimmed">
                В этой области нет извлечённых связей. Это не доказывает отсутствие обращений к
                данным или другим сервисам.
              </Text>
            )}
            {references.map((ref, i) =>
              ref.kind === "record" ? (
                <Button
                  key={i}
                  variant="subtle"
                  onClick={() =>
                    onNavigate({
                      ...pin,
                      wbMode: ref.recordType === "node" ? "neighborhood" : "overview",
                      wbScope: ref.recordType === "node" ? ref.id : undefined,
                      recordId: ref.id,
                      recordType: ref.recordType === "node" ? "node" : "edge",
                    })
                  }
                >
                  {refNames.data?.nodes.find((n) => n.id === ref.id)?.name ??
                    `Связанный ${ref.recordType === "node" ? "объект" : "переход"} ${i + 1}`}
                </Button>
              ) : diagramArtifactLink(projectId, evidenceTarget, ref) ? (
                <Button
                  key={i}
                  component="a"
                  href={diagramArtifactLink(projectId, evidenceTarget, ref)}
                  variant="subtle"
                  size="compact-sm"
                >
                  Открыть точную модель
                </Button>
              ) : (
                <Text key={i} size="xs">
                  Точный артефакт этой установки недоступен; его исходный pin сохранён в
                  подробностях.
                </Text>
              ),
            )}
            {moreRefs && (
              <Button variant="subtle" size="compact-xs" onClick={moreRefs}>
                Ещё связанные объекты
              </Button>
            )}
            {relations.data?.nextCursor && (
              <Button
                variant="subtle"
                onClick={() =>
                  onNavigate({ ...pin, wbMode: "neighborhood", wbScope: id, wbList: true })
                }
              >
                Все связи
              </Button>
            )}
          </Stack>
        ) : (
          <Stack gap="sm">
            <Text size="sm">
              {selected?.origin ?? edge?.origin ?? "Импортированная модель"}. Наличие схемы не
              подтверждает выполнение в среде.
            </Text>
            {evidence.isFetching && <Loader size="sm" />}
            {evidence.isError && (
              <Text c="red" size="sm">
                Не удалось загрузить доказательства.
              </Text>
            )}
            {evidence.data?.items.map((e) => (
              <div key={e.id}>
                <Text size="xs" fw={600}>
                  {e.source.file}
                </Text>
                <Text size="xs" c="dimmed">
                  {e.method} · {e.status}
                </Text>
                <Text size="sm">{e.explanation}</Text>
                {e.snippet && (
                  <details className={styles.details}>
                    <summary>Фрагмент исходника</summary>
                    <pre>{e.snippet}</pre>
                  </details>
                )}
              </div>
            ))}
            <details className={styles.details}>
              <summary>Технические сведения</summary>
              <dl className={styles.metadata}>
                <dt>Идентификатор</dt>
                <dd>{id}</dd>
                <dt>Версия</dt>
                <dd>{target.revisionId ?? target.changeProposal?.proposalRevisionId}</dd>
              </dl>
              <pre>{JSON.stringify(raw ?? selected ?? edge, null, 2)}</pre>
            </details>
          </Stack>
        )}
      </div>
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
