import { expectTypeOf, test } from "vitest";
import type { SimulateResponseRuleRequest } from "./generated/schemas";

test("generated simulation input requires exactly one selection", () => {
  expectTypeOf<{ exampleId: string }>().toExtend<SimulateResponseRuleRequest>();
  expectTypeOf<{ request: { query: []; headers: [] } }>().toExtend<SimulateResponseRuleRequest>();
  expectTypeOf<{}>().not.toExtend<SimulateResponseRuleRequest>();
  expectTypeOf<{
    request: { query: []; headers: [] };
    exampleId: string;
  }>().not.toExtend<SimulateResponseRuleRequest>();
});
