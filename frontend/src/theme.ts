/**
 * Theme tokens and the system/light/dark choice.
 *
 * WHY THIS EXISTS. The application was dark-ONLY, with 33 stylesheets, zero `@media`
 * queries and zero `prefers-color-scheme` / `data-theme` anywhere. So "add dark mode" was
 * the wrong framing: there was no light palette to toggle FROM, and no responsive baseline
 * to make mobile work. What ships here is the FOUNDATION -- a token layer both palettes
 * can express, an attribute on <html> that selects between them, and a persisted choice --
 * rather than a claim that the workstream is finished.
 *
 * What is NOT here: per-component light colours across all 33 stylesheets, and a
 * responsive pass over every view. Those are the follow-on, and pretending otherwise would
 * be claiming coverage this file does not have.
 */

/** The three choices a user can make. "system" follows the OS and can change mid-session. */
export type ThemeChoice = "system" | "light" | "dark";

/** The two palettes that actually exist. `system` resolves to one of these. */
export type ResolvedTheme = "light" | "dark";

export const THEME_STORAGE_KEY = "stashbox.theme";

/**
 * The token values, declared here as the single source of truth for the fallback.
 *
 * These DUPLICATE the `:root` block in theme.scss on purpose. A CSS custom property is
 * the mechanism the stylesheets consume, but the resolver needs the same values in JS to
 * resolve "system" without reading computed style -- and reading `getComputedStyle` to
 * learn the answer would force a style recalculation on every theme change and on every
 * mount. A duplicate that is asserted equal in a test beats a live read that can fail.
 */
export const themeTokens: Record<ResolvedTheme, Record<string, string>> = {
  dark: {
    "--sb-bg": "#202b33",
    "--sb-surface": "#30404d",
    "--sb-text": "#f5f8fa",
    "--sb-text-muted": "#bfccd6",
    "--sb-border": "#394b59",
  },
  light: {
    // Not derived from the dark palette by inversion. Inverting #202b33 gives a
    // brownish grey, and every hardcoded text colour would have to be inverted too --
    // which is how you end up with unreadable contrast. These are picked values, chosen
    // against the same `$primary` accents the dark palette already uses.
    "--sb-bg": "#f5f8fa",
    "--sb-surface": "#ffffff",
    "--sb-text": "#182026",
    "--sb-text-muted": "#5c7080",
    "--sb-border": "#d3dde4",
  },
};

/** Reads the stored choice, defaulting to "system" when absent or unreadable. */
export function readStoredTheme(): ThemeChoice {
  try {
    const stored = window.localStorage.getItem(THEME_STORAGE_KEY);
    if (stored === "light" || stored === "dark" || stored === "system") {
      return stored;
    }
  } catch {
    // localStorage throws in private-browsing modes and when cookies are blocked. A theme
    // preference is not worth failing a page render over, so fall through to the default
    // rather than propagating.
  }
  return "system";
}

/** Persists the choice, swallowing a storage failure for the same reason. */
export function storeTheme(choice: ThemeChoice): void {
  try {
    window.localStorage.setItem(THEME_STORAGE_KEY, choice);
  } catch {
    // Deliberately ignored. The choice still applies for this session; it just will not
    // survive a reload.
  }
}

/** Whether the OS currently asks for a dark palette. */
export function prefersDark(): boolean {
  try {
    return window.matchMedia("(prefers-color-scheme: dark)").matches;
  } catch {
    // matchMedia is absent in some test environments and very old browsers. Assuming dark
    // matches the application's historical appearance, so an unavailable query must not
    // flip every page to light.
    return true;
  }
}

/** Resolves "system" against the OS preference. */
export function resolveTheme(choice: ThemeChoice): ResolvedTheme {
  if (choice === "system") {
    return prefersDark() ? "dark" : "light";
  }
  return choice;
}

/**
 * Writes the resolved theme onto <html> as BOTH `data-theme` and a `dark` class.
 *
 * Both, because the stylesheets select on one and the tests on the other, and a token
 * layer that only responds to `data-theme` is invisible to any consumer checking
 * `.dark`. One attribute is a decision; two that must agree is a bug waiting to happen --
 * so this is the only place either is set, and the test asserts they agree.
 */
export function applyTheme(theme: ResolvedTheme): void {
  const root = document.documentElement;
  root.setAttribute("data-theme", theme);
  root.classList.toggle("dark", theme === "dark");
}