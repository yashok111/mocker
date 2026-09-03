/**
 * Playwright integration: a fixture factory. No import of `@playwright/test`
 * — the returned function has the shape Playwright's `test.extend` expects
 * (`async ({}, use) => …`), so this package stays dependency-free.
 *
 *   import { test as base } from "@playwright/test";
 *   import { mockerFixture, type MockerClient } from "@yashok111/mocker-test/playwright";
 *   export const test = base.extend<{ mock: MockerClient }>({
 *     mock: mockerFixture({ url: "http://alex.mock.local", scenario: "checkout-empty" }),
 *   });
 *
 *   test("retries on 503", async ({ page, mock }) => {
 *     await mock.fail("POST /orders", 503, { times: 2 });
 *     …
 *   });
 */
import { mocker, type MockerClient, type MockerOptions } from "./index.js";

export type { MockerClient } from "./index.js";

export interface MockerFixtureOptions extends MockerOptions {
  /** The workspace URL (`get_workspace`'s `url`, or the «Подключить» panel). */
  url: string;
  /** Clear every directive before the test; default true. */
  resetBefore?: boolean;
  /** Clear every directive after the test; default true. */
  resetAfter?: boolean;
  /** Activate this scenario before the test (and leave it active). */
  scenario?: string;
}

/** A Playwright test-scoped fixture: `{ mock: mockerFixture({ url }) }`. */
export function mockerFixture(
  options: MockerFixtureOptions,
): (fixtures: object, use: (mock: MockerClient) => Promise<void>) => Promise<void> {
  const { url, resetBefore = true, resetAfter = true, scenario, ...clientOptions } = options;
  // Playwright parses the fixture function's FIRST parameter and requires the
  // object-destructuring pattern; this literal is what it sees.
  // oxlint-disable-next-line no-empty-pattern -- Playwright requires exactly this pattern
  return async ({}, use) => {
    const mock = mocker(url, clientOptions);
    if (resetBefore) await mock.reset();
    if (scenario !== undefined) await mock.scenario(scenario);
    try {
      await use(mock);
    } finally {
      if (resetAfter) await mock.reset();
    }
  };
}
