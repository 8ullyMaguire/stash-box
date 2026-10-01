import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

afterEach(() => {
  cleanup();
});

if (typeof window !== "undefined") {
  // utils/user.ts reads localStorage at module scope, and that module is
  // imported by nearly every page via src/utils. jsdom only provides
  // localStorage when a test opts into it per-environment, so without this the
  // import throws "Cannot read properties of undefined (reading 'getItem')"
  // and every test file that transitively imports src/utils fails to load.
  if (!window.localStorage) {
    const store = new Map<string, string>();
    Object.defineProperty(window, "localStorage", {
      writable: true,
      value: {
        getItem: (key: string) => store.get(key) ?? null,
        setItem: (key: string, value: string) => void store.set(key, value),
        removeItem: (key: string) => void store.delete(key),
        clear: () => store.clear(),
        key: (index: number) => [...store.keys()][index] ?? null,
        get length() {
          return store.size;
        },
      },
    });
  }

  if (!window.matchMedia) {
    Object.defineProperty(window, "matchMedia", {
      writable: true,
      value: (query: string) => ({
        matches: false,
        media: query,
        onchange: null,
        addListener: () => {},
        removeListener: () => {},
        addEventListener: () => {},
        removeEventListener: () => {},
        dispatchEvent: () => false,
      }),
    });
  }

  if (!window.ResizeObserver) {
    window.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    } as unknown as typeof ResizeObserver;
  }

  if (!window.IntersectionObserver) {
    window.IntersectionObserver = class {
      readonly root = null;
      readonly rootMargin = "";
      readonly thresholds = [];
      observe() {}
      unobserve() {}
      disconnect() {}
      takeRecords() {
        return [];
      }
    } as unknown as typeof IntersectionObserver;
  }

  if (!window.scrollTo) {
    window.scrollTo = (() => {}) as typeof window.scrollTo;
  }

  if (!Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = () => {};
  }
}

// jsdom has neither of these, and the crop step needs both: an object URL to
// show the picture, and a decode to learn its size. Stubbed here rather than
// per test, since anything rendering a file preview wants them
if (typeof window !== "undefined") {
  if (!window.URL.createObjectURL) {
    window.URL.createObjectURL = () => "blob:stub";
    window.URL.revokeObjectURL = () => {};
  }

  if (!window.createImageBitmap) {
    // 200x300 so a test can tell a portrait frame from a landscape one
    window.createImageBitmap = (() =>
      Promise.resolve({
        width: 200,
        height: 300,
        close: () => {},
      })) as unknown as typeof createImageBitmap;
  }
}

// jsdom implements neither, and anything dragged with a pointer needs both:
// capture so the drag survives leaving the element, and a box to measure the drag against
if (typeof window !== "undefined") {
  for (const method of [
    "setPointerCapture",
    "releasePointerCapture",
    "hasPointerCapture",
  ] as const) {
    if (!Element.prototype[method]) {
      Object.defineProperty(Element.prototype, method, {
        value: () => false,
        writable: true,
      });
    }
  }
}
