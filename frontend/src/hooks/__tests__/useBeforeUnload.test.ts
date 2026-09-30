import { renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useBeforeUnload } from "../useBeforeUnload";

// The bug this exists for, and it is worth stating precisely because the fix
// looked like a no-op.
//
// The original code was:
//
//     useEffect(() => {
//       window.addEventListener("beforeunload", unloadListener);
//       return () => window.removeEventListener("beforeunload", unloadListener);
//     }, []);
//     return () => window.removeEventListener("beforeunload", unloadListener);
//
// That trailing `return` was in the FUNCTION BODY, not inside the effect. It ran
// on every render, and its value -- a cleanup function -- was discarded because a
// hook's return value is not a cleanup. So nothing was ever removed: every mount
// of PerformerForm, SceneForm, StudioForm or TagForm added one more listener
// that nothing would ever take off.
//
// The consequence is not "a warning that fires once". It is that after N
// form-lifecycle events the browser sees N beforeunload listeners, the user is
// nagged on every navigation whether or not there are unsaved changes, and
// memory grows by one closure per mount for the life of the tab.
//
// These tests assert on the LISTENER COUNT, because that is the observable
// difference between the two versions. Asserting "the warning does not appear
// after submitting" would be a weaker and much slower test for the same fact.
describe("useBeforeUnload", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("registers exactly one beforeunload listener on mount", () => {
    const addSpy = vi.spyOn(window, "addEventListener");
    const removeSpy = vi.spyOn(window, "removeEventListener");

    renderHook(() => useBeforeUnload());

    const added = addSpy.mock.calls.filter((c) => c[0] === "beforeunload");
    expect(added).toHaveLength(1);
    expect(removeSpy.mock.calls.filter((c) => c[0] === "beforeunload")).toHaveLength(0);
  });

  // The regression guard. On the buggy version this fails with 1 !== 0, because
  // the `return` was outside the effect and never ran.
  it("removes its listener when the component unmounts", () => {
    const addSpy = vi.spyOn(window, "addEventListener");
    const removeSpy = vi.spyOn(window, "removeEventListener");

    const { unmount } = renderHook(() => useBeforeUnload());
    const listener = addSpy.mock.calls.find((c) => c[0] === "beforeunload")?.[1];

    unmount();

    const removed = removeSpy.mock.calls.filter((c) => c[0] === "beforeunload");
    expect(removed).toHaveLength(1);
    // The exact same function reference, not merely "some listener". Removing a
    // different function would leave the real one attached while this test went
    // green, which is the failure mode a count-only assertion cannot see.
    expect(removed[0][1]).toBe(listener);
  });

  // The accumulating case, which is the bug's actual symptom rather than its
  // mechanism. Each mount/unmount cycle must be net zero.
  it("does not accumulate listeners across mount/unmount cycles", () => {
    const addSpy = vi.spyOn(window, "addEventListener");
    const removeSpy = vi.spyOn(window, "removeEventListener");

    for (let i = 0; i < 3; i++) {
      const { unmount } = renderHook(() => useBeforeUnload());
      unmount();
    }

    const added = addSpy.mock.calls.filter((c) => c[0] === "beforeunload").length;
    const removed = removeSpy.mock.calls.filter((c) => c[0] === "beforeunload").length;

    expect(added).toBe(removed);
    expect(added).toBe(3);
  });

  // A negative control, so the suite cannot pass by matching zero calls. Without
  // it, a typo in the event name -- "beforeUnload" instead of "beforeunload" --
  // would make every assertion above pass vacuously.
  it("uses the real browser event name", () => {
    const addSpy = vi.spyOn(window, "addEventListener");
    renderHook(() => useBeforeUnload());

    const events = addSpy.mock.calls.map((c) => c[0]);
    expect(events).toContain("beforeunload");
    expect(events).not.toContain("beforeUnload");
  });
});