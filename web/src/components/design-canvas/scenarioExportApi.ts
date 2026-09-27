import {
  exportDesignScenario,
  getDesignScenarioExportOptions,
} from "@/api/generated/design-scenarios/design-scenarios";
import type {
  ScenarioExportArtifact,
  ScenarioExportFormat,
  ScenarioExportOptions,
} from "@/api/generated/schemas";

export async function loadScenarioExportOptions(
  scenarioId: number,
  revisionId: number,
): Promise<ScenarioExportOptions> {
  const response = await getDesignScenarioExportOptions(scenarioId, revisionId);
  return response.data as ScenarioExportOptions;
}

export async function loadScenarioArtifact(
  scenarioId: number,
  revisionId: number,
  format: ScenarioExportFormat,
  contractId?: string,
): Promise<ScenarioExportArtifact> {
  const response = await exportDesignScenario(
    scenarioId,
    revisionId,
    format,
    contractId ? { contractId } : undefined,
  );
  return response.data as ScenarioExportArtifact;
}
