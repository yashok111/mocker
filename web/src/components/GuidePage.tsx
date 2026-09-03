import { useMemo } from "react";
import { Anchor, Box, Grid, List, Paper, Text, Title, Typography } from "@mantine/core";
import { Marked, type Tokens } from "marked";
import guideSource from "../../../docs/USER-GUIDE.md?raw";

// GuidePage renders docs/USER-GUIDE.md — the operator's manual, in the
// product's own language — inside the admin shell at /guide. The markdown
// is compiled INTO the bundle (`?raw`), not fetched: a guide route on the
// admin API would be a route the contract, the coverage test and the MCP
// allowlist all have to learn, for a text that changes only with a build.
// The same reasoning keeps the file under docs/ rather than under web/:
// it is read on the forge as often as here, and Vite reaches it through
// server.fs.allow (vite.config.ts).
//
// marked has no sanitiser and needs none here: the only HTML that reaches
// dangerouslySetInnerHTML is rendered from a file this repository ships,
// never from a request, and the panel's CSP (internal/webui) would refuse
// an inline script anyway.

/** slugify makes a heading id the way a forge would: letters and digits of any script, hyphens between. */
function slugify(text: string): string {
  return text
    .toLowerCase()
    .replace(/[^\p{L}\p{N}]+/gu, "-")
    .replace(/^-+|-+$/g, "");
}

const md = new Marked({
  gfm: true,
  renderer: {
    // Headings get ids so the table of contents can point at them; marked
    // stopped generating ids on its own in v8.
    heading({ tokens, depth, text }: Tokens.Heading): string {
      const inner = this.parser.parseInline(tokens);
      return `<h${depth} id="${slugify(text)}">${inner}</h${depth}>\n`;
    },
  },
});

type TocEntry = { id: string; text: string };

function tableOfContents(source: string): TocEntry[] {
  return md
    .lexer(source)
    .filter((t): t is Tokens.Heading => t.type === "heading" && t.depth === 2)
    .map((t) => ({ id: slugify(t.text), text: t.text }));
}

export function GuidePage() {
  const { html, toc } = useMemo(
    () => ({ html: md.parse(guideSource, { async: false }), toc: tableOfContents(guideSource) }),
    [],
  );

  return (
    <Box data-testid="guide-page">
      <Grid gap="xl">
        <Grid.Col span={{ base: 12, md: 3 }}>
          <Paper withBorder p="md" pos="sticky" top={72}>
            <Title order={5} mb="xs">
              Содержание
            </Title>
            <List spacing={4} size="sm" listStyleType="none" data-testid="guide-toc">
              {toc.map((entry) => (
                <List.Item key={entry.id}>
                  <Anchor href={`#${entry.id}`} size="sm">
                    {entry.text}
                  </Anchor>
                </List.Item>
              ))}
            </List>
            <Text size="xs" c="dimmed" mt="md">
              Для агентов: инструмент MCP <code>get_guide</code>.
            </Text>
          </Paper>
        </Grid.Col>
        <Grid.Col span={{ base: 12, md: 9 }}>
          <Typography>
            <div data-testid="guide-body" dangerouslySetInnerHTML={{ __html: html }} />
          </Typography>
        </Grid.Col>
      </Grid>
    </Box>
  );
}
