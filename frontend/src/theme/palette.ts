/* estate-tokens:brand-file -- this file IS stash-box's palette: the light and dark token values, and the
   categorical cluster palette. Every literal colour the front end declares lives here, which is what the
   adoption audit asks for: identity in one auditable place, drift counted everywhere else.

   WHY THE TOKEN VALUES EXIST IN TS AT ALL. A CSS custom property is what the stylesheets consume, but the
   theme resolver has to answer "system" without reading computed style -- reading `getComputedStyle` to
   learn the answer would force a style recalculation on every mount and every change. So the values are
   duplicated between `src/styles/theme.scss` and here, and `__tests__/theme.test.ts` asserts the two copies
   are equal. Splitting the values out of `theme.ts` keeps the machinery (resolve / apply / store) separate
   from the palette; it does not weaken the equality test, which still reads both sides.

   Changing a value here without changing theme.scss makes that test fail, which is the point. */

/** The two palettes that actually exist. `system` resolves to one of these. */
export type ResolvedTheme = "light" | "dark";

/**
 * The token values, the single source of truth for the JS fallback.
 *
 * Dark is the pre-existing palette and is asserted unchanged in the test: the token refactor was not a
 * restyle, and "improving" a dark value while touching this file would change the default rendering in a
 * way that looks like a tidy-up in review.
 */
export const themeTokens: Record<ResolvedTheme, Record<string, string>> = {
  dark: {
    "--sb-bg": "#202b33",
    "--sb-surface": "#30404d",
    "--sb-text": "#f5f8fa",
    "--sb-text-muted": "#bfccd6",
    "--sb-code": "#f0a6c8",
    "--sb-border": "#394b59",
    "--sb-link": "#6cc4f2",
    "--sb-danger-text": "#ff9e9e",
  },
  light: {
    // Not derived from the dark palette by inversion. Inverting #202b33 gives a
    // brownish grey, and every hardcoded text colour would have to be inverted too --
    // which is how you end up with unreadable contrast. These are picked values, chosen
    // against the same `$primary` accents the dark palette already uses.
    "--sb-bg": "#f5f8fa",
    "--sb-surface": "#ffffff",
    "--sb-text": "#182026",
    "--sb-text-muted": "#566877",
    "--sb-code": "#a3236b",
    "--sb-border": "#d3dde4",
    "--sb-link": "#0b4a6b",
    "--sb-danger-text": "#7d1616",
  },
};

/**
 * Tableau-10, for the fingerprint-cluster graph. Categorical, not chrome: these hues exist to separate
 * scenes from each other, so they are not theme tokens and do not flip with the palette. Kept here so the
 * graph's hook has no literals of its own.
 */
export const CLUSTER_PALETTE = [
  "#4e79a7",
  "#f28e2b",
  "#e15759",
  "#76b7b2",
  "#59a14f",
  "#edc948",
  "#b07aa1",
  "#ff9da7",
  "#9c755f",
  "#bab0ac",
] as const;
