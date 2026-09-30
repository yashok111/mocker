import { describe, expect, it } from "vitest";
import { execFileSync } from "node:child_process";
import {
  blankRule,
  headerTemplate,
  readRules,
  writeRules,
  removeNode,
  EXTENSION,
  ports,
  type ResponseRule,
} from "./model";

const source = JSON.stringify({ openapi: "3.1.0", paths: {}, "x-neighbor": { keep: true } });
describe("response rule document buffer", () => {
  it("admits maximum-size scalar strings without rescanning interior whitespace", () => {
    const probe = `
      import { createServer } from "vite";
      const server = await createServer({
        configFile: false, root: process.cwd(), logLevel: "silent",
        optimizeDeps: { noDiscovery: true, include: [] },
        server: { middlewareMode: true, hmr: false, ws: false, watch: null },
      });
      try {
        const { readRules } = await server.ssrLoadModule("/src/components/response-rules/model.ts");
        const valueJSON = '"' + " ".repeat(65534) + '"';
        const source = JSON.stringify({ "x-mocker-response-rules": { formatVersion: 1, rules: [{
          id: "rule", name: "", edges: [], nodes: [{ id: "check", type: "condition", name: "", x: 0, y: 0,
            resultCondition: { source: { source: "result", nodeId: "read" }, op: "equals", valueJSON },
          }],
        }] } });
        const started = performance.now();
        const admitted = readRules(source);
        const milliseconds = performance.now() - started;
        if (admitted.rules[0].nodes[0].resultCondition.valueJSON !== valueJSON)
          throw new Error("Scalar string changed");
        process.stdout.write(JSON.stringify({ milliseconds }));
      } finally { await server.close(); }
    `;
    const output = execFileSync(process.execPath, ["--input-type=module", "--eval", probe], {
      encoding: "utf8",
      timeout: 3000,
      killSignal: "SIGKILL",
    });
    expect(JSON.parse(output).milliseconds).toBeLessThan(250);
  });
  it("bounds admission of repeated pointer segments and rejects invalid escapes", () => {
    // Load the real admission module in a killable process so a future
    // backtracking regression cannot block the test worker or browser loop.
    const probe = `
      import { createServer } from "vite";
      import { resolve } from "node:path";
      const server = await createServer({
        configFile: false,
        root: process.cwd(),
        logLevel: "silent",
        optimizeDeps: { noDiscovery: true, include: [] },
        resolve: { alias: { "@": resolve(process.cwd(), "src") } },
        server: { middlewareMode: true, hmr: false, ws: false, watch: null },
      });
      try {
        const { readRules } = await server.ssrLoadModule("/src/components/response-rules/model.ts");
        const documentFor = (pointer) => JSON.stringify({
          "x-mocker-response-rules": { formatVersion: 1, rules: [{
            id: "rule", name: "", edges: [], nodes: [{
              id: "check", type: "condition", name: "", x: 0, y: 0,
              resultCondition: { source: { source: "result", nodeId: "read", pointer }, op: "exists" },
            }],
          }] },
        });
        const valid = "/".repeat(2048);
        const admitted = readRules(documentFor(valid));
        if (admitted.rules[0].nodes[0].resultCondition.source.pointer !== valid)
          throw new Error("Valid empty pointer segments changed");
        for (const pointer of ["/".repeat(32) + "~2", "/".repeat(2046) + "~2", "/".repeat(2047) + "~", "/".repeat(2049)]) {
          let rejected = false;
          try { readRules(documentFor(pointer)); }
          catch { rejected = true; }
          if (!rejected) throw new Error("Invalid or oversized pointer was accepted");
        }
        process.stdout.write("admitted-valid/rejected-invalid");
      } finally { await server.close(); }
    `;
    expect(
      execFileSync(process.execPath, ["--input-type=module", "--eval", probe], {
        encoding: "utf8",
        timeout: 3000,
        killSignal: "SIGKILL",
      }),
    ).toBe("admitted-valid/rejected-invalid");
  });
  it.each(["9007199254740993", "1e999999999999999999999", "null", '"paid"', "false"])(
    "roundtrips result predicates with exact scalar JSON %s",
    (valueJSON) => {
      const rule = {
        ...blankRule(),
        nodes: [
          {
            id: "check",
            type: "condition",
            name: "Проверка",
            x: 0,
            y: 0,
            resultCondition: {
              source: { source: "result", nodeId: "read", pointer: "/n" },
              op: "equals",
              valueJSON,
            },
          },
        ],
      } as unknown as ResponseRule;
      expect(readRules(writeRules(source, [rule])).rules[0]).toEqual(rule);
    },
  );
  it.each([
    { source: { source: "result", nodeId: "read" }, op: "exists" },
    { source: { source: "result", nodeId: "read", pointer: "/a~1b/~0/0" }, op: "not_exists" },
  ])("admits result presence predicates without expected values", (resultCondition) => {
    const rule = {
      ...blankRule(),
      nodes: [{ id: "check", type: "condition", name: "", x: 0, y: 0, resultCondition }],
    } as unknown as ResponseRule;
    expect(readRules(writeRules(source, [rule])).rules[0]).toEqual(rule);
  });
  it.each([
    { source: { source: "body", pointer: "/n" }, op: "exists" },
    { source: { source: "result", nodeId: "read", name: "unexpected" }, op: "exists" },
    { source: { source: "result", nodeId: "read", pointer: "/bad~2escape" }, op: "exists" },
    { source: { source: "result", nodeId: "read" }, op: "contains", valueJSON: '"a"' },
    { source: { source: "result", nodeId: "read" }, op: "equals" },
    { source: { source: "result", nodeId: "read" }, op: "exists", valueJSON: "null" },
    { source: { source: "result", nodeId: "read" }, op: "not_exists", valueJSON: null },
    ...["{}", "[]", "01", "undefined", "1 2", ""].map((valueJSON) => ({
      source: { source: "result", nodeId: "read" },
      op: "equals",
      valueJSON,
    })),
  ])("refuses malformed result predicates", (resultCondition) => {
    const rule = {
      ...blankRule(),
      nodes: [{ id: "check", type: "condition", name: "", x: 0, y: 0, resultCondition }],
    };
    expect(() =>
      readRules(JSON.stringify({ [EXTENSION]: { formatVersion: 1, rules: [rule] } })),
    ).toThrow();
  });
  it("refuses overlapping request/result payloads and result payloads on other nodes", () => {
    const resultCondition = { source: { source: "result", nodeId: "read" }, op: "exists" };
    const node = { id: "check", type: "condition", name: "", x: 0, y: 0, resultCondition };
    for (const candidate of [
      { ...node, condition: { in: "body", name: "n", op: "exists" } },
      { ...node, type: "start" },
      { ...node, resultCondition: null },
    ]) {
      expect(() =>
        readRules(
          JSON.stringify({
            [EXTENSION]: { formatVersion: 1, rules: [{ ...blankRule(), nodes: [candidate] }] },
          }),
        ),
      ).toThrow();
    }
  });
  it("roundtrips entity refs and dynamic response bodies without changing raw JSON numbers", () => {
    const rule = {
      ...blankRule(),
      nodes: [
        {
          id: "create",
          type: "entity_create",
          name: "Создать",
          x: 0,
          y: 0,
          entity: {
            family: "/orders",
            scope: [],
            data: { source: "literal", valueJSON: '{"n":9007199254740993}' },
          },
        },
        {
          id: "response",
          type: "response",
          name: "Ответ",
          x: 400,
          y: 0,
          response: {
            status: 201,
            mediaType: "application/json",
            headers: [],
            bodyFrom: { source: "result", nodeId: "create", pointer: "" },
          },
        },
      ],
      edges: [],
    } as unknown as ResponseRule;
    expect(readRules(writeRules(source, [rule])).rules[0]).toEqual(rule);
  });
  it("offers missing branches for single records and next for list/create", () => {
    const node = {
      id: "read",
      name: "Читать",
      x: 0,
      y: 0,
      type: "entity_read",
      entity: { family: "/orders", operation: "get", key: { source: "path", name: "id" } },
    };
    expect(ports(node as never)).toEqual(["found", "missing"]);
    expect(ports({ ...node, entity: { family: "/orders", operation: "list" } } as never)).toEqual([
      "next",
    ]);
    expect(ports({ ...node, type: "entity_create" } as never)).toEqual(["next"]);
    expect(ports({ ...node, type: "entity_update" } as never)).toEqual(["found", "missing"]);
  });
  it.each([
    { source: "literal", valueJSON: "1", unknown: true },
    { source: "result", nodeId: "create", pointer: 42 },
    { source: "body", pointer: "/n", valueJSON: "2" },
  ])("refuses malformed value references", (data) => {
    const rule = {
      ...blankRule(),
      nodes: [
        {
          id: "create",
          name: "",
          x: 0,
          y: 0,
          type: "entity_create",
          entity: { family: "/orders", data },
        },
      ],
    };
    expect(() =>
      readRules(JSON.stringify({ [EXTENSION]: { formatVersion: 1, rules: [rule] } })),
    ).toThrow();
  });
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
    if (condition.type === "condition" && "condition" in condition)
      condition.condition = { in: "body", name: "", op: "equals" };
    expect(readRules(writeRules(source, [rule])).rules).toEqual([rule]);
  });
});
