// @vitest-environment node
import { describe, expect, it } from "vitest";
import { blankRule, EXTENSION, readRules, writeRules } from "./model";
import { nodeSummary } from "./layout";

const ref = { source: "result", nodeId: "read", pointer: "/amount" };
const leaf = { source: ref, op: "equals", valueJSON: "9007199254740993" };
function documentFor(resultCondition: unknown, extra = {}) {
  return JSON.stringify({
    [EXTENSION]: {
      formatVersion: 1,
      rules: [
        {
          ...blankRule(),
          ...extra,
          nodes: [{ id: "check", type: "condition", name: "", x: 0, y: 0, resultCondition }],
        },
      ],
    },
  });
}
describe("entity result follow-up admission", () => {
  it.each([
    `{"all":[${JSON.stringify(leaf)},${JSON.stringify(leaf)}],"all":[${JSON.stringify(leaf)},${JSON.stringify(leaf)}]}`,
    `{"any":[${JSON.stringify(leaf)},${JSON.stringify(leaf)}],"a\\u006ey":[${JSON.stringify(leaf)},${JSON.stringify(leaf)}]}`,
    `{"source":${JSON.stringify(ref)},"op":"equals","op":"not_equals","valueJSON":"1"}`,
    `{"source":{"source":"result","nodeId":"read","nodeId":"other"},"op":"exists"}`,
    `{"source":${JSON.stringify(ref)},"op":"equals","valueFrom":{"source":"result","nodeId":"read","p\\u006finter":"/a","pointer":"/b"}}`,
    `{"all":[${JSON.stringify(leaf)},{"any":[${JSON.stringify(leaf)},${JSON.stringify(leaf)}],"any":[${JSON.stringify(leaf)},${JSON.stringify(leaf)}]}]}`,
  ])(
    "rejects raw duplicate predicate/ref keys before read and write lose authored terms",
    (predicate) => {
      const source = documentFor(leaf).replace(
        `"resultCondition":${JSON.stringify(leaf)}`,
        `"resultCondition":${predicate}`,
      );
      expect(() => readRules(source)).toThrow();
      expect(() => writeRules(source, [blankRule()])).toThrow();
      expect(source).toContain(predicate);
    },
  );
  it("rejects duplicate example fields and an escaped duplicate root extension", () => {
    const request = { query: [], headers: [] };
    const source = documentFor(leaf, { examples: [{ id: "case", name: "Case", request }] });
    const duplicateExample = source.replace('"name":"Case"', '"name":"Earlier","name":"Case"');
    expect(() => readRules(duplicateExample)).toThrow();
    expect(() => writeRules(duplicateExample, [blankRule()])).toThrow();
    const extension = JSON.stringify(JSON.parse(source)[EXTENSION]);
    const duplicateExtension = `{"${EXTENSION}":${extension},"x-mocker-response-rul\\u0065s":${extension}}`;
    expect(() => readRules(duplicateExtension)).toThrow();
    expect(() => writeRules(duplicateExtension, [blankRule()])).toThrow();
  });
  it("keeps the existing duplicate-key policy for unrelated OpenAPI content", () => {
    const source = documentFor(leaf).replace(
      "{",
      '{"info":{"title":"first","title":"last"},"x-other":{"all":1,"all":2},',
    );
    expect(readRules(source).document).toHaveProperty("info.title", "last");
    expect(() => writeRules(source, readRules(source).rules)).not.toThrow();
    expect(readRules('{"info":{"title":"first","title":"last"}}').rules).toEqual([]);
  });
  it.each(["greater_than", "greater_or_equal", "less_than", "less_or_equal"])(
    "preserves exact %s literals",
    (op) => {
      const parsed = readRules(documentFor({ ...leaf, op, valueJSON: "1e999999999999999999999" }));
      expect(readRules(writeRules(documentFor(leaf), parsed.rules)).rules).toEqual(parsed.rules);
    },
  );
  it("roundtrips nested groups, RHS refs and named requests without rounding authored text", () => {
    const predicate = {
      all: [
        leaf,
        {
          any: [
            { source: ref, op: "exists" },
            { source: ref, op: "less_than", valueFrom: { ...ref, nodeId: "other" } },
          ],
        },
      ],
    };
    const examples = [
      {
        id: "large",
        name: "Большое число",
        request: {
          query: [],
          headers: [],
          bodyJSON: " 9007199254740993 ",
          entities: [
            {
              family: "/orders",
              idField: "id",
              idType: "integer",
              rows: [{ key: "1", scope: [], dataJSON: '{"n":9007199254740993}' }],
            },
          ],
        },
      },
    ];
    const source = documentFor(predicate, { examples });
    const parsed = readRules(source);
    expect(readRules(writeRules(source, parsed.rules)).rules).toEqual(parsed.rules);
    expect(parsed.rules[0]).toHaveProperty("examples", examples);
    expect(nodeSummary(parsed.rules[0]!.nodes[0]!)).toContain("И");
    expect(nodeSummary(parsed.rules[0]!.nodes[0]!)).toContain("other");
  });
  it.each([
    { ...leaf, op: "less_than", valueJSON: '"10"' },
    { ...leaf, valueFrom: ref },
    { source: ref, op: "exists", valueFrom: ref },
    { source: ref, op: "equals", valueFrom: { source: "body" } },
    { source: ref, op: "equals", valueFrom: { ...ref, pointer: "/a~2b" } },
    { all: [] },
    { any: [leaf] },
    { all: [leaf, leaf], any: [leaf, leaf] },
    { all: [leaf, leaf], source: ref },
    { all: Array.from({ length: 17 }, () => leaf) },
    { all: [leaf, { any: [leaf, { all: [leaf, { any: [leaf, leaf] }] }] }] },
  ])("rejects malformed or over-budget trees", (predicate) => {
    expect(() => readRules(documentFor(predicate))).toThrow();
  });
  it.each([
    null,
    [{ id: "case", name: " ", request: { query: [], headers: [] } }],
    [{ id: "case", name: "😀".repeat(51), request: { query: [], headers: [] } }],
    [{ id: "case", name: "Case", request: { query: [], headers: [], bodyJSON: "invalid" } }],
    [{ id: "case", name: "Case", request: { query: [], headers: [], entities: [null] } }],
    Array.from({ length: 21 }, (_, index) => ({
      id: `case${index}`,
      name: "Case",
      request: { query: [], headers: [] },
    })),
    Array.from({ length: 2 }, () => ({
      id: "same",
      name: "Case",
      request: { query: [], headers: [] },
    })),
  ])("rejects malformed examples", (examples) => {
    expect(() => readRules(documentFor(leaf, { examples }))).toThrow();
  });
  it.each([
    { query: [{ name: "", value: "x" }], headers: [] },
    { query: [], headers: [{ name: "invalid header", value: "x" }] },
    { query: [], headers: [{ name: "X-Test", value: "line\nbreak" }] },
    {
      query: [],
      headers: [],
      path: [
        { name: "id", value: "1" },
        { name: "id", value: "2" },
      ],
    },
    { query: [], headers: [], bodyJSON: '{"n":1,"\\u006e":2}' },
    { query: [], headers: [], bodyJSON: "[".repeat(65) + "0" + "]".repeat(65) },
    {
      query: [],
      headers: [],
      entities: [
        {
          family: "/orders",
          idField: "id",
          idType: "integer",
          rows: [{ key: "01", scope: [], dataJSON: "{}" }],
        },
      ],
    },
    {
      query: [],
      headers: [],
      entities: [
        {
          family: "/orders",
          idField: "id",
          idType: "integer",
          rows: [{ key: "1", scope: [], dataJSON: "[]" }],
        },
      ],
    },
    {
      query: [],
      headers: [],
      entities: [
        {
          family: "/users/{}/orders",
          idField: "id",
          idType: "integer",
          rows: [{ key: "1", scope: [], dataJSON: "{}" }],
        },
      ],
    },
    {
      query: [],
      headers: [],
      entities: Array.from({ length: 2 }, () => ({
        family: "/orders",
        idField: "id",
        idType: "integer",
        rows: [],
      })),
    },
    {
      query: [],
      headers: [],
      entities: [
        {
          family: "/orders",
          idField: "id",
          idType: "integer",
          rows: Array.from({ length: 2 }, () => ({ key: "1", scope: [], dataJSON: "{}" })),
        },
      ],
    },
  ])("refuses invalid intrinsic request and fixture shapes", (request) => {
    expect(() =>
      readRules(documentFor(leaf, { examples: [{ id: "case", name: "Case", request }] })),
    ).toThrow();
  });
});
