import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import CompletionBar, {
  type CompletionData,
} from "src/components/CompletionBar";
import { describe, expect, it } from "vitest";

/**
 * These tests encode three decisions made while building the bar, and each one
 * names the mutation that must turn it red. A completion bar has very little
 * surface area, so a test that only checks "something rendered" is worth almost
 * nothing here -- the interesting claims are all about what is NOT shown.
 *
 * MUTATIONS:
 *  1. render `null` when score < 100        -> "hides the bar when complete" goes red
 *  2. drop the missing-field list           -> "names each missing field" goes red
 *  3. label total as a field count          -> "does not call weights a count" goes red
 *  4. point the CTA at the entity page      -> "links to the edit form" goes red
 *  5. drop the CTA entirely                -> "links to the edit form" goes red
 *  6. render a CTA for an unknown type      -> "still names the missing fields but omits the CTA" goes red
 */

const partial: CompletionData = {
  entityType: "performer",
  score: 43,
  missing: ["birthdate", "eye_color", "measurements"],
  total: 80,
  earned: 34,
};

const complete: CompletionData = {
  entityType: "performer",
  score: 100,
  missing: [],
  total: 80,
  earned: 80,
};

const renderBar = (
  completion: CompletionData | null,
  entityId = "a1111111-1111-1111-1111-111111111111",
) =>
  render(
    <MemoryRouter>
      <CompletionBar completion={completion} entityId={entityId} />
    </MemoryRouter>,
  );

describe("CompletionBar", () => {
  describe("when the score is below 100", () => {
    it("shows the percentage", () => {
      renderBar(partial);
      expect(screen.getByText("43%")).toBeInTheDocument();
    });

    it("names each missing field, because 'improve this performer' is not a task anyone can act on", () => {
      renderBar(partial);
      // Human labels, not raw column names: eye_color -> "eye color".
      expect(screen.getByText("birthdate")).toBeInTheDocument();
      expect(screen.getByText("eye color")).toBeInTheDocument();
      expect(screen.getByText("measurements")).toBeInTheDocument();
    });

    it("counts the missing fields, not the weights", () => {
      renderBar(partial);
      // 3 missing fields, even though earned/total are 34/80. A bar that said
      // "missing 34 fields" would be counting something the user never asked for.
      expect(screen.getByText(/missing 3 fields/i)).toBeInTheDocument();
    });

    it("does not call the weights a field count", () => {
      renderBar(partial);
      // total is a WEIGHT SUM. Rendered naively as "0 of 80" it reads as
      // "80 fields", when 12 are actually missing. This asserts the fix.
      expect(screen.queryByText(/0 of 80/)).not.toBeInTheDocument();
      expect(screen.getByText(/34 of 80 weighted/)).toBeInTheDocument();
    });

    it("links to the edit form, not the entity page", () => {
      renderBar(partial);
      const cta = screen.getByRole("link", { name: /help complete this/i });
      // Completing an entity is a form with a dozen fields and an edit history.
      // Linking to the read-only page would send the user nowhere useful.
      expect(cta).toHaveAttribute(
        "href",
        "/performers/a1111111-1111-1111-1111-111111111111/edit",
      );
    });
  });

  describe("when the entity is complete", () => {
    it("hides the bar and says so plainly", () => {
      renderBar(complete);
      // A full-width green bar on every well-curated page is noise, and it
      // competes with the pages that actually need help.
      expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
      expect(screen.getByText(/complete/i)).toBeInTheDocument();
    });

    it("offers no call to action, because there is nothing to do", () => {
      renderBar(complete);
      expect(
        screen.queryByRole("link", { name: /help complete this/i }),
      ).not.toBeInTheDocument();
    });
  });

  describe("when the score could not be computed", () => {
    it("renders nothing at all", () => {
      // null means "could not be scored", which is different from a score of 0.
      // Showing "0% complete" would be a false claim about the record.
      const { container } = renderBar(null);
      expect(container).toBeEmptyDOMElement();
    });
  });

  describe("when the entity type is unknown", () => {
    it("still names the missing fields but omits the CTA", () => {
      // The bar's value is the missing-field list, so it still renders. A link
      // to an arbitrary page would be worse than no link.
      renderBar({ ...partial, entityType: "gallery" });
      expect(screen.getByText("birthdate")).toBeInTheDocument();
      expect(
        screen.queryByRole("link", { name: /help complete this/i }),
      ).not.toBeInTheDocument();
    });
  });

  describe("when showCTA is false", () => {
    it("omits the CTA but keeps the missing fields", () => {
      render(
        <MemoryRouter>
          <CompletionBar completion={partial} entityId="x" showCTA={false} />
        </MemoryRouter>,
      );
      expect(screen.getByText("birthdate")).toBeInTheDocument();
      expect(
        screen.queryByRole("link", { name: /help complete this/i }),
      ).not.toBeInTheDocument();
    });
  });

  describe("CTA targets for every entity type", () => {
    // Each type has its own edit route; five hand-written literals is five
    // chances to point at the wrong entity, so this table is the guard.
    const cases: [CompletionData["entityType"], string][] = [
      ["performer", "/performers/EID/edit"],
      ["scene", "/scenes/EID/edit"],
      ["studio", "/studios/EID/edit"],
      ["site", "/sites/EID/edit"],
      ["tag", "/tags/EID/edit"],
    ];

    for (const [entityType, expected] of cases) {
      it(`points a ${entityType} at its own edit route`, async () => {
        renderBar({ ...partial, entityType }, "EID");
        const cta = screen.getByRole("link", { name: /help complete this/i });
        await userEvent.click(cta);
        expect(cta).toHaveAttribute("href", expected);
      });
    }
  });
});
