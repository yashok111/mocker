import { useState } from "react";
import {
  Badge,
  Button,
  Code,
  Group,
  NativeSelect,
  Paper,
  Stack,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import type {
  BackendEffectiveFieldOrigin,
  BackendEffectiveGraphPins,
  BackendGraphResponse,
  BackendQualifiedSourceIdentity,
  BackendReadTarget,
  BackendSourceReadContext,
  BackendLineageValueRef,
} from "@/api/generated/schemas";
import {
  backendReadQueryKey,
  readBackendCoverage,
  readBackendGraph,
  readBackendNode,
  type BackendGraphFilters,
} from "./backendGraphReads";
import { backendReadTargetKey, checkBackendReadPins } from "./backendReadTargets";
import { BackendAssertions } from "./BackendAssertions";
import { BackendExactEvidence } from "./BackendExactEvidence";
import { CoverageDetails, LoadState, Pages } from "./BackendReadUI";
import { useBackendAPIDeparture } from "./useBackendAPIDeparture";
import { usePinnedValue } from "./backendFlowReads";
import { BackendRepresentationFields, BackendRepresentationValue } from "./BackendRepresentations";
import { BackendLineage } from "./BackendLineage";
import { BackendDesiredIdentities } from "./BackendDesiredIdentities";
import { BackendValueSeeds } from "./BackendLineageActions";
import { BackendValueInspector } from "./BackendValueInspector";
import { BackendAPIFields } from "./BackendAPIFields";
const wrap = { overflowWrap: "anywhere" as const, whiteSpace: "pre-wrap" as const };
function edgeDisplayName(page: BackendGraphResponse | undefined, id: string) {
  return page && "edgeNames" in page
    ? page.edgeNames?.find((item) => item.id === id)?.name
    : undefined;
}
const nodeKinds = [
  "system",
  "service",
  "module",
  "external_system",
  "datastore",
  "symbol",
  "http_operation",
  "handler",
  "unresolved_target",
  "db_schema",
  "table",
  "column",
  "constraint",
  "index",
  "view",
  "migration",
  "flow",
  "flow_step",
  "query",
  "transaction",
  "api_field",
  "field_mapping",
  "channel",
  "message",
  "consumer",
  "job",
  "event_field",
  "domain_entity",
  "dto",
  "api_schema",
  "representation_field",
];
type Selection = { recordType: "node" | "edge"; id: string };
type Props = {
  projectId: string;
  target: BackendReadTarget;
  pins?: BackendEffectiveGraphPins;
  focusTarget?: Selection;
  onSelectionChange?: (selection: Selection | undefined) => void;
};
type Context = {
  projectId: string;
  target: BackendReadTarget;
  pins?: BackendEffectiveGraphPins;
  claimsSupported: boolean;
};
export function BackendExactGraph(props: Props) {
  return <ExactGraph key={`${props.projectId}:${backendReadTargetKey(props.target)}`} {...props} />;
}
function ExactGraph({ projectId, target, ...props }: Props) {
  const query = useQuery({
    queryKey: backendReadQueryKey("coverage", projectId, target, props.pins?.targetHash),
    queryFn: ({ signal }) => readBackendCoverage(projectId, target, signal, props.pins),
    select: (coverage) => {
      checkBackendReadPins(coverage, target, props.pins);
      return coverage;
    },
    retry: false,
    staleTime: Infinity,
  });
  const coverage = query.data;
  const pins = coverage && "pins" in coverage ? coverage.pins : undefined;
  const source = coverage && "source" in coverage ? coverage.source : undefined;
  return (
    <Stack aria-label="Модель выбранного графа" gap="lg">
      <LoadState query={query} label="покрытия" />
      {coverage && (
        <>
          <CoverageDetails data={coverage} />
          {pins && (
            <details>
              <summary>Базовая ревизия и точный контекст</summary>
              <Code block style={wrap}>
                {JSON.stringify(
                  { baseRevisionId: pins.baseRevisionId, sourceVector: source?.sourceVector, pins },
                  null,
                  2,
                )}
              </Code>
            </details>
          )}
          <ExactInventory
            {...props}
            projectId={projectId}
            target={target}
            pins={pins}
            claimsSupported={Boolean(source?.sourceVector) && !target.proposal}
          />
        </>
      )}
    </Stack>
  );
}
function ExactInventory({
  projectId,
  target,
  pins,
  claimsSupported,
  focusTarget,
  onSelectionChange,
}: Context & Omit<Props, "projectId" | "target">) {
  const depart = useBackendAPIDeparture();
  const [filter, setFilter] = useState({
    search: "",
    kind: "",
    recordType: "nodes" as "nodes" | "edges",
  });
  const [draft, setDraft] = useState(filter);
  const [cursors, setCursors] = useState([""]);
  const [selection, setSelection] = usePinnedValue<Selection | undefined>(
    JSON.stringify(focusTarget ?? null),
    focusTarget,
  );
  const select = (value: Selection | undefined) =>
    depart(() => {
      setSelection(value);
      onSelectionChange?.(value);
    });
  const paging = { kind: filter.kind, limit: 100, cursor: cursors.at(-1) ?? "" };
  const input: BackendGraphFilters =
    filter.recordType === "nodes"
      ? { ...paging, recordType: "nodes", search: filter.search }
      : { ...paging, recordType: "edges" };
  const query = useQuery({
    queryKey: backendReadQueryKey("graph", projectId, target, input),
    queryFn: ({ signal }) => readBackendGraph(projectId, target, input, signal, pins),
    retry: false,
    staleTime: Infinity,
  });
  const page = query.data;
  return (
    <Stack>
      <form
        onSubmit={(event) => {
          event.preventDefault();
          depart(() => {
            setFilter({ ...draft, search: draft.search.trim() });
            setCursors([""]);
            setSelection(undefined);
            onSelectionChange?.(undefined);
          });
        }}
      >
        <Group align="flex-end">
          {draft.recordType === "nodes" && (
            <TextInput
              label="Название объекта"
              value={draft.search}
              onChange={(event) => setDraft({ ...draft, search: event.currentTarget.value })}
              style={{ flex: "1 1 200px" }}
            />
          )}
          <NativeSelect
            label="Записи графа"
            value={draft.recordType}
            onChange={(event) =>
              setDraft({
                ...draft,
                recordType: event.currentTarget.value as "nodes" | "edges",
                kind: "",
                search: "",
              })
            }
            data={[
              { value: "nodes", label: "Объекты" },
              { value: "edges", label: "Связи" },
            ]}
          />
          {draft.recordType === "nodes" && (
            <NativeSelect
              label="Тип объекта"
              value={draft.kind}
              onChange={(event) => setDraft({ ...draft, kind: event.currentTarget.value })}
              data={[
                { value: "", label: "Все типы" },
                ...nodeKinds.map((value) => ({ value, label: value })),
              ]}
            />
          )}
          <Button type="submit">Найти объекты</Button>
        </Group>
      </form>
      <LoadState query={query} label="объектов" />
      {page && (
        <>
          {page.nodes.length === 0 && page.edges.length === 0 && (
            <Text c="dimmed">Объекты не найдены</Text>
          )}
          {page.nodes.map((node) => (
            <Button
              key={node.id}
              variant={selection?.id === node.id ? "light" : "subtle"}
              aria-pressed={selection?.id === node.id}
              aria-label={`Открыть объект ${node.name}`}
              styles={{ label: wrap }}
              h="auto"
              py="xs"
              justify="flex-start"
              onClick={() => select({ recordType: "node", id: node.id })}
            >
              {node.kind} · {node.name}
            </Button>
          ))}
          {page.edges.map((edge) => (
            <Button
              key={edge.id}
              variant="subtle"
              styles={{ label: wrap }}
              h="auto"
              justify="flex-start"
              onClick={() => select({ recordType: "edge", id: edge.id })}
            >
              Открыть связь {edgeDisplayName(page, edge.id) ?? edge.kind} {edge.id}
            </Button>
          ))}
          <Pages
            label="объекты"
            cursors={cursors}
            next={page.nextCursor}
            busy={query.isFetching}
            setCursors={setCursors}
          />
        </>
      )}
      {selection && (
        <Paper
          component="section"
          aria-label="Инспектор объекта"
          withBorder
          p="md"
          style={{ minWidth: 0 }}
        >
          <Stack>
            <Group justify="space-between">
              <Title order={3}>{selection.recordType === "node" ? "Объект" : "Связь"}</Title>
              <Button variant="subtle" onClick={() => select(undefined)}>
                Закрыть инспектор
              </Button>
            </Group>
            <BackendExactRecordInspector
              projectId={projectId}
              target={target}
              pins={pins}
              claimsSupported={claimsSupported}
              {...selection}
            />
          </Stack>
        </Paper>
      )}
    </Stack>
  );
}
export function BackendExactRecordInspector(props: Context & Selection) {
  return (
    <ExactRecord
      key={JSON.stringify([
        props.projectId,
        backendReadTargetKey(props.target),
        props.recordType,
        props.id,
      ])}
      {...props}
    />
  );
}
function ExactRecord({ recordType, id, ...context }: Context & Selection) {
  const { projectId, target, pins, claimsSupported } = context;
  const [evidenceId, setEvidenceId] = useState<string>();
  const [identity, setIdentity] = useState<BackendQualifiedSourceIdentity>();
  const [related, setRelated] = useState<Selection>();
  const [lineage, setLineage] = useState<BackendLineageValueRef>();
  const [selectedValue, setSelectedValue] = useState<BackendLineageValueRef>();
  const query = useQuery({
    queryKey: backendReadQueryKey(recordType, projectId, target, id),
    queryFn: async ({ signal }) => {
      if (recordType === "node") {
        const read = await readBackendNode(projectId, target, id, signal, pins);
        const node =
          "node" in read
            ? read.node
            : "proposalProjection" in read
              ? read.proposalProjection
              : read;
        return {
          record: node,
          origins: "origins" in read ? read.origins : undefined,
          edgeName: undefined,
        };
      }
      const page = await readBackendGraph(
        projectId,
        target,
        { recordType: "edges", id, limit: 1 },
        signal,
        pins,
      );
      if (page.edges.length !== 1 || page.edges[0]?.id !== id)
        throw new Error("Связь отсутствует в выбранном графе.");
      return {
        record: page.edges[0],
        origins: "origins" in page ? page.origins : undefined,
        edgeName: edgeDisplayName(page, id),
      };
    },
    retry: false,
    staleTime: Infinity,
  });
  const record = query.data?.record;
  const source = record && "source" in record ? record.source : undefined;
  return (
    <Stack>
      <LoadState query={query} label="объекта" />
      {record && (
        <>
          <Title order={4} style={wrap}>
            {"name" in record ? record.name : (query.data?.edgeName ?? record.kind)}
          </Title>
          <Badge>{record.kind}</Badge>
          <Text size="xs" style={wrap}>
            {record.id}
          </Text>
          <SourceIdentities
            source={source}
            onSelect={(next) => {
              setIdentity(next);
              setEvidenceId(undefined);
            }}
          />
          {target.changeProposal && (
            <BackendDesiredIdentities
              projectId={projectId}
              target={target}
              pins={pins}
              recordType={recordType}
              id={id}
            />
          )}
          {"externalKey" in record && (
            <Text size="sm" style={wrap}>
              Ключ источника: {record.externalKey}
            </Text>
          )}
          <Code block style={wrap}>
            {JSON.stringify(
              "attributes" in record ? record.attributes : record.effectiveFacet,
              null,
              2,
            )}
          </Code>
          {"from" in record && (
            <Text style={wrap}>
              {record.from} → {record.to}
            </Text>
          )}
          <IntentOrigins
            origins={query.data?.origins?.filter((origin) => origin.subjectId === id)}
          />
          {"attributes" in record && "parentId" in record && (
            <>
              <BackendRepresentationValue
                node={record}
                onValueSelect={target.importCandidate || target.proposal ? undefined : setLineage}
              />
              {["domain_entity", "dto", "api_schema"].includes(record.kind) && (
                <BackendRepresentationFields
                  projectId={projectId}
                  target={target}
                  pins={pins}
                  ownerId={id}
                  onInspect={(fieldId) => setRelated({ recordType: "node", id: fieldId })}
                />
              )}
              {!target.importCandidate &&
                !target.proposal &&
                record.kind !== "representation_field" && (
                  <>
                    <BackendValueSeeds
                      projectId={projectId}
                      target={target}
                      pins={pins}
                      node={record}
                      onValueSelect={setSelectedValue}
                    />
                    {record.kind === "http_operation" && (
                      <BackendAPIFields
                        projectId={projectId}
                        target={target}
                        pins={pins}
                        operationId={record.id}
                        onValueSelect={setSelectedValue}
                      />
                    )}
                  </>
                )}
            </>
          )}
          {selectedValue && (
            <BackendValueInspector
              projectId={projectId}
              target={target}
              pins={pins}
              value={selectedValue}
              onClose={() => setSelectedValue(undefined)}
            />
          )}
          {lineage && (
            <BackendLineage
              projectId={projectId}
              target={target}
              pins={pins}
              seed={lineage}
              initialDirection="reverse"
              onClose={() => setLineage(undefined)}
              onValueSelect={(value) => setRelated({ recordType: "node", id: value.nodeId })}
            />
          )}
          <BackendAssertions
            projectId={projectId}
            target={target}
            pins={pins}
            supported={claimsSupported}
            recordType={recordType}
            id={id}
            repositoryId={identity?.repositoryId}
            providerNamespace={identity?.providerNamespace}
            assertionHash={identity?.assertionHash}
            onEvidence={setEvidenceId}
          />
          {identity && (
            <Button variant="subtle" onClick={() => setIdentity(undefined)}>
              Все поставщики объекта
            </Button>
          )}
          <BackendExactEvidence
            key={evidenceId ?? "all"}
            projectId={projectId}
            target={target}
            pins={pins}
            subjectId={id}
            evidenceId={evidenceId}
          />
          {evidenceId && (
            <Button variant="subtle" onClick={() => setEvidenceId(undefined)}>
              Все основания объекта
            </Button>
          )}
          {recordType === "node" && (
            <>
              <RelatedEdges {...context} nodeId={id} direction="out" onSelect={setRelated} />
              <RelatedEdges {...context} nodeId={id} direction="in" onSelect={setRelated} />
            </>
          )}
        </>
      )}
      {related && (
        <Paper component="section" aria-label="Инспектор связи" withBorder p="sm">
          <Button variant="subtle" onClick={() => setRelated(undefined)}>
            Закрыть связь
          </Button>
          <BackendExactRecordInspector {...context} {...related} />
        </Paper>
      )}
    </Stack>
  );
}
function SourceIdentities({
  source,
  onSelect,
}: {
  source?: BackendSourceReadContext;
  onSelect: (identity: BackendQualifiedSourceIdentity) => void;
}) {
  if (!source) return null;
  return (
    <Stack gap="xs" aria-label="Идентичности источника">
      <Title order={5}>Идентичности источника</Title>
      {source.identities.length === 0 && (
        <Text size="sm" c="dimmed">
          У объекта нет идентичности в исходниках.
        </Text>
      )}
      {source.identities.map((identity) => (
        <div key={JSON.stringify(identity)}>
          <Text size="sm" style={wrap}>
            {identity.repositoryId} · {identity.providerNamespace} · {identity.externalKey}
          </Text>
          <Text size="xs" style={wrap}>
            {identity.assertionHash}
          </Text>
          <Button
            variant="subtle"
            aria-label={`Утверждение ${identity.providerNamespace} ${identity.externalKey}`}
            onClick={() => onSelect(identity)}
          >
            Показать утверждение
          </Button>
        </div>
      ))}
    </Stack>
  );
}
export function IntentOrigins({ origins }: { origins?: BackendEffectiveFieldOrigin[] }) {
  const intents = origins?.filter((origin) => origin.kind === "intent");
  if (!intents?.length) return null;
  return (
    <Stack gap="xs" aria-label="Намерения предложения">
      <Title order={5}>Намерения предложения</Title>
      {intents.map((origin) => (
        <div key={JSON.stringify(origin.selector)}>
          <Text size="sm" style={wrap}>
            {JSON.stringify(origin.selector)} · {origin.reason}
          </Text>
          <Text size="xs" style={wrap}>
            Команда: {origin.commandId}
          </Text>
        </div>
      ))}
    </Stack>
  );
}
function RelatedEdges({
  nodeId,
  direction,
  onSelect,
  ...context
}: Context & {
  nodeId: string;
  direction: "in" | "out";
  onSelect: (selection: Selection) => void;
}) {
  const [cursors, setCursors] = useState([""]);
  const { projectId, target, pins } = context;
  const input = {
    recordType: "edges" as const,
    ...(direction === "out" ? { from: nodeId } : { to: nodeId }),
    limit: 100,
    cursor: cursors.at(-1) ?? "",
  };
  const query = useQuery({
    queryKey: backendReadQueryKey("graph", projectId, target, input),
    queryFn: ({ signal }) => readBackendGraph(projectId, target, input, signal, pins),
    retry: false,
    staleTime: Infinity,
  });
  const page: BackendGraphResponse | undefined = query.data;
  const label = direction === "out" ? "исходящие связи" : "входящие связи";
  return (
    <Stack gap="xs">
      <Title order={5}>{direction === "out" ? "Исходящие связи" : "Входящие связи"}</Title>
      <LoadState query={query} label={label} />
      {page?.edges.map((edge) => (
        <Button
          key={edge.id}
          variant="subtle"
          styles={{ label: wrap }}
          h="auto"
          justify="flex-start"
          onClick={() => onSelect({ recordType: "edge", id: edge.id })}
        >
          Основания связи {edgeDisplayName(page, edge.id) ?? edge.kind} {edge.id}
        </Button>
      ))}
      <Pages
        label={label}
        cursors={cursors}
        next={page?.nextCursor}
        busy={query.isFetching}
        setCursors={setCursors}
      />
    </Stack>
  );
}
