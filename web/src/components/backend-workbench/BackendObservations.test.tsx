import { MantineProvider } from "@mantine/core";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import { BackendObservationSelect, BackendObservationProvider } from "./BackendObservationContext";
import { BackendCorrelation } from "./BackendCorrelation";
import type {
  BackendDiagramScopeInput,
  BackendDiagramVersion,
  ObservationCorrelationSnapshot,
} from "@/api/generated/schemas";
const resolve = vi.hoisted(() => vi.fn());
vi.mock("@/api/generated/backend-projects/backend-projects", () => ({
  resolveBackendDiagramScope: resolve,
}));
describe("observation diagram choices", () => {
  it("requires an explicit keyboard-accessible action, not opening a view", async () => {
    resolve.mockResolvedValue({ status: 200, data: { targetHash: "hash", truncated: false } });
    const input = {
      pin: { id: "d", version: 1, contentHash: "h" },
      selectors: [{ kind: "semantic", id: "member" }],
    } as BackendDiagramScopeInput;
    render(
      <MantineProvider>
        <BackendObservationProvider>
          <BackendObservationSelect
            projectId="p"
            input={input}
            diagram={{ targetHash: "hash" } as BackendDiagramVersion}
          />
        </BackendObservationProvider>
      </MantineProvider>,
    );
    expect(resolve).not.toHaveBeenCalled();
    const button = screen.getByRole("button", { name: "Наблюдения выбранного элемента" });
    button.focus();
    expect(button).toHaveFocus();
    fireEvent.click(button);
    await waitFor(() => expect(resolve).toHaveBeenCalledWith("p", input));
  });
  it("manual candidates require a reason and retain inferred provenance", () => {
    const onOverride = vi.fn();
    const ref = { kind: "record", recordType: "node", id: "node" };
    const snapshot = {
      sourceCompatible: false,
      version: 1,
      contentHash: "h",
      input: {},
      gaps: ["unknown build"],
      rows: [
        { recordId: "r", outcome: "unresolved", method: "locator", reasons: [], candidates: [ref] },
      ],
      diagramRows: [],
    } as unknown as ObservationCorrelationSnapshot;
    render(
      <MantineProvider>
        <BackendCorrelation snapshot={snapshot} onOverride={onOverride} />
      </MantineProvider>,
    );
    const button = screen.getByRole("button");
    expect(button).toBeDisabled();
    fireEvent.change(screen.getByLabelText("Причина ручного сопоставления"), {
      target: { value: "reviewed exact source" },
    });
    fireEvent.click(button);
    expect(onOverride).toHaveBeenCalledWith({
      recordId: "r",
      ref,
      reason: "reviewed exact source",
    });
  });
});
