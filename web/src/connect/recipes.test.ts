// buildRecipes is plain data, so this pins the two things that would
// otherwise regress silently: every snippet is built from ws.url and
// config.reservedPrefix as given (never re-derived), and the apiBase recipe
// carries its own honest caveat rather than reading like the other three.
import { describe, expect, it } from "vitest";
import { buildRecipes } from "./recipes";
import { serverConfigFixture, workspaceFixture as workspace } from "@/test/fixtures";

const config = serverConfigFixture();

describe("buildRecipes", () => {
  it("builds four recipes, one per id", () => {
    const recipes = buildRecipes(workspace(), config);
    expect(recipes.map((r) => r.id)).toEqual(["env", "apiBase", "devtools", "curl"]);
  });

  it("uses ws.url as given, never rebuilding it from a slug", () => {
    const ws = workspace({ url: "http://alex.mock.corp.internal:9999" });
    const recipes = buildRecipes(ws, config);
    const env = recipes.find((r) => r.id === "env");
    expect(env?.snippet).toBe("API_BASE_URL=http://alex.mock.corp.internal:9999");
  });

  it('reads the reserved prefix from config, never a hard-coded "/__mocker"', () => {
    const recipes = buildRecipes(workspace(), config);
    const curl = recipes.find((r) => r.id === "curl");
    expect(curl?.snippet).toBe("curl http://alex.mock.corp.internal:8080/__test-prefix/health");
    expect(curl?.snippet).not.toContain("/__mocker");
  });

  it("marks the apiBase recipe as conditional on frontend support, unlike the others", () => {
    const recipes = buildRecipes(workspace(), config);
    const apiBase = recipes.find((r) => r.id === "apiBase");
    const env = recipes.find((r) => r.id === "env");
    expect(apiBase?.note).toContain("только если");
    expect(env?.note).not.toContain("только если");
  });

  it("carries the workspace's basePath on the API recipes and never on the health curl (A21, B1)", () => {
    const ws = workspace({ settings: { ...workspace().settings, basePath: "/api/v1" } });
    const recipes = buildRecipes(ws, config);
    expect(recipes.find((r) => r.id === "env")?.snippet).toBe(
      "API_BASE_URL=http://alex.mock.corp.internal:8080/api/v1",
    );
    expect(recipes.find((r) => r.id === "devtools")?.snippet).toBe(
      "http://alex.mock.corp.internal:8080/api/v1",
    );
    expect(recipes.find((r) => r.id === "curl")?.snippet).toBe(
      "curl http://alex.mock.corp.internal:8080/__test-prefix/health",
    );
  });
});
