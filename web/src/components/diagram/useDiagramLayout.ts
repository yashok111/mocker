import { useEffect, useState } from "react";
import { layoutDiagram, type DiagramLayoutInput, type DiagramLayoutResult } from "./elkLayout";

// Callers supply semantic geometry (sizes, ports and labels), not saved x/y.
// Equal input avoids another expensive layout when selection or position changes.
export function useDiagramLayout(input: DiagramLayoutInput) {
  const key = JSON.stringify(input);
  const [resolved, setResolved] = useState<{
    key: string;
    layout?: DiagramLayoutResult;
    error?: string;
  }>();
  useEffect(() => {
    let cancelled = false;
    void layoutDiagram(JSON.parse(key) as DiagramLayoutInput).then(
      (layout) => {
        if (!cancelled) setResolved({ key, layout });
      },
      () => {
        if (!cancelled) setResolved({ key, error: "Не удалось рассчитать расположение графа." });
      },
    );
    return () => {
      cancelled = true;
    };
  }, [key]);
  const current = resolved?.key === key ? resolved : undefined;
  return { layout: current?.layout, pending: !current, error: current?.error };
}
