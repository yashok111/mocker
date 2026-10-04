import type { ProjectionReadProps } from "./backendEffectiveProjectionReads";
import { Badge, Code, Stack, Text } from "@mantine/core";
import type {
  ArtifactProjectionData,
  ArtifactComparisonSide,
  EditorArtifactSide,
  ArtifactGroupSide,
} from "@/api/generated/schemas";
import { databaseWrap } from "./backendDatabaseReads";
import { BackendArtifactReference } from "./BackendAPIArtifacts";
/** Field lists retain all authored auxiliary data, including nested branches and schema fields. */
function Fields({ value }: { value: unknown }) {
  if (value === null || typeof value !== "object")
    return (
      <Text size="sm" style={databaseWrap}>
        {value === null ? "null" : String(value)}
      </Text>
    );
  if (Array.isArray(value))
    return value.length ? (
      <ol style={{ paddingInlineStart: 20, margin: 0 }}>
        {value.map((entry, i) => (
          <li key={i}>
            <Fields value={entry} />
          </li>
        ))}
      </ol>
    ) : (
      <Text size="sm">Пустой список</Text>
    );
  return (
    <dl style={{ margin: 0, minWidth: 0 }}>
      {Object.entries(value).map(([key, entry]) => (
        <div key={key} style={{ minWidth: 0, paddingBlock: 3 }}>
          <dt style={{ fontWeight: 600, overflowWrap: "anywhere" }}>{key}</dt>
          <dd style={{ marginInlineStart: 12, minWidth: 0 }}>
            <Fields value={entry} />
          </dd>
        </div>
      ))}
    </dl>
  );
}
export function ArtifactTypedContent({ data }: { data: ArtifactProjectionData }) {
  switch (data.kind) {
    case "participant":
      return <Fields value={data.participant} />;
    case "sequence_message":
      return <Fields value={data.sequenceMessage} />;
    case "fragment":
      return <Fields value={data.fragment} />;
    case "state_diagram":
      return <Fields value={data.stateDiagram} />;
    case "state":
      return <Fields value={data.state} />;
    case "state_transition":
      return <Fields value={data.stateTransition} />;
    case "response_rule":
      return <Fields value={data.responseRule} />;
    case "response_node":
      return <Fields value={data.responseNode} />;
    case "response_edge":
      return <Fields value={data.responseEdge} />;
    case "event_node":
      return <Fields value={data.eventNode} />;
    case "event_edge":
      return <Fields value={data.eventEdge} />;
    case "event_server":
      return <Fields value={data.eventServer} />;
    case "event_channel":
      return <Fields value={data.eventChannel} />;
    case "event_message":
      return <Fields value={data.eventMessage} />;
    case "event_schema":
      return <Fields value={data.eventSchema} />;
    case "event_contract":
      return <Fields value={data.eventContract} />;
    case "event_operation":
      return <Fields value={data.eventOperation} />;
    case "api_operation":
      return <Fields value={data.apiOperation} />;
    case "unsupported":
      return (
        <Stack>
          <Badge color="yellow">Неподдерживаемое содержимое</Badge>
          <Fields value={data.unsupported} />
        </Stack>
      );
    default: {
      const exhaustive: never = data;
      return exhaustive;
    }
  }
}
export function ArtifactEditorSide({ side }: { side: EditorArtifactSide | ArtifactGroupSide }) {
  return (
    <Stack gap="xs">
      <Text style={databaseWrap}>
        {side.pin.kind} {side.pin.id} · ревизия {side.pin.revisionId}
      </Text>
      {"contentHash" in side.pin && <Code style={databaseWrap}>{side.pin.contentHash}</Code>}
      {"binding" in side && (
        <>
          <Text fw={600} style={databaseWrap}>
            {side.binding.lastKnownLabel}
          </Text>
          <Fields value={side.binding.selector} />
          <Text style={databaseWrap}>Исходные узлы: {side.binding.sourceNodeIds.join(", ")}</Text>
          <Text style={databaseWrap}>
            Сохранённые подписи: {side.binding.sourceLabels.join(", ")}
          </Text>
          <Text style={databaseWrap}>Хеш объекта: {side.binding.objectHash}</Text>
          <Text style={databaseWrap}>Причина: {side.binding.reason}</Text>
        </>
      )}
    </Stack>
  );
}
export function ArtifactDeltaSide({
  side,
  projectId,
  revisionId,
  target,
  pins,
}: ProjectionReadProps & {
  side?: ArtifactComparisonSide;
  projectId: string;
  revisionId?: string;
}) {
  if (!side) return <Text>Отсутствует</Text>;
  if ("api" in side)
    return (
      <BackendArtifactReference
        reference={side.api}
        projectId={projectId}
        revisionId={revisionId}
        target={target}
        pins={pins}
      />
    );
  return <ArtifactEditorSide side={"editor" in side ? side.editor : side.group} />;
}
