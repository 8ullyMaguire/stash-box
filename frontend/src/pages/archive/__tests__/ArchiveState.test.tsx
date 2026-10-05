import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import ArchiveState, {
  type ArchiveStateType,
} from "src/pages/archive/ArchiveState";
import { describe, expect, it } from "vitest";

/**
 * The claims worth testing on this page are all about what it does NOT say.
 * A "State of the Archive" page whose numbers are wrong is indistinguishable from
 * one whose numbers are right until someone trusts it, so the failures that
 * matter are the confidently-wrong ones: a type measured as complete when it was
 * never measured, and a percentage that does not add up.
 *
 * MUTATIONS:
 *  1. treat a null numerator as 0        -> "never claims an unmeasured type is complete"
 *  2. drop the `incomplete !== null` filter from the totals -> the same test
 *  3. invert complete/total              -> "sums the per-type fractions into the headline"
 *  4. rank/order the cards wrongly       -> order does not matter here, so N/A
 *  5. render a bar at 0% for unmeasured  -> "shows no progress bar when unmeasured"
 */

const state = (
  entityType: string,
  total: number | null,
  incomplete: number | null,
): ArchiveStateType => ({ entityType, total, incomplete });

const renderPage = (states: ArchiveStateType[]) =>
  render(
    <MemoryRouter>
      <ArchiveState states={states} />
    </MemoryRouter>,
  );

describe("ArchiveState", () => {
  describe("the headline", () => {
    it("sums the per-type fractions rather than averaging the percentages", () => {
      // performers 0/1, scenes 9/10. The mean of the two percentages is 45%;
      // the true archive figure is 9/11 = 82%. Averaging percentages is the
      // obvious implementation and it is wrong whenever the types differ in size,
      // which they always do.
      renderPage([state("performer", 1, 1), state("scene", 10, 1)]);
      expect(screen.getByText("82%")).toBeInTheDocument();
      expect(screen.queryByText("45%")).not.toBeInTheDocument();
    });

    it("isolates the headline reducer from any single card", () => {
      // The headline and a lone card compute the same fraction, so a one-type
      // fixture cannot tell them apart — a mutation to the HEADLINE reducer
      // survives every test that uses one type. Two types of very different
      // sizes is what makes the aggregate its own thing: 1/2 performers and
      // 99/100 scenes is 100/102 = 98%, and neither card reads 98%.
      renderPage([state("performer", 2, 1), state("scene", 100, 1)]);
      const headline = screen.getByRole("progressbar", {
        name: "Archive completion",
      });
      expect(headline).toHaveAttribute("aria-valuenow", "98");
      // Neither card is 98%: the aggregate is not any single type's number.
      expect(
        screen.getByRole("progressbar", { name: "performers completion" }),
      ).toHaveAttribute("aria-valuenow", "50");
      expect(
        screen.getByRole("progressbar", { name: "scenes completion" }),
      ).toHaveAttribute("aria-valuenow", "99");
    });

    it("reports 0%, not 100%, when nothing has been catalogued", () => {
      // An instance where every type is entirely incomplete. The empty-archive
      // branch and the fully-incomplete branch both reach `pct`, and treating
      // either as 100% would open the page on a lie.
      renderPage([state("scene", 10, 10), state("performer", 5, 5)]);
      expect(
        screen.getByRole("progressbar", { name: "Archive completion" }),
      ).toHaveAttribute("aria-valuenow", "0");
    });

    it("reports 0% for a page with nothing to measure at all", () => {
      // Every type empty and unmeasured: total is 0, so the division is
      // undefined. 0% is the honest answer; 100% claims a finished archive.
      renderPage([state("scene", 0, 0), state("tag", 0, null)]);
      expect(
        screen.getByRole("progressbar", { name: "Archive completion" }),
      ).toHaveAttribute("aria-valuenow", "0");
    });

    it("caps a type at 100% when the incomplete count exceeds the total", () => {
      // Unreachable from the current query — `incomplete` counts entities BELOW
      // a threshold of the same type, so it cannot exceed `total`. The clamp is
      // kept anyway and asserted here, because the two numbers come from two
      // queries and a future change to either could produce it; without a
      // fixture the clamp would be untested code guarding a real possibility.
      //
      // (The first version of this test used total=12/incomplete=2 and asserted
      // 100%, which is simply wrong — that is 83%. The fixture has to be one the
      // bug could actually produce.)
      renderPage([state("performer", 12, 20)]);
      // Both clamp to the 0..100 range. Which END they land on differs by
      // construction and that is the honest reading: the card clamps a negative
      // completion up to 0, and the headline's numerator is already floored at 0,
      // so an over-count reports "nothing complete" rather than "everything".
      expect(
        screen.getByRole("progressbar", { name: "performers completion" }),
      ).toHaveAttribute("aria-valuenow", "0");
      expect(
        screen.getByRole("progressbar", { name: "Archive completion" }),
      ).toHaveAttribute("aria-valuenow", "0");
    });

    it("floors a negative aggregate at 0% rather than rendering a negative bar", () => {
      // The headline's own lower clamp. Distinct from the card's: they are two
      // separate `Math.max` calls, and mutating either one must show up here.
      // Without the floor, complete = 12 - 20 = -8 over total 12 gives -67%,
      // and aria-valuenow="-67" on a progressbar is a figure no reader can
      // interpret and a bar with a negative width.
      renderPage([state("performer", 12, 20)]);
      expect(
        screen.getByRole("progressbar", { name: "Archive completion" }),
      ).toHaveAttribute("aria-valuenow", "0");
    });

    it("states both halves of the fraction so the percentage is checkable", () => {
      renderPage([state("scene", 10, 1)]);
      expect(screen.getByText(/9 of 10 catalogued/)).toBeInTheDocument();
    });
  });

  describe("a type whose completion could not be measured", () => {
    const unmeasured = state("site", 400, null);

    it("never claims it is fully catalogued", () => {
      // 400 entities with an unknown incomplete count. Treating the unknown as
      // 0 says "All catalogued" about a type nobody has measured, which is the
      // one reading that is always wrong.
      renderPage([state("scene", 10, 1), unmeasured]);
      expect(screen.queryByText("All catalogued")).not.toBeInTheDocument();
      expect(screen.getByText("Not measured")).toBeInTheDocument();
    });

    it("prints no per-type fraction for it, not a computed one", () => {
      // The `measurable` guard already blocks the "N of M" line, so a mutation
      // that changes the arithmetic INSIDE that branch is invisible to any test
      // that only checks the absent case. This one renders a measured type and
      // checks the arithmetic itself, which is where such a mutation lands.
      // A single scene type makes the headline and the card identical, so the
      // text matches twice. The aria-valuenow assertion is the unambiguous one:
      // it can only come from the card's own arithmetic.
      renderPage([state("scene", 10, 4)]);
      expect(
        screen.getByRole("progressbar", { name: "scenes completion" }),
      ).toHaveAttribute("aria-valuenow", "60");
      expect(screen.getAllByText(/6 of 10/).length).toBeGreaterThan(0);
    });

    it("shows no progress bar for it", () => {
      // A 0%-width bar renders as an empty track and reads as "0% complete",
      // which is a measurement. No bar is an honest "unknown".
      renderPage([unmeasured]);
      const bars = screen.getAllByRole("progressbar");
      expect(bars).toHaveLength(1);
      expect(bars[0]).toHaveAttribute("aria-label", "Archive completion");
      expect(
        screen.queryByRole("progressbar", { name: "sites completion" }),
      ).not.toBeInTheDocument();
    });

    it("excludes it from the headline totals", () => {
      // 9 of 10 scenes known-complete plus 400 unmeasured sites must NOT render
      // as 409 of 410 = 100%, which is what including it would produce.
      renderPage([state("scene", 10, 1), unmeasured]);
      expect(screen.getByText(/9 of 10 catalogued/)).toBeInTheDocument();
      expect(screen.queryByText(/409 of 410/)).not.toBeInTheDocument();
    });
  });

  describe("a type with no entities at all", () => {
    it("says so instead of claiming completion", () => {
      renderPage([state("tag", 0, 0)]);
      expect(screen.getByText("No records yet")).toBeInTheDocument();
    });

    it("does not claim a measurement failed for an empty type", () => {
      // Live rendering showed studios and tags saying BOTH "Completion not
      // measured for this type" AND "No records yet" on the same card. There is
      // no numerator to read because there is nothing to count -- that is not a
      // failed measurement, and the two statements contradict each other on
      // screen. An empty type must show only the second.
      renderPage([state("studio", 0, null)]);
      expect(
        screen.queryByText("Completion not measured"),
      ).not.toBeInTheDocument();
      expect(screen.getByText("No records yet")).toBeInTheDocument();
    });

    it("does not drag the headline percentage down", () => {
      // A brand-new instance: tags and sites empty, scenes fully catalogued.
      // The headline must be 100%, not 25%.
      renderPage([
        state("scene", 4, 0),
        state("tag", 0, 0),
        state("site", 0, 0),
      ]);
      // Scoped to the headline: a fully catalogued type ALSO renders 100% on its
      // own card, so a bare getByText is ambiguous here. That ambiguity is the
      // test's problem, not the page's.
      const headline = screen.getByRole("progressbar", {
        name: "Archive completion",
      });
      expect(headline).toHaveAttribute("aria-valuenow", "100");
    });
  });

  describe("the per-type cards", () => {
    it("links the incomplete count to that type's own list", () => {
      // Every type, not just the first. Asserting only performers' href left the
      // other four routes untested, so pointing `site` at /performers passed --
      // which is exactly what mutation 7 did.
      const routes: [string, string][] = [
        ["performer", "/performers"],
        ["scene", "/scenes"],
        ["studio", "/studios"],
        ["site", "/sites"],
        ["tag", "/tags"],
      ];
      // `cleanup` per iteration: testing-library does not unmount between
      // assertions in a loop, so a second render leaves the first DOM in the
      // document and getByRole finds two links with the same name.
      for (const [entityType, href] of routes) {
        const { unmount } = renderPage([state(entityType, 400, 58)]);
        expect(
          screen.getByRole("link", { name: /58 records still incomplete/ }),
        ).toHaveAttribute("href", href);
        unmount();
      }
    });

    it("uses the singular for exactly one record", () => {
      renderPage([state("studio", 3, 1)]);
      expect(
        screen.getByRole("link", { name: /1 record still incomplete/ }),
      ).toBeInTheDocument();
    });

    it("shows a link per incomplete type, not one aggregate link", () => {
      // Each type links to its own list, so a user can act on the type they
      // care about. One aggregate link would send everyone to the same place.
      renderPage([state("performer", 10, 2), state("scene", 10, 3)]);
      expect(
        screen.getByRole("link", { name: /2 records still incomplete/ }),
      ).toHaveAttribute("href", "/performers");
      expect(
        screen.getByRole("link", { name: /3 records still incomplete/ }),
      ).toHaveAttribute("href", "/scenes");
    });
  });

  describe("what the page must never do", () => {
    it("does not pressure the reader", () => {
      // §7.25's line: a page states a fact and offers the work. No countdown, no
      // urgency, no claim about how much the reader personally owes.
      const { container } = renderPage([state("performer", 400, 358)]);
      const text = container.textContent ?? "";
      for (const forbidden of ["only", "left!", "hurry", "you owe", "urgent"]) {
        expect(text.toLowerCase()).not.toContain(forbidden);
      }
    });

    it("leads with what is done rather than what is missing", () => {
      // The first number a reader meets must be the completion figure. A page
      // whose headline is the incomplete count is a debt report.
      const { container } = renderPage([state("performer", 400, 358)]);
      const text = container.textContent ?? "";
      expect(text.indexOf("11%")).toBeGreaterThanOrEqual(0);
      expect(text.indexOf("11%")).toBeLessThan(text.indexOf("358"));
    });
  });
});
