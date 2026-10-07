import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { readCatalog, type CatalogItem } from "./catalogs";
const key = (item: CatalogItem) => `${item.kind}:${item.id}`;
const version = (item: CatalogItem) =>
  JSON.stringify([item.version, item.status, item.revisionId, item.hash]);
export function changedResults(before: CatalogItem[] | undefined, after: CatalogItem[]) {
  if (!before) return [];
  const seen = new Map(before.map((item) => [key(item), version(item)]));
  return after.filter((item) => seen.get(key(item)) !== version(item)).map(key);
}
export function useResultSignal(projectId: string, panel: "changes" | "checks", opened: boolean) {
  const result = useQuery({
    queryKey: ["workbench-catalog", projectId, panel, {}],
    queryFn: ({ signal }) => readCatalog(projectId, panel, signal),
    retry: false,
    staleTime: 0,
    refetchOnWindowFocus: true,
  });
  const [observation, setObservation] = useState<{
    version: number;
    before?: CatalogItem[];
    fresh: string[];
  }>({ version: 0, fresh: [] });
  if (!result.isError && result.data && result.dataUpdatedAt !== observation.version) {
    const changed = changedResults(observation.before, result.data.items);
    setObservation({
      version: result.dataUpdatedAt,
      before: result.data.items,
      fresh: opened ? [] : [...new Set([...observation.fresh, ...changed])],
    });
  } else if (opened && observation.fresh.length) setObservation({ ...observation, fresh: [] });
  return { count: opened ? 0 : observation.fresh.length, error: result.isError };
}
