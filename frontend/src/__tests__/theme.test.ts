import { beforeEach, describe, expect, it, vi } from "vitest";
import { readFileSync } from "node:fs";
import { resolve as resolvePath } from "node:path";

import {
  THEME_STORAGE_KEY,
  applyTheme,
  prefersDark,
  readStoredTheme,
  resolveTheme,
  storeTheme,
  themeTokens,
} from "../theme";

/**
 * The theme logic (growth item 20).
 *
 * Two things are worth noticing about how these are written.
 *
 * First, matchMedia and localStorage are MOCKED PER TEST rather than stubbed globally. A
 * theme that resolves through the OS is only testable if the OS is an input you control,
 * and a test that leaves the real one in place tests whatever the CI machine happens to
 * prefer.
 *
 * Second, the last test reads theme.scss. The token values are duplicated between the SCSS
 * and theme.ts -- necessarily, since the resolver needs them in JS without a
 * getComputedStyle round trip. Duplication that is only documented rots silently; the same
 * palette drifting in two files is exactly the kind of thing nobody notices until a page
 * is half-themed. So the duplication is asserted equal.
 */

function mockMatchMedia(dark: boolean, hasModernApi = true) {
  const listeners: Array<() => void> = [];
  const mql = {
    matches: dark,
    media: "(prefers-color-scheme: dark)",
    addEventListener: hasModernApi
      ? (_: string, cb: () => void) => {
          listeners.push(cb);
        }
      : undefined,
    removeEventListener: hasModernApi
      ? (_: string, cb: () => void) => {
          const i = listeners.indexOf(cb);
          if (i >= 0) listeners.splice(i, 1);
        }
      : undefined,
    addListener: hasModernApi
      ? undefined
      : (cb: () => void) => {
          listeners.push(cb);
        },
    removeListener: hasModernApi
      ? undefined
      : (cb: () => void) => {
          const i = listeners.indexOf(cb);
          if (i >= 0) listeners.splice(i, 1);
        },
  };
  Object.defineProperty(window, "matchMedia", {
    writable: true,
    configurable: true,
    value: () => mql,
  });
  return { mql, fire: () => listeners.forEach((cb) => cb()) };
}

describe("theme resolution", () => {
  beforeEach(() => {
    window.localStorage.clear();
    document.documentElement.removeAttribute("data-theme");
    document.documentElement.classList.remove("dark");
  });

  it("follows the OS for 'system'", () => {
    mockMatchMedia(true);
    expect(resolveTheme("system")).toBe("dark");

    mockMatchMedia(false);
    expect(resolveTheme("system")).toBe("light");
  });

  it("an explicit choice ignores the OS entirely", () => {
    // Both directions. A user who picked light must get light even on a machine set to
    // dark -- this is the assertion that fails if anyone "helpfully" re-adds a
    // prefers-color-scheme rule to the stylesheet.
    mockMatchMedia(true);
    expect(resolveTheme("light")).toBe("light");

    mockMatchMedia(false);
    expect(resolveTheme("dark")).toBe("dark");
  });

  it("defaults to dark when matchMedia is unavailable", () => {
    // Not light: dark is the application's historical appearance, so an unavailable
    // query must not flip the whole site.
    Object.defineProperty(window, "matchMedia", {
      writable: true,
      configurable: true,
      value: () => {
        throw new Error("not supported");
      },
    });
    expect(prefersDark()).toBe(true);
    expect(resolveTheme("system")).toBe("dark");
  });
});

describe("theme persistence", () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  it("round-trips a choice", () => {
    expect(readStoredTheme()).toBe("system");
    storeTheme("light");
    expect(readStoredTheme()).toBe("light");
    storeTheme("dark");
    expect(readStoredTheme()).toBe("dark");
  });

  it("falls back to system for a corrupt stored value", () => {
    // Anything unreadable must not be treated as a valid palette: `resolveTheme` would
    // return it verbatim and `applyTheme` would write an attribute matching no rule, so
    // the page would silently fall back to the CSS default while claiming a choice.
    window.localStorage.setItem(THEME_STORAGE_KEY, "chartreuse");
    expect(readStoredTheme()).toBe("system");
  });

  it("survives localStorage throwing", () => {
    const spy = vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked by privacy mode");
    });
    expect(readStoredTheme()).toBe("system");
    spy.mockRestore();
  });
});

describe("applyTheme", () => {
  beforeEach(() => {
    document.documentElement.removeAttribute("data-theme");
    document.documentElement.classList.remove("dark");
  });

  it("sets the attribute and the class together", () => {
    // Both, not either: the stylesheets select on `data-theme` and any consumer checking
    // `.dark` sees the class. If these could disagree, half the UI would theme and half
    // would not -- so they are set in one place and asserted together.
    applyTheme("light");
    expect(document.documentElement.getAttribute("data-theme")).toBe("light");
    expect(document.documentElement.classList.contains("dark")).toBe(false);

    applyTheme("dark");
    expect(document.documentElement.getAttribute("data-theme")).toBe("dark");
    expect(document.documentElement.classList.contains("dark")).toBe(true);
  });

  it("is idempotent", () => {
    applyTheme("dark");
    applyTheme("dark");
    expect(document.documentElement.getAttribute("data-theme")).toBe("dark");
    expect(document.documentElement.classList.contains("dark")).toBe(true);
  });
});

describe("token parity between theme.ts and theme.scss", () => {
  it("the JS tokens match the stylesheet", () => {
    // Resolved from process.cwd() rather than import.meta.url: under vitest's transform
    // `import.meta.url` is not a file: URL, so fileURLToPath throws "The URL must be of
    // scheme file". cwd IS the frontend directory, which is where vitest is always run
    // from, so this is stable -- and it is asserted below by finding the file.
    const scss = readFileSync(resolvePath("src/styles/theme.scss"), "utf8");

    // The two palettes, located by their selectors. Parsed out of the SCSS rather than
    // asserted as literals: hardcoding the expected hex in the test would just be a third
    // copy that can drift from both.
    const darkBlock = scss.slice(
      scss.indexOf(':root[data-theme="dark"]'),
      scss.indexOf(':root[data-theme="light"]'),
    );
    const lightBlock = scss.slice(
      scss.indexOf(':root[data-theme="light"]'),
      scss.indexOf("// `system` is resolved"),
    );

    const readTokens = (block: string) => {
      const out: Record<string, string> = {};
      for (const line of block.split("\n")) {
        const m = line.match(/^\s*(--sb-[a-z-]+):\s*(#[0-9a-f]{3,8})\s*;/i);
        if (m) out[m[1]] = m[2];
      }
      return out;
    };

    const scssDark = readTokens(darkBlock);
    const scssLight = readTokens(lightBlock);

    for (const theme of ["dark", "light"] as const) {
      const fromScss = theme === "dark" ? scssDark : scssLight;
      const fromTs = themeTokens[theme];
      for (const [name, value] of Object.entries(fromTs)) {
        expect(
          fromScss[name],
          `--${name} is ${value} in theme.ts but ${fromScss[name]} in theme.scss ` +
            `(${theme})`,
        ).toBe(value);
      }
    }
  });

  it("the dark palette is the pre-existing one, unchanged", () => {
    // The whole point of the refactor is that it is NOT a restyle. If someone "improves"
    // the dark tokens while touching this file, the default rendering changes and the
    // change is invisible in review because it looks like a tidy-up.
    expect(themeTokens.dark["--sb-bg"]).toBe("#202b33");
    expect(themeTokens.dark["--sb-surface"]).toBe("#30404d");
    expect(themeTokens.dark["--sb-text"]).toBe("#f5f8fa");
    expect(themeTokens.dark["--sb-text-muted"]).toBe("#bfccd6");
    expect(themeTokens.dark["--sb-border"]).toBe("#394b59");
  });

  it("both palettes have readable text against their own backgrounds", () => {
    // Relative luminance per WCAG, compared as a CONTRAST RATIO rather than an ordering.
    // The ordering check I first wrote assumed light-theme text is light, which is exactly
    // backwards -- the light palette has DARK text on a LIGHT background -- so it failed
    // immediately. A test that encodes the wrong direction of a colour relationship is
    // worse than no test: it fails once and then gets "fixed" by inverting the assertion
    // without anyone re-deriving what the numbers should be.
    //
    // The 4.5:1 threshold is the WCAG AA minimum for body text. It is asserted rather than
    // "some contrast is present", because a light palette nobody has ever rendered before
    // is exactly where unreadable text ships unnoticed.
    const luminance = (hex: string) => {
      const v = hex.replace("#", "");
      const [r, g, b] = [0, 2, 4].map((i) => parseInt(v.slice(i, i + 2), 16) / 255);
      const lin = (c: number) => (c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4);
      return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b);
    };
    const contrast = (a: string, b: string) => {
      const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
      return (hi + 0.05) / (lo + 0.05);
    };

    for (const theme of ["light", "dark"] as const) {
      const t = themeTokens[theme];
      expect(
        contrast(t["--sb-text"], t["--sb-bg"]),
        `${theme}: body text on the page background must reach 4.5:1`,
      ).toBeGreaterThanOrEqual(4.5);
      expect(
        contrast(t["--sb-text"], t["--sb-surface"]),
        `${theme}: body text on a card surface must reach 4.5:1`,
      ).toBeGreaterThanOrEqual(4.5);
      expect(
        contrast(t["--sb-text-muted"], t["--sb-bg"]),
        `${theme}: muted text on the page background must reach 3:1`,
      ).toBeGreaterThanOrEqual(3);
    }
  });
});