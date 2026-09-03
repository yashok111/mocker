import { createTheme } from "@mantine/core";

// One theme object for the whole app. Kept deliberately small: this is an
// internal tool, and every knob set here is a knob that has to be justified
// later when a screen wants something else.
export const theme = createTheme({
  // slate/blue, matching nothing in particular — it just has to be legible
  // and calm for a panel someone leaves open on a second monitor all day.
  primaryColor: "blue",
  defaultRadius: "md",
  fontFamilyMonospace:
    "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', monospace",
  headings: {
    sizes: {
      h1: { fontSize: "1.5rem", fontWeight: "600" },
      h2: { fontSize: "1.125rem", fontWeight: "600" },
    },
  },
});
