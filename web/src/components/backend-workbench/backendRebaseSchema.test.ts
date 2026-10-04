import { expect, it } from "vitest";
import type { BackendChangeRebaseConflict } from "@/api/generated/schemas";
import { rebaseReplacementSchema } from "./backendRebaseSchema";
import { changeSchemaIssues } from "./backendChangeFormModel";
it("restricts replacement to the conflict semantic group and preserves ordered typed arrays", () => {
  const conflict = {
    object: { recordType: "node" },
    selector: {
      kind: "property",
      property: {
        kind: "source",
        source: { kind: "relational_facet", facetKey: "main", group: "nullable" },
      },
    },
  } as BackendChangeRebaseConflict;
  const schema = rebaseReplacementSchema(conflict, "column")!;
  expect(changeSchemaIssues(schema, { status: "known", value: false })).toEqual([]);
  expect(changeSchemaIssues(schema, "unrelated string").length).toBeGreaterThan(0);
  expect(
    rebaseReplacementSchema({ ...conflict, selector: { kind: "record" } }, "column"),
  ).toBeUndefined();
});
