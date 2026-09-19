import { describe, expect, it } from "vitest";
import { findJsonPointerRange } from "./jsonPointerRange";

describe("findJsonPointerRange", () => {
  it("uses every pointer token when object keys repeat", () => {
    const source = JSON.stringify(
      {
        info: { description: "first" },
        paths: {
          "/orders": { get: { description: "orders" } },
          "/users": { get: { description: "users" } },
        },
      },
      null,
      2,
    );

    const range = findJsonPointerRange(source, "/paths/~1users/get/description");

    expect(range).not.toBeNull();
    expect(source.slice(range?.startOffset, range?.endOffset)).toContain('"description": "users"');
  });

  it("resolves escaped schema names and array indexes", () => {
    const source = JSON.stringify({ components: { schemas: { "A/B~C": { oneOf: [1, 2] } } } });

    const range = findJsonPointerRange(source, "/components/schemas/A~1B~0C/oneOf/1");

    expect(source.slice(range?.startOffset, range?.endOffset)).toBe("2");
  });
});
