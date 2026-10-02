// @vitest-environment node
import { describe, expect, it } from "vitest";
import { addLocalOperation, emptyCanvas } from "./canvasModel";
import { buildCanvasContractGroups, previewCanvasContractGroups } from "./canvasContractGroups";

describe("canvas contract groups", () => {
  it("keeps a shared source operation available to each receiver", () => {
    const base = {
      ...emptyCanvas(),
      participants: [
        { id: "client", name: "Клиент", kind: "client" as const, description: "" },
        { id: "one", name: "Первый", kind: "service" as const, description: "" },
        { id: "two", name: "Второй", kind: "service" as const, description: "" },
      ],
      messages: [
        {
          id: "a",
          fromId: "client",
          toId: "one",
          kind: "request" as const,
          label: "GET /status",
          description: "",
        },
        {
          id: "b",
          fromId: "client",
          toId: "two",
          kind: "request" as const,
          label: "GET /status",
          description: "",
        },
      ],
    };
    const first = addLocalOperation(base, "a", {
      method: "GET",
      path: "/status",
      responseStatus: "default",
    });
    const source = first.contracts[0]!;
    const doc = addLocalOperation(first, "b", {
      method: "GET",
      path: "/status",
      responseStatus: "default",
      contractId: source.id,
    });
    const groups = previewCanvasContractGroups(doc);
    expect(groups).toHaveLength(2);
    expect(groups.every((group) => group.document.contracts[0] === source)).toBe(true);
    const results = buildCanvasContractGroups(
      groups,
      ["One API", "Two API"],
      ["new-one", "new-two"],
    );
    expect(results.every((result) => result.errors.length === 0 && result.contract !== null)).toBe(
      true,
    );
    expect(results.map((result) => result.bindings.map(({ messageId }) => messageId))).toEqual([
      ["a"],
      ["b"],
    ]);
    expect(doc.contracts).toEqual([source]);
  });
  it("separates receivers and combines repeated endpoints within each", () => {
    const doc = {
      ...emptyCanvas(),
      participants: [
        { id: "client", name: "Клиент", kind: "client" as const, description: "" },
        { id: "one", name: "Первый", kind: "service" as const, description: "" },
        { id: "two", name: "Второй", kind: "service" as const, description: "" },
      ],
      messages: [
        {
          id: "a",
          fromId: "client",
          toId: "one",
          kind: "request" as const,
          label: "GET /status",
          description: "",
        },
        {
          id: "b",
          fromId: "client",
          toId: "two",
          kind: "request" as const,
          label: "GET /status",
          description: "",
        },
        {
          id: "c",
          fromId: "client",
          toId: "one",
          kind: "request" as const,
          label: "GET /status",
          description: "",
        },
      ],
    };
    const groups = previewCanvasContractGroups(doc);
    expect(groups.map(({ participantId }) => participantId)).toEqual(["one", "two"]);
    expect(groups[0]!.rows).toHaveLength(1);
    expect(groups[0]!.rows[0]!.messageIds).toEqual(["a", "c"]);
    expect(groups[1]!.rows[0]!.messageIds).toEqual(["b"]);
    const results = buildCanvasContractGroups(
      groups,
      ["Первый API", "Второй API"],
      ["new-one", "new-two"],
    );
    expect(results.map(({ contract }) => contract?.name)).toEqual(["Первый API", "Второй API"]);
    expect(results[0]!.bindings.map(({ messageId }) => messageId)).toEqual(["a", "c"]);
    expect(results[1]!.bindings.map(({ messageId }) => messageId)).toEqual(["b"]);
    groups[0]!.rows.forEach((row) => {
      row.included = false;
    });
    const selected = buildCanvasContractGroups(
      groups,
      ["One API", "Two API"],
      ["one-id", "two-id"],
    );
    expect(selected).toHaveLength(1);
    expect(selected[0]!.contract).toMatchObject({ id: "two-id", name: "Two API" });
    expect(selected[0]!.bindings.map(({ messageId }) => messageId)).toEqual(["b"]);
  });
});
