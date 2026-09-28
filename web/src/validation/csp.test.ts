// @vitest-environment node
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { expect, it } from "vitest";

it("validates form input without probing dynamic code when jitless is configured", () => {
  // A fresh process catches import-time probes before ArkType caches the result.
  const output = execFileSync(
    process.execPath,
    [
      "--input-type=module",
      "-e",
      `
        let attempts = 0;
        const blocked = () => {
          attempts++;
          throw new EvalError("Dynamic code is blocked by CSP");
        };
        globalThis.Function = new Proxy(Function, { construct: blocked, apply: blocked });
        await import("./src/validation/configure.ts");
        const { userName } = await import("./src/validation/name.ts");
        const { type } = await import("arktype");
        console.log(JSON.stringify({
          valid: userName("demo"),
          invalid: userName(" ") instanceof type.errors,
          attempts,
        }));
      `,
    ],
    { cwd: fileURLToPath(new URL("../../", import.meta.url)), encoding: "utf8" },
  );

  expect(JSON.parse(output)).toEqual({ valid: "demo", invalid: true, attempts: 0 });
});
