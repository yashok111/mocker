import { expect, it } from "vitest";
import { resourceLayoutInput } from "./layout";
import type { Model } from "./model";

it("reserves operation rows and full labels while excluding saved coordinates and dangling relations", () => {
  const model: Model = {
    resources: [
      {
        id: "a",
        name: "A",
        service: "",
        description: "",
        operationKeys: ["1", "2", "3", "4", "5"],
        x: 40,
        y: 40,
        inferred: true,
      },
      {
        id: "b",
        name: "B",
        service: "",
        description: "",
        operationKeys: [],
        x: 700,
        y: 400,
        inferred: false,
      },
    ],
    operations: ["1", "2", "3", "4", "5"].map((key) => ({
      key,
      method: "get",
      path: "/",
      summary: "",
      schemas: [],
    })),
    relations: [
      { id: "link", fromResourceId: "a", toResourceId: "b", label: "Зависит от" },
      { id: "missing", fromResourceId: "a", toResourceId: "absent", label: "" },
    ],
    diagnostics: [],
  };
  const input = resourceLayoutInput(model);
  expect(input.nodes).toEqual([
    { id: "resource:a", width: 220, height: 196 },
    { id: "resource:b", width: 220, height: 100 },
  ]);
  expect(input.edges).toHaveLength(1);
  expect(input.edges[0]).toMatchObject({
    id: "relation:link",
    source: "resource:a",
    target: "resource:b",
    label: { text: "Зависит от" },
  });
  model.resources[0]!.x = -500;
  expect(resourceLayoutInput(model)).toEqual(input);
});
