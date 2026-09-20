import type { Mermaid } from "mermaid";

let renderer: Promise<Mermaid> | undefined;
let sequence = 0;

function loadRenderer(): Promise<Mermaid> {
  renderer ??= import("mermaid")
    .then(({ default: mermaid }) => {
      mermaid.initialize({
        startOnLoad: false,
        securityLevel: "strict",
        suppressErrorRendering: true,
        htmlLabels: false,
        theme: "base",
        look: "classic",
        layout: "dagre",
        fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace",
        themeVariables: {
          fontSize: "13px",
          primaryColor: "#edf6f3",
          primaryTextColor: "#25332f",
          primaryBorderColor: "#8aa89d",
          lineColor: "#69756e",
          secondaryColor: "#f5f6f4",
          tertiaryColor: "#ffffff",
        },
        class: { hideEmptyMembersBox: true },
      });
      return mermaid;
    })
    .catch((error: unknown) => {
      renderer = undefined;
      throw error;
    });
  return renderer;
}

export async function renderSchemaDiagram(source: string): Promise<string> {
  const mermaid = await loadRenderer();
  // SVG layout needs a mounted element for text measurement. Isolate Mermaid's
  // temporary/error DOM so a rejected render cannot leave nodes in the page.
  const container = document.createElement("div");
  container.style.cssText = "position:absolute;left:-100000px;top:0;visibility:hidden";
  container.setAttribute("aria-hidden", "true");
  document.body.appendChild(container);
  try {
    const { svg } = await mermaid.render(`schema-diagram-${++sequence}`, source, container);
    const parsed = new DOMParser().parseFromString(svg, "image/svg+xml");
    // With htmlLabels disabled Mermaid double-escapes numeric entities in SVG
    // labels. Decode text nodes only; XMLSerializer still escapes markup, so a
    // property named <img ...> remains text in both the preview and the export.
    for (const element of parsed.querySelectorAll("text, tspan")) {
      for (const child of element.childNodes) {
        if (child.nodeType !== Node.TEXT_NODE || child.nodeValue === null) continue;
        child.nodeValue = child.nodeValue.replace(/&#(\d+);/g, (encoded, digits: string) => {
          const code = Number(digits);
          const validXmlCharacter =
            code === 9 ||
            code === 10 ||
            code === 13 ||
            (code >= 0x20 && code <= 0xd7ff) ||
            (code >= 0xe000 && code <= 0xfffd) ||
            (code >= 0x10000 && code <= 0x10ffff);
          return validXmlCharacter ? String.fromCodePoint(code) : encoded;
        });
      }
    }
    return new XMLSerializer().serializeToString(parsed.documentElement);
  } finally {
    container.remove();
  }
}
