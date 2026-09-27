import { readFileSync } from "node:fs";
import mermaid from "mermaid";
import { expect, it } from "vitest";

it("parses the backend fixture without consuming author labels as Mermaid instructions", async () => {
  const text = readFileSync("../internal/scenarioexport/testdata/sequence.mmd", "utf8");
  mermaid.initialize({ startOnLoad: false, securityLevel: "strict" });
  await expect(mermaid.parse(text)).resolves.toMatchObject({ diagramType: "sequence" });
  const diagram = await mermaid.mermaidAPI.getDiagramFromText(text);
  const db = diagram.db as unknown as {
    getActors(): Map<string, { description: string }>;
    getMessages(): Array<{ from?: string; to?: string; message: string }>;
  };
  const decode = (text: string) =>
    text.replace(/ﬂ°°(\d+)¶ß/g, (_, code: string) => String.fromCodePoint(Number(code)));
  expect(decode(db.getActors().get("p0")!.description)).toBe("wrap:Клиент");
  expect(decode(db.getActors().get("p1")!.description)).toBe("nowrap:API");
  expect(db.getMessages().map((m) => ({ ...m, message: decode(m.message) }))).toEqual(
    expect.arrayContaining([
      expect.objectContaining({ from: "p0", to: "p1", message: "wrap:GET /status" }),
      expect.objectContaining({ from: "p1", to: "p0", message: "200 OK" }),
      expect.objectContaining({ message: "nowrap:note" }),
    ]),
  );
});
