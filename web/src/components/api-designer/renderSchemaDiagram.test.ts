import { expect, it, vi } from "vitest";
import { renderSchemaDiagram } from "./renderSchemaDiagram";

// Mermaid needs browser text measurements. Supply its SVG boundary output so
// the test exercises our label normalization and XML serialization themselves.
vi.mock("mermaid", () => ({
  default: {
    initialize: () => {},
    render: async () => ({
      svg: '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 200 100"><text>A/B&amp;#126;C<tspan>&amp;#60;img onerror=&amp;#34;bad&amp;#34;&amp;#62;</tspan><tspan>control&amp;#1;surrogate&amp;#55296;</tspan></text></svg>',
    }),
  },
}));

it("decodes visible labels as text while preserving XML-invalid code points as escaped text", async () => {
  const before = document.body.childElementCount;
  const result = await renderSchemaDiagram("classDiagram\nclass s0");
  const parsed = new DOMParser().parseFromString(result, "image/svg+xml");
  expect(parsed.querySelector("parsererror")).toBeNull();
  expect(parsed.querySelector("text")?.textContent).toBe(
    'A/B~C<img onerror="bad">control&#1;surrogate&#55296;',
  );
  expect(parsed.querySelector("img, script, [onerror]")).toBeNull();
  expect(document.body.childElementCount).toBe(before);
});
