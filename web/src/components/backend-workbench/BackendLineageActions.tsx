import { LoadState } from "./BackendGraphInventory";
import { useEffect, useRef, useState } from "react";
import { Button, Group, Stack, Text } from "@mantine/core";
import { useGetBackendRevision } from "@/api/generated/backend-projects/backend-projects";
import type {
  BackendLineageValueRef,
  BackendNode,
  BackendComposedNode,
  BackendReadTarget,
  BackendEffectiveGraphPins,
} from "@/api/generated/schemas";
import { lineageRefKey } from "./backendLineageReads";
import { BackendLineage } from "./BackendLineage";
import { databaseButtonStyles, databaseWrap } from "./backendDatabaseReads";
export function BackendLineageActions({
  projectId,
  revisionId,
  target: selectedTarget,
  pins,
  seed,
  onValueSelect,
  proposal = false,
}: {
  projectId: string;
  revisionId?: string;
  target?: BackendReadTarget;
  pins?: BackendEffectiveGraphPins;
  seed: BackendLineageValueRef;
  onValueSelect: (ref: BackendLineageValueRef) => void;
  proposal?: boolean;
}) {
  const target = selectedTarget ?? { revisionId: revisionId ?? "" };
  const sourceTarget = target.revisionId;
  const revision = useGetBackendRevision(projectId, sourceTarget ?? "", {
    query: { enabled: Boolean(sourceTarget) && !proposal, retry: false },
  });
  const [direction, setDirection] = useState<"forward" | "reverse" | null>(null);
  const [launchId, setLaunchId] = useState(0);
  const trigger = useRef<HTMLButtonElement | null>(null);
  if (
    proposal ||
    target.proposal ||
    target.importCandidate ||
    (revision.data?.status === 200 && !["4", "5", "6"].includes(revision.data.data.schemaVersion))
  )
    return (
      <Text size="sm">
        Происхождение значения доступно только в снимке source4–6 или полном предложении. Для этого
        графа оно недоступно.
      </Text>
    );
  return (
    <Stack gap="xs">
      {revision.isError && <LoadState query={revision} label="ревизии происхождения" />}
      <Group>
        {(["reverse", "forward"] as const).map((value) => (
          <Button
            key={value}
            variant="default"
            disabled={Boolean(sourceTarget) && revision.data?.status !== 200}
            onClick={(event) => {
              trigger.current = event.currentTarget;
              setDirection(value);
              setLaunchId((previous) => previous + 1);
            }}
          >
            {value === "reverse" ? "Происхождение значения" : "Куда передаётся значение"}
          </Button>
        ))}
      </Group>
      {direction && (
        <BackendLineage
          key={launchId}
          projectId={projectId}
          target={target}
          pins={pins}
          seed={seed}
          initialDirection={direction}
          onValueSelect={onValueSelect}
          onClose={() => {
            setDirection(null);
            requestAnimationFrame(() => trigger.current?.focus());
          }}
        />
      )}
    </Stack>
  );
}
export function BackendValueSeeds({
  projectId,
  revisionId,
  target,
  pins,
  node,
  onValueSelect,
  selected,
}: {
  projectId: string;
  revisionId?: string;
  target?: BackendReadTarget;
  pins?: BackendEffectiveGraphPins;
  node: BackendNode | BackendComposedNode;
  onValueSelect: (ref: BackendLineageValueRef) => void;
  selected?: BackendLineageValueRef;
}) {
  const exact = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (selected?.nodeId === node.id) exact.current?.focus();
  }, [selected, node.id]);
  const seeds: { ref: BackendLineageValueRef; label: string }[] = [];
  if (node.kind === "representation_field")
    seeds.push({ ref: { kind: "representation_field", nodeId: node.id }, label: node.name });
  if (node.kind === "api_field")
    seeds.push({ ref: { kind: "api_field", nodeId: node.id }, label: node.name });
  if (node.kind === "column")
    for (const facetKey of Object.keys(node.attributes.facets))
      seeds.push({
        ref: { kind: "column", nodeId: node.id, facetKey },
        label: `${node.name} · ${facetKey}`,
      });
  for (const collection of ["inputs", "outputs", "parameters", "results"] as const) {
    const attrs = node.attributes;
    if (collection in attrs) {
      const ports = attrs[collection as keyof typeof attrs];
      if (Array.isArray(ports))
        for (const port of ports)
          if ("key" in port && "name" in port)
            seeds.push({
              ref: { kind: "port", nodeId: node.id, collection, portKey: port.key },
              label: `${port.name} · ${collection} · ${port.key}`,
            });
    }
  }
  return (
    <Stack gap="sm">
      {seeds.map(({ ref, label }, i) => (
        <Stack
          key={i}
          gap="xs"
          data-lineage-selected={
            selected && lineageRefKey(selected) === lineageRefKey(ref) ? "true" : undefined
          }
        >
          <Text fw={600} style={databaseWrap}>
            {label}
          </Text>
          <Button
            ref={selected && lineageRefKey(selected) === lineageRefKey(ref) ? exact : undefined}
            variant="subtle"
            h="auto"
            styles={databaseButtonStyles}
            onClick={() => onValueSelect(ref)}
          >
            Открыть точное значение {label}
          </Button>
          <BackendLineageActions
            projectId={projectId}
            revisionId={revisionId}
            target={target}
            pins={pins}
            seed={ref}
            onValueSelect={onValueSelect}
          />
        </Stack>
      ))}
    </Stack>
  );
}
