import { useQuery } from "@tanstack/react-query";
import type { BackendEffectiveGraphPins, BackendReadTarget } from "@/api/generated/schemas";
import { readBackendCoverage, backendReadQueryKey } from "./backendGraphReads";
import { BackendExactRecordInspector } from "./BackendExactGraph";
import { LoadState } from "./BackendReadUI";
export function BackendProjectionSources({
  projectId,
  target,
  pins,
  recordType,
  id,
}: {
  projectId: string;
  target: BackendReadTarget;
  pins?: BackendEffectiveGraphPins;
  recordType: "node" | "edge";
  id: string;
}) {
  const query = useQuery({
    queryKey: backendReadQueryKey("projection-proof-mode", projectId, target, pins),
    queryFn: ({ signal }) => readBackendCoverage(projectId, target, signal, pins),
    staleTime: Infinity,
    retry: false,
  });
  const source = query.data && "source" in query.data ? query.data.source : undefined;
  return (
    <details>
      <summary>Источники и желаемые поля</summary>
      <LoadState query={query} label="контекста источников" />
      {query.data && (
        <BackendExactRecordInspector
          projectId={projectId}
          target={target}
          pins={pins}
          recordType={recordType}
          id={id}
          claimsSupported={Boolean(source?.sourceVector)}
        />
      )}
    </details>
  );
}
