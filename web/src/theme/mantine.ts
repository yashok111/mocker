import { createTheme } from "@mantine/core";

// Keep everyday controls consistent across the independently composed screens.
// Semantic colors (HTTP methods, warnings, errors) remain distinct from the
// brand accent, so the operator can still scan status without reading a label.
export const theme = createTheme({
  primaryColor: "teal",
  primaryShade: 8,
  defaultRadius: "md",
  colors: {
    teal: [
      "#edf8f4",
      "#d4f0e5",
      "#a9dfcd",
      "#7bcdb4",
      "#50b89e",
      "#31a58b",
      "#18937d",
      "#0c8975",
      "#087f70",
      "#07665b",
    ],
    gray: [
      "#f7f8f7",
      "#eff2ef",
      "#e1e6e2",
      "#cdd5cf",
      "#a9b5ad",
      "#849389",
      "#65746b",
      "#4f5e55",
      "#35463b",
      "#25332f",
    ],
  },
  fontFamily: "Inter, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif",
  fontFamilyMonospace:
    "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', monospace",
  fontSizes: { xs: "0.75rem", sm: "0.8125rem", md: "0.875rem", lg: "1rem", xl: "1.125rem" },
  radius: { xs: "0.25rem", sm: "0.375rem", md: "0.5rem", lg: "0.75rem", xl: "1rem" },
  headings: {
    fontWeight: "650",
    sizes: {
      h1: { fontSize: "1.75rem", lineHeight: "1.25", fontWeight: "650" },
      h2: { fontSize: "1.25rem", lineHeight: "1.35", fontWeight: "650" },
      h3: { fontSize: "1rem", lineHeight: "1.4", fontWeight: "600" },
    },
  },
  components: {
    Button: { defaultProps: { size: "sm", fw: 550 } },
    ActionIcon: { defaultProps: { size: "md", variant: "subtle" } },
    Badge: { defaultProps: { radius: "sm", variant: "light", fw: 600 } },
    Card: { defaultProps: { padding: "lg", radius: "lg" } },
    Paper: { defaultProps: { radius: "lg" } },
    Input: { defaultProps: { size: "sm" } },
    Table: { defaultProps: { verticalSpacing: "sm", horizontalSpacing: "md" } },
    Tooltip: { defaultProps: { withArrow: true, multiline: true, maw: 320 } },
    Modal: { defaultProps: { radius: "lg", padding: "xl", centered: true } },
    Drawer: { defaultProps: { padding: "xl" } },
  },
});
