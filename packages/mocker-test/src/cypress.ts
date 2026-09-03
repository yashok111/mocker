/**
 * Cypress integration: registers `cy.mocker*` commands over one client. No
 * import of `cypress` — the two globals are passed in, typed by the two
 * methods this file uses, so the package stays dependency-free. Commands run
 * in the browser; the mock plane answers CORS to every origin, so the calls
 * reach the workspace host from the app's origin.
 *
 *   // cypress/support/e2e.ts
 *   import { registerMockerCommands } from "@yashok111/mocker-test/cypress";
 *   registerMockerCommands({ Cypress, cy }, { url: Cypress.env("MOCK_URL") });
 *
 *   // a test
 *   beforeEach(() => { cy.mockerReset(); cy.mockerScenario("checkout-empty"); });
 *   it("retries on 503", () => { cy.mockerFail("POST /orders", 503, { times: 2 }); … });
 *
 * Declare the commands for TypeScript in your own d.ts (Cypress.Chainable
 * is not visible from here):
 *
 *   declare global { namespace Cypress { interface Chainable {
 *     mockerHealth(): Chainable<Health>; mockerState(): Chainable<StateList>;
 *     mockerReset(): Chainable<Cleared>; mockerClear(target: Target, action?: Directive["action"]): Chainable<Cleared>;
 *     mockerScenario(name: string | null): Chainable<ScenarioSwitched>;
 *     mockerStatus(target: Target, status: number): Chainable<StateList>;
 *     mockerFail(target: Target, status: number, options?: FailOptions): Chainable<StateList>;
 *     mockerDelay(target: Target, ms: number): Chainable<StateList>;
 *     mockerPause(target: Target): Chainable<StateList>;
 *     mockerWaitForRevision(atLeast: number, options?: WaitOptions): Chainable<Health>;
 *   } } }
 */
import {
  mocker,
  type Directive,
  type FailOptions,
  type MockerOptions,
  type Target,
  type WaitOptions,
} from "./index.js";

export type {
  Cleared,
  Directive,
  FailOptions,
  Health,
  ScenarioSwitched,
  StateList,
  Target,
  WaitOptions,
} from "./index.js";

/** The slice of Cypress this file needs. */
export interface CypressGlobals {
  Cypress: { Commands: { add(name: string, fn: (...args: never[]) => unknown): void } };
  cy: { wrap<T>(value: Promise<T> | T, options?: { log?: boolean }): unknown };
}

export interface MockerCommandsOptions extends MockerOptions {
  url: string;
  /** Prefix of the command names; default `mocker` → `cy.mockerReset()` etc. */
  commandPrefix?: string;
}

/** Registers the `cy.mocker*` commands. Call once from the support file. */
export function registerMockerCommands(
  globals: CypressGlobals,
  options: MockerCommandsOptions,
): void {
  const { url, commandPrefix = "mocker", ...clientOptions } = options;
  const mock = mocker(url, clientOptions);
  const { Cypress, cy } = globals;
  const name = (verb: string) => commandPrefix + verb.charAt(0).toUpperCase() + verb.slice(1);
  const wrap = <T>(p: Promise<T>) => cy.wrap(p, { log: false });

  Cypress.Commands.add(name("health"), () => wrap(mock.health()));
  Cypress.Commands.add(name("state"), () => wrap(mock.state()));
  Cypress.Commands.add(name("reset"), () => wrap(mock.reset()));
  Cypress.Commands.add(name("clear"), ((target: Target, action?: Directive["action"]) =>
    wrap(mock.clear(target, action))) as never);
  Cypress.Commands.add(name("scenario"), ((scenario: string | null) =>
    wrap(mock.scenario(scenario))) as never);
  Cypress.Commands.add(name("status"), ((target: Target, status: number) =>
    wrap(mock.status(target, status))) as never);
  Cypress.Commands.add(name("fail"), ((target: Target, status: number, o?: FailOptions) =>
    wrap(mock.fail(target, status, o))) as never);
  Cypress.Commands.add(name("delay"), ((target: Target, ms: number) =>
    wrap(mock.delay(target, ms))) as never);
  Cypress.Commands.add(name("pause"), ((target: Target) => wrap(mock.pause(target))) as never);
  Cypress.Commands.add(name("waitForRevision"), ((atLeast: number, o?: WaitOptions) =>
    wrap(mock.waitForRevision(atLeast, o))) as never);
}
