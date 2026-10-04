import { useState } from "react";
import { Button, Stack, Text } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import type {
  ArtifactProjectionLocator,
  BackendEffectiveGraphPins,
  BackendReadTarget,
  QueryBackendArtifactsRequest,
} from "@/api/generated/schemas";
import { readArtifactPage } from "./backendArtifactReads";
import { ArtifactTypedContent } from "./BackendArtifactContent";
import { LoadState } from "./BackendReadUI";
import { changeValueKey } from "./backendChangeFormModel";
import type { ChangeJSON } from "./backendChangeSchemaTypes";
export function BackendAnalysisArtifact({
  projectId,
  target,
  pins,
  locator,
  objectHash,
}: {
  projectId: string;
  target?: BackendReadTarget;
  pins: BackendEffectiveGraphPins;
  locator: ArtifactProjectionLocator;
  objectHash?: string;
}) {
  const [open, setOpen] = useState(false);
  const query = useQuery({
    queryKey: ["analysis-exact-artifact", projectId, target, pins.targetHash, locator, objectHash],
    enabled: open && !!target,
    retry: false,
    staleTime: Infinity,
    queryFn: async ({ signal }) => {
      if (!target || target.proposal)
        throw new Error("Точная проекция этой стороны доступна только в сохранённом отчёте.");
      const scope = {
        projectId,
        target,
        pins: target.revisionId && pins.viewSchemaVersion !== "6" ? undefined : pins,
        revisionId: pins.baseRevisionId,
        semanticHash: pins.effectiveSemanticHash,
        sourceSnapshotIds: pins.sourceSnapshotIds,
        artifactPins: pins.artifactPins,
      };
      let cursor = "";
      const seen = new Set<string>();
      do {
        const input = {
          ...target,
          artifact: { kind: locator.pin.kind, id: locator.pin.id },
          view: locator.view,
          ...(locator.embedded ? { embeddedContractId: locator.embedded.contractId } : {}),
          limit: 100,
          ...(cursor ? { cursor } : {}),
        } as QueryBackendArtifactsRequest;
        const page = await readArtifactPage(scope, input, signal);
        const item = page.items.find(
          (x) =>
            changeValueKey(x.locator as unknown as ChangeJSON) ===
            changeValueKey(locator as unknown as ChangeJSON),
        );
        if (item) {
          if (objectHash && item.objectHash !== objectHash)
            throw new Error("Хеш объекта не соответствует отчёту");
          return item;
        }
        cursor = page.nextCursor;
        if (cursor && seen.has(cursor)) throw new Error("Повтор курсора точной проекции");
        seen.add(cursor);
      } while (cursor);
      throw new Error("Точный объект отсутствует в закреплённой проекции");
    },
  });
  return (
    <Stack gap="xs">
      <Button variant="subtle" onClick={() => setOpen((v) => !v)} aria-expanded={open}>
        Осмотреть точный объект артефакта {locator.pin.kind} {locator.pin.id}
      </Button>
      {open && (
        <>
          {!target ? (
            <Text>
              Сторона несохранённого буфера закреплена в отчёте; текущий артефакт не подменяет её.
            </Text>
          ) : (
            <LoadState query={query} label="объекта артефакта" />
          )}
          {query.data && (
            <>
              <Text>{query.data.label}</Text>
              <ArtifactTypedContent data={query.data.data} />
            </>
          )}
        </>
      )}
    </Stack>
  );
}
