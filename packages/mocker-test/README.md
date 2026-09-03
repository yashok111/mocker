# @yashok111/mocker-test

Own a [mocker](../../README.md) workspace from a test suite. The mock plane's
control routes — `{prefix}/health` and `{prefix}/state` on the workspace
host, unauthenticated by design — become a typed client, a Playwright
fixture and a set of Cypress commands. Zero dependencies; Node ≥ 18 or any
browser with `fetch`.

```ts
import { mocker } from "@yashok111/mocker-test";

const mock = mocker("http://alex.mock.local"); // the URL the «Подключить» panel / get_workspace shows

beforeEach(() => mock.reset()); // clears every directive (and releases a pause)

test("shows a retry on 503", async () => {
  await mock.fail("POST /orders", 503, { times: 2 }); // the next two POST /orders fail, then normal
  await mock.delay("*", 800); // every response waits 800 ms
  // … drive the app …
});

test("empty cart", async () => {
  await mock.scenario("checkout-empty"); // a scenario created in the panel or by an agent
  // …
  await mock.scenario(null); // deactivate
});
```

## The client

| call | route | effect |
|---|---|---|
| `health()` | `GET {prefix}/health` | `{ ok, workspace, revision, spec }` — `revision` moves on every configuration change and scenario switch, never on a directive |
| `state()` | `GET {prefix}/state` | the directives currently set |
| `reset()` | `DELETE {prefix}/state` | clears every directive; releases every `pause`; leaves the scenario as it is |
| `clear(target, action?)` | `DELETE {prefix}/state {target, action?}` | removes one target's directives (every action, or the one given); releases a pause on it; other targets stay |
| `scenario(name \| null)` | `POST {prefix}/state {scenario}` | activates by name (persisted, bumps `revision`); `null`/`""` deactivates; unknown name → 404 |
| `status(target, code)` | directive `status` | every matching request answers `code` until `reset()` |
| `fail(target, code, {times})` | directive `fail` | the next `times` (default 1) matching requests answer `code`, then normal |
| `delay(target, ms)` | directive `delay` | every matching response waits `ms` (1..30000) |
| `pause(target)` | directive `pause` | every matching request is parked until `clear(target)` or `reset()` |
| `waitForRevision(n, {timeoutMs, intervalMs})` | polls `health` | resolves once `revision >= n`; `MockerTimeoutError` otherwise |
| `fetch(path, init)` | the workspace host | a plain request, for asserting what the mock serves |

`target` is `"*"`, `"METHOD /path"` (the path RELATIVE to the workspace's
base path, exactly as the spec declares it) or `{ method, path }`.

Errors: a refused control call rejects with `MockerError { status, code,
message, details }` carrying the server's envelope; a slow one with
`MockerTimeoutError` (default 10 s, `options.timeoutMs`).

Options: `mocker(url, { prefix, fetch, timeoutMs })` — `prefix` is the
server's `MOCKER_RESERVED_PREFIX` (default `/__mocker`).

## Playwright

```ts
// fixtures.ts
import { test as base } from "@playwright/test";
import { mockerFixture, type MockerClient } from "@yashok111/mocker-test/playwright";

export const test = base.extend<{ mock: MockerClient }>({
  mock: mockerFixture({ url: process.env.MOCK_URL!, scenario: "checkout-empty" }),
});

// a spec
test("retries on 503", async ({ page, mock }) => {
  await mock.fail("POST /orders", 503, { times: 2 });
  await page.goto("/checkout");
  // …
});
```

The fixture resets before and after every test (`resetBefore`/`resetAfter`)
and activates `scenario` when given. No dependency on `@playwright/test`:
the factory returns a function of the shape `test.extend` expects.

## Cypress

```ts
// cypress/support/e2e.ts
import { registerMockerCommands } from "@yashok111/mocker-test/cypress";
registerMockerCommands({ Cypress, cy }, { url: Cypress.env("MOCK_URL") });

// a test
beforeEach(() => {
  cy.mockerReset();
  cy.mockerScenario("checkout-empty");
});
it("retries on 503", () => {
  cy.mockerFail("POST /orders", 503, { times: 2 });
  cy.visit("/checkout");
});
```

Commands: `mockerHealth`, `mockerState`, `mockerReset`, `mockerClear`, `mockerScenario`,
`mockerStatus`, `mockerFail`, `mockerDelay`, `mockerPause`,
`mockerWaitForRevision` (rename the prefix with `commandPrefix`). Declare
them for TypeScript in your own `d.ts` — the header of
`src/cypress.ts` has the block to paste.

## What it does not do

- Nothing on the admin plane: no login, no overrides, no spec import. That
  is an operator's or an agent's job (`docs/USER-GUIDE.md`, `skills/mocker/`).
  A test suite consumes states; it does not author them.
- `clear(target)` removes one target's directives; `reset()` everything.
  Neither touches the active scenario.
- Directives are RAM on the server: a restart forgets them, a second test
  runner sees them. One workspace per runner, or `reset()` in `beforeEach`.

## Developing

From the repository root: `make plugin-test` builds `bin/mocker`, installs,
typechecks, lints and runs the suite against the real binary (path routing on
a loopback port, one server per file). `make plugin-build` emits `dist/`
through tsdown: ESM (`.mjs`), CommonJS (`.cjs`) and declarations for both,
one file per subpath export.

## Installing

Published on npm as `@yashok111/mocker-test`:

```sh
npm i -D @yashok111/mocker-test     # or: yarn add -D @yashok111/mocker-test
```

A release is a tag `plugin-v<version>` on the repository, where `<version>`
is what `package.json` says — `.github/workflows/release-plugin.yml` runs the
package's bars against the real binary and publishes with provenance through
npm Trusted Publishing; there is no npm token anywhere in the repository.

A tarball still works where a registry is out of reach:

```sh
make plugin-pack          # packages/mocker-test/yashok111-mocker-test-0.1.1.tgz
npm i -D ./yashok111-mocker-test-0.1.1.tgz
```

`npm pack` runs the build itself (the `prepack` script), so the tarball
always carries a fresh `dist/`.

`import { mocker } from "@yashok111/mocker-test"` resolves, as do the `/playwright`
and `/cypress` subpaths; the package is ESM only and needs Node 24 or
newer (which can also `require()` it).
