import { render, screen, within } from "@testing-library/react";
import MetricsList, {
  groupDigits,
  type InstanceMetricType,
  isWindowed,
} from "src/pages/metrics/MetricsList";
import { describe, expect, it } from "vitest";

/**
 * What a table of figures can be wrong about, and what these tests refuse.
 *
 * The failure that matters is not a missing row: it is a number whose meaning is not on
 * the page. A reader who sees "scenes_total 1,104,375" and nothing else will supply their
 * own definition (deleted scenes included? sources counted?) and be wrong in a way no
 * error message will ever announce. So the assertions here are mostly about what ELSE is
 * rendered beside each value.
 *
 * A fixture of THREE figures, chosen so the mutants are separable: one windowed figure, one
 * lifetime figure, and no two of them sharing a value or a length of definition.
 *
 * MUTATIONS this suite is written against, each checked to redden exactly one test:
 *  1. drop the definition column            -> "keeps every figure beside its definition"
 *  2. hide a figure with no definition      -> "shows an unexplained figure rather than hiding it"
 *  3. mark every figure as windowed         -> "marks exactly the figures bound to the window"
 *  4. drop the digit grouping               -> "groups the digits"
 *  5. ignore the unit                       -> "renders a unit that is not a count"
 *  6. render an empty table for no figures  -> "says the list is empty rather than showing a bare table"
 */

const FIXTURE: InstanceMetricType[] = [
  {
    key: "scenes_total",
    unit: "count",
    definition: "Catalogue scenes not marked deleted.",
    value: 1104375,
  },
  {
    key: "members_active_window",
    unit: "count",
    definition:
      "Accounts inside the window whose last authenticated call (users.last_api_call) falls in it.",
    value: 2,
  },
  {
    key: "sources_checked_window",
    unit: "count",
    definition:
      "Source addresses whose health was last checked inside the window (content_sources.last_checked).",
    value: 17,
  },
];

const renderList = (metrics: InstanceMetricType[] = FIXTURE) =>
  render(
    <MetricsList
      metrics={metrics}
      windowDays={30}
      since="2026-09-16T00:00:00Z"
    />,
  );

const rowFor = (key: string) => {
  // The row is found BY ITS FIGURE NAME, which is the name an operator will quote.
  const cell = screen.getByText(key, { exact: false });
  const row = cell.closest("tr");
  if (!row) throw new Error(`no row for ${key}`);
  return within(row);
};

describe("MetricsList", () => {
  it("keeps every figure beside its definition", () => {
    renderList();

    for (const metric of FIXTURE) {
      expect(
        rowFor(metric.key).getByText(metric.definition),
      ).toBeInTheDocument();
    }

    // The absence half, with its subject counted first: "no figure is missing a
    // definition" is vacuously true on a page that rendered no figures at all.
    expect(
      screen.getAllByText(
        /^Catalogue scenes|^Accounts inside|^Source addresses/,
      ),
    ).toHaveLength(FIXTURE.length);
  });

  it("shows an unexplained figure rather than hiding it", () => {
    const unexplained: InstanceMetricType = {
      key: "mystery_total",
      unit: "count",
      definition: "",
      value: 7,
    };
    renderList([...FIXTURE, unexplained]);

    // The figure is on the page...
    expect(rowFor("mystery_total").getByText("7")).toBeInTheDocument();
    // ...and so is the reason it cannot be trusted.
    expect(
      rowFor("mystery_total").getByText(/arrived without a definition/i),
    ).toBeInTheDocument();

    // And the four rows are all there: a page that quietly dropped the fourth would
    // satisfy "the unexplained figure is warned about" by having no such figure.
    expect(screen.getAllByRole("row")).toHaveLength(FIXTURE.length + 1 + 1);
  });

  it("marks exactly the figures bound to the window", () => {
    renderList();

    const windowed = FIXTURE.filter((m) => isWindowed(m.key));
    expect(windowed.length).toBeGreaterThan(0);
    expect(windowed.length).toBeLessThan(FIXTURE.length);

    // Counted before it is asserted absent from anywhere: an unmarked-everything page
    // and a marked-everything page both satisfy "the total carries no marker" if the
    // assertion is written the other way round.
    expect(screen.getAllByText("window")).toHaveLength(windowed.length + 1); // the rows, and the sentence
    expect(
      rowFor("scenes_total").queryByText("window"),
    ).not.toBeInTheDocument();

    // The window is named with its width and its start, because "in the window" means
    // nothing to a reader who does not know where it begins.
    expect(
      screen.getByText(/covers the 30\s+days from 2026-09-16T00:00:00Z/),
    ).toBeInTheDocument();
  });

  it("groups the digits", () => {
    renderList();

    expect(rowFor("scenes_total").getByText("1,104,375")).toBeInTheDocument();
    // Paired with the ungrouped string's absence, because "1,104,375 is on the page" is
    // also true of a page that renders the number twice.
    expect(screen.queryByText("1104375")).not.toBeInTheDocument();

    // And a number that needs no separator does not get one.
    expect(rowFor("members_active_window").getByText("2")).toBeInTheDocument();
    expect(groupDigits(7)).toBe("7");
  });

  it("renders a unit that is not a count", () => {
    renderList([
      {
        key: "median_hours",
        unit: "hours",
        definition:
          "Median hours from an edit being opened to it reaching a decision.",
        value: 4,
      },
    ]);

    expect(rowFor("median_hours").getByText(/4\s*hours/)).toBeInTheDocument();
  });

  it("says the list is empty rather than showing a bare table", () => {
    renderList([]);

    expect(screen.getByRole("alert")).toHaveTextContent(
      /published no figures/i,
    );
    // A bare table would be a different claim — that the instance has nothing to count —
    // so the absence of the header is asserted along with the sentence.
    expect(screen.queryByText("Figure")).not.toBeInTheDocument();
  });
});
