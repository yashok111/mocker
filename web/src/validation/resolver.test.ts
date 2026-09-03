import { describe, expect, it } from "vitest";
import { type } from "arktype";
import { arktypeResolver } from "./resolver";
import { userName } from "./name";

const form = type({ name: userName, password: "string" });
const resolve = arktypeResolver(form);

// react-hook-form calls a resolver with (values, context, options); only the
// first is read here, so the other two are stubbed with the minimum shape.
function run(values: unknown) {
  return resolve(values as never, undefined, {
    fields: {},
    shouldUseNativeValidation: false,
  } as never);
}

describe("arktypeResolver", () => {
  it("passes the parsed value through when everything validates", async () => {
    const out = await run({ name: "alex", password: "hunter2" });

    expect(out.errors).toEqual({});
    expect(out.values).toEqual({ name: "alex", password: "hunter2" });
  });

  it("reports a failure against the field it belongs to, with arktype's message", async () => {
    const out = await run({ name: "", password: "hunter2" });

    expect(out.values).toEqual({});
    expect(out.errors.name?.message).toContain("Введите имя");
    // The valid sibling must not pick up an error just because the form failed.
    expect(out.errors.password).toBeUndefined();
  });

  it("reports every failing field, not only the first", async () => {
    const out = await run({ name: "", password: 42 });

    expect(out.errors.name).toBeDefined();
    expect(out.errors.password).toBeDefined();
  });

  it("keeps one message per field", async () => {
    const out = await run({ name: "a".repeat(200) });

    expect(out.errors.name?.type).toBe("validation");
    expect(typeof out.errors.name?.message).toBe("string");
  });
});
