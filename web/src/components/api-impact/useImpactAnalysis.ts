import { useEffect, useRef, useState } from "react";
import type { ApiImpactInput, ApiImpactReport } from "@/api/generated/schemas";
import { describeApiFailureDetailed } from "@/api/errors";
import { analyzeImpact } from "./adapter";

export type ImpactRequest = ApiImpactInput & { designId: number };
type State = {
  key: string;
  report: ApiImpactReport | null;
  error: string | null;
  pending: boolean;
};

export function useImpactAnalysis(request: ImpactRequest, blocked: boolean) {
  const key = JSON.stringify(request);
  const [state, setState] = useState<State | null>(null);
  const controller = useRef<AbortController | null>(null);
  const generation = useRef(0);
  useEffect(() => {
    generation.current += 1;
    controller.current?.abort();
    // Context changes invalidate an explicit analysis even if the user later returns to it.
    // oxlint-disable-next-line react/set-state-in-effect -- Clear request state on changed analysis inputs.
    setState(null);
    return () => {
      generation.current += 1;
      controller.current?.abort();
    };
  }, [key, blocked]);

  async function analyze() {
    if (blocked) return;
    controller.current?.abort();
    const active = new AbortController();
    controller.current = active;
    const run = ++generation.current;
    setState({ key, report: null, error: null, pending: true });
    try {
      const report = await analyzeImpact(request, active.signal);
      if (!active.signal.aborted && run === generation.current)
        setState({ key, report, error: null, pending: false });
    } catch (cause) {
      if (!active.signal.aborted && run === generation.current)
        setState({ key, report: null, error: describeApiFailureDetailed(cause), pending: false });
    }
  }
  const current = !blocked && state?.key === key ? state : null;
  return {
    report: current?.report ?? null,
    error: current?.error ?? null,
    pending: current?.pending ?? false,
    analyze,
  };
}
