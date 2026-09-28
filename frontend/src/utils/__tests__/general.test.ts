import { describe, expect, it } from "vitest";
import { proposedOrCurrent } from "../general";

describe("proposedOrCurrent", () => {
  it("keeps an explicit null, which is a deletion in an edit", () => {
    expect(proposedOrCurrent<string>(null, "actress")).toBeNull();
  });

  it("keeps an explicit empty string", () => {
    expect(proposedOrCurrent<string>("", "actress")).toBe("");
  });

  it("keeps an explicit zero", () => {
    expect(proposedOrCurrent<number>(0, 170)).toBe(0);
  });

  it("falls back to the current value when the edit omits the field", () => {
    expect(proposedOrCurrent<string>(undefined, "actress")).toBe("actress");
  });

  it("returns undefined when neither side has a value", () => {
    expect(proposedOrCurrent<string>(undefined, undefined)).toBeUndefined();
  });

  it("does not treat a false proposal as absent", () => {
    expect(proposedOrCurrent<boolean>(false, true)).toBe(false);
  });

  // The bug this exists for: `??` and `||` both fall through on null, which
  // restores a value the contributor deleted.
  it("differs from ?? and || exactly on null", () => {
    const proposed = null;
    const current = "actress";
    expect(proposed ?? current).toBe("actress");
    expect(proposed || current).toBe("actress");
    expect(proposedOrCurrent(proposed, current)).toBeNull();
  });
});
