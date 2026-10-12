import { describe, expect, it } from "vitest";
import { isRedirect } from "@tanstack/react-router";
import { authResponseFixture, serverConfigFixture } from "@/test/fixtures";
import { requireBackendWorkbench } from "./backendWorkbenchGate";

describe("requireBackendWorkbench", () => {
  it("sends a frozen session to the workspace list", () => {
    let thrown: unknown;
    try {
      requireBackendWorkbench(authResponseFixture());
    } catch (error) {
      thrown = error;
    }
    expect(isRedirect(thrown)).toBe(true);
  });

  it("lets an enabled session through", () => {
    expect(() =>
      requireBackendWorkbench(
        authResponseFixture({ config: serverConfigFixture({ backendWorkbench: true }) }),
      ),
    ).not.toThrow();
  });
});
