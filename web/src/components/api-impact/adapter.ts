import { analyzeApiDesignImpact } from "@/api/generated/api-designs/api-designs";
import type { ImpactRequest } from "./useImpactAnalysis";

export async function analyzeImpact(request: ImpactRequest, signal: AbortSignal) {
  const { designId, ...input } = request;
  const response = await analyzeApiDesignImpact(designId, input, { signal });
  if (response.status !== 200) throw new Error("Не удалось проанализировать влияние");
  return response.data;
}
