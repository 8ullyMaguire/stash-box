import React, {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from "react";

import {
  ResolvedTheme,
  ThemeChoice,
  applyTheme,
  readStoredTheme,
  resolveTheme,
  storeTheme,
} from "../theme";

/**
 * The theme context (growth item 20 -- foundation).
 *
 * A Context rather than a bare module-level variable because the choice has to reach
 * components AND because "system" needs to re-render when the OS preference changes while
 * the app is open. A module singleton can hold the value but cannot notify anyone.
 *
 * The attribute is applied in an EFFECT, not during render. Writing to the DOM during
 * render is a side effect, and under StrictMode's double-invoke it happens twice, which
 * is how a value that should be idempotent becomes a source of surprises.
 */

interface ThemeContextValue {
  /** What the user chose, including "system". */
  choice: ThemeChoice;
  /** What "choice" resolves to right now. What the stylesheets are actually showing. */
  resolved: ResolvedTheme;
  setChoice: (choice: ThemeChoice) => void;
  /**
   * Step through system -> light -> dark.
   *
   * A cycle rather than a toggle because "system" is a real option that must be
   * reachable, and a two-state toggle can never select it once set.
   */
  cycle: () => void;
}

const ThemeContext = createContext<ThemeContextValue>({
  choice: "system",
  resolved: "dark",
  setChoice: () => {},
  cycle: () => {},
});

const CYCLE: ThemeChoice[] = ["system", "light", "dark"];

export const ThemeProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  // The stored choice is read LAZILY, once, via the useState initialiser. Reading it on
  // every render would be a localStorage hit per render, and would clobber the user's
  // in-session choice with the persisted one on every keystroke elsewhere in the app.
  const [choice, setChoiceState] = useState<ThemeChoice>(readStoredTheme);
  const [resolved, setResolved] = useState<ResolvedTheme>(() => resolveTheme(choice));

  // Track the OS preference so "system" is live rather than sampled once at startup.
  useEffect(() => {
    let mql: MediaQueryList;
    try {
      mql = window.matchMedia("(prefers-color-scheme: dark)");
    } catch {
      return;
    }

    // The modern API. `addEventListener` is absent in Safari < 14, where the deprecated
    // `addListener` is the only option -- so support both rather than silently doing
    // nothing on an older browser.
    const onChange = () => {
      // Only recompute if the user is actually following the system. If they picked an
      // explicit palette, an OS change must not override them.
      setChoiceState((current) => {
        setResolved(resolveTheme(current));
        return current;
      });
    };

    if (mql.addEventListener) {
      mql.addEventListener("change", onChange);
      return () => mql.removeEventListener("change", onChange);
    }
    mql.addListener(onChange);
    return () => mql.removeListener(onChange);
  }, []);

  // Single place the DOM is written. Both the attribute and the class, together.
  useEffect(() => {
    applyTheme(resolved);
  }, [resolved]);

  const setChoice = useCallback((next: ThemeChoice) => {
    setChoiceState(next);
    setResolved(resolveTheme(next));
    storeTheme(next);
  }, []);

  const cycle = useCallback(() => {
    setChoiceState((current) => {
      const idx = CYCLE.indexOf(current);
      const next = CYCLE[(idx + 1) % CYCLE.length];
      setResolved(resolveTheme(next));
      storeTheme(next);
      return next;
    });
  }, []);

  const value = useMemo(
    () => ({ choice, resolved, setChoice, cycle }),
    [choice, resolved, setChoice, cycle],
  );

  return (
    <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>
  );
};

export function useTheme(): ThemeContextValue {
  return useContext(ThemeContext);
}