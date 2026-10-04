import { afterEach, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/render";
import { json } from "@/test/http";
import { exactCoverage, exactEnvelope, exactIDs, exactSource } from "@/test/backendExact";
import { BackendEffectiveViews } from "./BackendEffectiveViews";
vi.mock("./BackendFlow", () => ({
  BackendFlow: (props: unknown) => <output data-testid="full-flow">{JSON.stringify(props)}</output>,
}));
vi.mock("./BackendDatabase", () => ({
  BackendDatabase: (props: unknown) => (
    <output data-testid="full-database">{JSON.stringify(props)}</output>
  ),
}));
vi.mock("./BackendEvents", () => ({
  BackendEvents: (props: unknown) => (
    <output data-testid="full-events">{JSON.stringify(props)}</output>
  ),
}));
vi.mock("./BackendAPIArtifacts", () => ({
  BackendAPIArtifacts: (props: unknown) => (
    <output data-testid="full-api">{JSON.stringify(props)}</output>
  ),
}));
vi.mock("./BackendArtifactProjections", () => ({
  BackendArtifactProjections: (props: unknown) => (
    <output data-testid="full-artifacts">{JSON.stringify(props)}</output>
  ),
}));
vi.mock("./BackendExactGraph", () => ({
  BackendExactGraph: (props: unknown) => (
    <output data-testid="full-graph">{JSON.stringify(props)}</output>
  ),
}));
afterEach(() => vi.unstubAllGlobals());
const target = {
  changeProposal: { proposalId: exactIDs.project, proposalRevisionId: exactIDs.snapshot },
};
it("gives every full view the same immutable draft and pins", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      json(200, { ...exactEnvelope(target), ...exactCoverage, source: exactSource() }),
    ),
  );
  renderWithProviders(
    <BackendEffectiveViews
      projectId={exactIDs.project}
      target={target}
      pins={exactEnvelope(target).pins}
    />,
  );
  for (const name of ["flow", "database", "events", "api", "artifacts", "graph"]) {
    const props = JSON.parse((await screen.findByTestId(`full-${name}`)).textContent ?? "{}");
    expect(props.target).toEqual(target);
    expect(props.pins).toEqual(exactEnvelope(target).pins);
    expect(props.revisionId).toBeUndefined();
  }
});
it("blocks all child views when coverage does not match saved pins", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      json(200, {
        ...exactEnvelope(target),
        ...exactCoverage,
        pins: { ...exactEnvelope(target).pins, targetHash: "b".repeat(64) },
      }),
    ),
  );
  renderWithProviders(
    <BackendEffectiveViews
      projectId={exactIDs.project}
      target={target}
      pins={exactEnvelope(target).pins}
    />,
  );
  expect(await screen.findByRole("alert")).toHaveTextContent(/другой граф/);
  expect(screen.queryByTestId("full-flow")).not.toBeInTheDocument();
});
