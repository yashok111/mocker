import { defineConfig } from "tsdown";

// Three entries, one per subpath export in package.json. ESM only, on
// the owner's call (2026-09-03): Playwright and Cypress configs are ESM
// today and Node 24 `require()`s an ESM module natively, so a CJS twin
// would only double the files. Declarations come from the same pass
// (rolldown-plugin-dts), so tsc is no longer part of the build —
// `yarn typecheck` still runs it over src and test for the diagnostics.
export default defineConfig({
  entry: {
    index: "src/index.ts",
    playwright: "src/playwright.ts",
    cypress: "src/cypress.ts",
  },
  format: ["esm"],
  // .js/.d.ts, not .mjs/.d.mts: the package is "type": "module", so .js IS
  // ESM, and package.json's exports name these files.
  fixedExtension: false,
  platform: "node",
  target: "node24",
  dts: true,
  clean: true,
  outDir: "dist",
});
