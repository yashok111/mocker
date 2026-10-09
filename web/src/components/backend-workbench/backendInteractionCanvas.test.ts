import { expect, it } from "vitest";
import { readFileSync } from "node:fs";
const source = readFileSync("../internal/backendmodel/testdata/diagrams/interaction.json", "utf8");
import type { BackendInteractionDocument } from "@/api/generated/schemas";
import { interactionCanvas } from "./backendInteractionCanvas";
it("keeps alternatives and unordered peers outside a causal sequence without mutating the pin", () => {
  const d = JSON.parse(source) as BackendInteractionDocument;
  const before = JSON.stringify(d);
  const canvas = interactionCanvas(d.payload, []);
  expect(canvas.document.messages).toHaveLength(0);
  expect(canvas.boundaries.map((b) => b.id)).toContain(d.payload.steps[3]!.id);
  expect(canvas.sidecar[d.payload.steps[1]!.id]?.branchPath).toEqual(
    d.payload.steps[1]!.branchPath,
  );
  expect(canvas.sidecar[d.payload.order[0]!.id]).toBeDefined();
  expect(JSON.stringify(d)).toBe(before);
});
it("uses only explicit causal order for a safe linear fragment", () => {
  const d = JSON.parse(source) as BackendInteractionDocument;
  d.payload.steps = d.payload.steps.slice(0, 2).map((s) => ({ ...s, branchPath: [] }));
  d.payload.branches = [];
  d.payload.order = d.payload.order.slice(0, 1);
  const forward = interactionCanvas(d.payload, []);
  d.payload.steps.reverse();
  const reversed = interactionCanvas(d.payload, []);
  expect(forward.document.messages.map((m) => m.id)).toEqual(
    reversed.document.messages.map((m) => m.id),
  );
  expect(forward.document.messages).toHaveLength(2);
  expect(forward.document.contracts).toEqual([]);
});

it("applies presentation positions and collapse without editing semantic order", () => {
  const d = JSON.parse(source) as BackendInteractionDocument;
  d.payload.steps = d.payload.steps.slice(0, 2).map((s) => ({ ...s, branchPath: [] }));
  d.payload.branches = [];
  d.payload.order = d.payload.order.slice(0, 1);
  const before = JSON.stringify(d);
  const shifted = interactionCanvas(d.payload, [], {
    positions: [{ id: d.payload.participants[0]!.id, x: 120, y: 0 }],
    collapsedIds: [],
  });
  expect(shifted.document.participants[0]!.offsetX).toBe(120);
  expect(shifted.document.participants[1]!.offsetX).toBe(-120);
  const collapsed = interactionCanvas(d.payload, [], {
    positions: [],
    collapsedIds: [d.payload.participants[0]!.id],
  });
  expect(collapsed.document.participants).toHaveLength(1);
  expect(collapsed.document.messages).toHaveLength(0);
  expect(JSON.stringify(d)).toBe(before);
});

it("keeps local actions out of message rendering without a missing receiver claim", () => {
  const d = JSON.parse(source) as BackendInteractionDocument;
  d.payload.steps = [
    { ...d.payload.steps[0]!, kind: "action", to: undefined, replyTo: undefined, branchPath: [] },
  ];
  d.payload.order = [];
  d.payload.branches = [];
  const canvas = interactionCanvas(d.payload, []);
  expect(canvas.document.messages).toHaveLength(0);
  expect(canvas.boundaries[0]?.reason).toBe("Локальное действие участника");
});
