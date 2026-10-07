import { MantineProvider } from "@mantine/core";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import {
  BackendDiagramReplayPrepare,
  BackendDiagramReplayProvider,
} from "./BackendDiagramReplayContext";
import type { BackendDiagramScopeInput, BackendDiagramVersion } from "@/api/generated/schemas";
import type { ReplayPreparation } from "./backendDiagramReplay";
describe("diagram selection replay admission", () => {
  it("opening and changing selection never invokes preparation; explicit click only reads", async () => {
    const prepare = vi
      .fn()
      .mockResolvedValue({
        projectId: "project",
        package: { diagramBindings: [], excludedIds: ["member"] },
        excluded: [],
      } as unknown as ReplayPreparation);
    const diagram = {
      projectId: "project",
      pin: { id: "diagram", version: 2, contentHash: "hash" },
    } as BackendDiagramVersion;
    const input = {
      pin: diagram.pin,
      selectors: [{ kind: "semantic", id: "first" }],
    } as BackendDiagramScopeInput;
    const tree = (selection: BackendDiagramScopeInput) => (
      <MantineProvider>
        <BackendDiagramReplayProvider>
          <BackendDiagramReplayPrepare
            projectId="project"
            diagram={diagram}
            input={selection}
            prepare={prepare}
          />
        </BackendDiagramReplayProvider>
      </MantineProvider>
    );
    const view = render(tree(input));
    expect(prepare).not.toHaveBeenCalled();
    const changed = { ...input, selectors: [{ kind: "semantic" as const, id: "second" }] };
    view.rerender(tree(changed));
    expect(prepare).not.toHaveBeenCalled();
    // Since 41ca3c6 the preparation block also renders the observation
    // selector's own button, so the replay button is addressed by name.
    fireEvent.click(
      screen.getByRole("button", { name: "Подготовить поддерживаемый replay выбранного элемента" }),
    );
    await waitFor(() => expect(prepare).toHaveBeenCalledTimes(1));
    expect(prepare).toHaveBeenCalledWith("project", diagram, changed);
  });
});
