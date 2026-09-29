import { describe, expect, it } from "vitest";
import { blankRule, headerTemplate, readRules, writeRules, removeNode, EXTENSION } from "./model";

const source = JSON.stringify({ openapi: "3.1.0", paths: {}, "x-neighbor": { keep: true } });
describe("response rule document buffer", () => {
  it("creates stable blank and header graphs without changing neighboring fields", () => {
    const blank = blankRule();
    const header = headerTemplate();
    const next = writeRules(source, [blank, header]);
    expect(readRules(next).rules).toEqual([blank, header]);
    expect(JSON.parse(next)["x-neighbor"]).toEqual({ keep: true });
    expect(blank.nodes.map((node) => node.type)).toEqual(["start", "fallback"]);
    expect(header.edges.filter((edge) => edge.from === "auth").map((edge) => edge.port)).toEqual([
      "true",
      "false",
    ]);
    expect(header.nodes.find((node) => node.id === "slow")).toMatchObject({ delayMs: 250 });
    expect(blank.id).not.toBe(blankRule().id);
  });
  it("removes incident edges atomically while keeping surviving identities", () => {
    const rule = headerTemplate();
    const next = removeNode(rule, "auth");
    expect(next.nodes.some((node) => node.id === "auth")).toBe(false);
    expect(next.edges.map((edge) => edge.id)).toEqual(["e4"]);
    expect(next.id).toBe(rule.id);
  });
  it.each([
    { formatVersion: 2, rules: [] },
    { formatVersion: 1, rules: [], unknown: true },
    { formatVersion: 1, rules: [{ ...blankRule(), unknown: true }] },
    {
      formatVersion: 1,
      rules: [
        { ...blankRule(), nodes: [{ id: "broken", type: "condition", name: "", x: 0, y: 0 }] },
      ],
    },
  ])("refuses malformed known data instead of replacing it with an empty graph", (extension) => {
    expect(() => readRules(JSON.stringify({ [EXTENSION]: extension }))).toThrow();
  });
  it("permits absent bindings and separate node/edge ID namespaces", () => {
    const rule = blankRule();
    rule.edges[0]!.id = rule.nodes[0]!.id;
    expect(readRules(writeRules(source, [rule])).rules[0]).toEqual(rule);
  });
  it("blocks lossy full-document mutation while retaining raw body JSON", () => {
    expect(() => writeRules('{"value":9007199254740993}', [blankRule()])).toThrow(/точности/);
    const rule = headerTemplate();
    const response = rule.nodes.find((node) => node.type === "response")!;
    if (response.type === "response") response.response.bodyJSON = '{"value":9007199254740993}';
    expect(readRules(writeRules(source, [rule])).rules).toEqual([rule]);
  });
  it.each([
    { mediaType: "text/html", headers: [] },
    { mediaType: "application/json; broken", headers: [] },
    { mediaType: "application/json", headers: [{ name: "Set-Cookie2", value: "bad" }] },
    { mediaType: "application/json", headers: [{ name: "X-Test", value: "line\r\ninjected" }] },
    {
      mediaType: "application/json",
      headers: [
        { name: "X-Test", value: "one" },
        { name: "x-test", value: "two" },
      ],
    },
  ])("preserves unsafe existing responses for source recovery", (response) => {
    const rule = headerTemplate();
    const node = rule.nodes.find((item) => item.type === "response")!;
    if (node.type === "response") node.response = { status: 200, ...response };
    expect(() =>
      readRules(JSON.stringify({ [EXTENSION]: { formatVersion: 1, rules: [rule] } })),
    ).toThrow();
  });
  it("accepts safe JSON suffixes, MIME parameters and authored incomplete predicates", () => {
    const rule = headerTemplate();
    const node = rule.nodes.find((item) => item.type === "response")!;
    if (node.type === "response")
      node.response.mediaType = 'application/problem+json; charset="utf-8"';
    const condition = rule.nodes.find((item) => item.type === "condition")!;
    if (condition.type === "condition")
      condition.condition = { in: "body", name: "", op: "equals" };
    expect(readRules(writeRules(source, [rule])).rules).toEqual([rule]);
  });
});
