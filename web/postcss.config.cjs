// Mantine is styled with PostCSS, not utility classes: postcss-preset-mantine
// provides the light-dark()/rem() functions and the mixins its own stylesheets
// use, and postcss-simple-vars supplies the breakpoint variables those mixins
// reference by name. Without this file Mantine's CSS ships with unresolved
// functions and the layout silently degrades — nothing errors, it just looks
// wrong.
module.exports = {
  plugins: {
    "postcss-preset-mantine": {},
    "postcss-simple-vars": {
      variables: {
        "mantine-breakpoint-xs": "36em",
        "mantine-breakpoint-sm": "48em",
        "mantine-breakpoint-md": "62em",
        "mantine-breakpoint-lg": "75em",
        "mantine-breakpoint-xl": "88em",
      },
    },
  },
};
