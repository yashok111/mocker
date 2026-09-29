import { useEffect, useRef, useState } from "react";
import type { CanvasDocument, DataFlowAnalysis } from "./types";

export type AnalyzeDataFlow = (
  document: CanvasDocument,
  signal: AbortSignal,
) => Promise<DataFlowAnalysis>;

// Keep the last catalog while a draft is incomplete, but only the current response may allow Run.
export function useDataFlowAnalysis(
  document: CanvasDocument | null,
  opened: boolean,
  analyze?: AnalyzeDataFlow,
) {
  const signature = document ? JSON.stringify(document) : null;
  const request = useRef(analyze);
  useEffect(() => {
    request.current = analyze;
  }, [analyze]);
  const [retry, setRetry] = useState(0);
  const [state, setState] = useState<{
    signature: string | null;
    analysis: DataFlowAnalysis | null;
    error: string | null;
  }>({ signature: null, analysis: null, error: null });
  const available = Boolean(analyze);
  useEffect(() => {
    if (!opened || !signature || !request.current) return;
    const controller = new AbortController();
    const timer = setTimeout(() => {
      void request.current!(JSON.parse(signature) as CanvasDocument, controller.signal)
        .then((analysis) => {
          if (!controller.signal.aborted) setState({ signature, analysis, error: null });
        })
        .catch((cause: unknown) => {
          if (!controller.signal.aborted)
            setState((previous) => ({
              signature,
              analysis: previous.analysis,
              error: cause instanceof Error ? cause.message : "Сервер недоступен",
            }));
        });
    }, 200);
    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  }, [signature, opened, available, retry]);
  const pending = available && signature !== null && state.signature !== signature;
  const error = signature === state.signature ? state.error : null;
  const current = !pending && !error && signature !== null && available ? state.analysis : null;
  return {
    analysis: state.analysis
      ? { ...state.analysis, diagnostics: current?.diagnostics ?? [] }
      : null,
    current,
    pending,
    error,
    retry: () => {
      setState((previous) => ({ ...previous, signature: null, error: null }));
      setRetry((value) => value + 1);
    },
  };
}
