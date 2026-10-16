import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import DemandTable from "../DemandTable";

/**
 * The demand board's table, tested where the rules live.
 *
 * The important one is the ABSENCE: no link on a hash. It is asserted with its
 * subject counted first -- "there are no links" is vacuous on a table that
 * rendered no rows, and a table that rendered no rows is exactly what a broken
 * selector produces. This is the UI half of the API's field-set gate; the two
 * exist because a locator could arrive either way.
 */

const entry = (
  over: Partial<{
    algorithm: string;
    hash: string;
    holder_lower_bound: number;
  }> = {},
) => ({
  algorithm: "PHASH",
  hash: "1234567890",
  holder_lower_bound: 4,
  ...over,
});

describe("DemandTable", () => {
  it("shows the algorithm, the hash and the holders", () => {
    render(<DemandTable entries={[entry()]} floor={2} />);

    expect(screen.getByText("PHASH")).toBeInTheDocument();
    expect(screen.getByText("1234567890")).toBeInTheDocument();
    expect(screen.getByText("4")).toBeInTheDocument();
  });

  it("calls the figure a lower bound in the column header, not only in a tooltip", () => {
    render(<DemandTable entries={[entry()]} floor={2} />);

    // A bare number in a column headed "Holders" reads as a count.
    expect(
      screen.getByRole("columnheader", { name: /at least/i }),
    ).toBeInTheDocument();
  });

  it("puts no link on a hash", () => {
    render(
      <DemandTable
        entries={[entry(), entry({ hash: "2222222222" })]}
        floor={2}
      />,
    );

    // Subject first: two entries plus the header row.
    expect(screen.getAllByRole("row")).toHaveLength(3);
    expect(screen.queryAllByRole("link")).toHaveLength(0);
  });

  it("reports an empty board as an answer rather than a failure", () => {
    render(<DemandTable entries={[]} floor={5} />);

    expect(screen.queryByRole("table")).not.toBeInTheDocument();
    expect(
      screen.getByText(/held by 5 or more members yet/i),
    ).toBeInTheDocument();
  });
});
