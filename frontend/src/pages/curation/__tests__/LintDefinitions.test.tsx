import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import LintDefinitions from "../LintDefinitions";
import { describeRegistryError } from "../LintQuestDefinitions";

/**
 * The registry is a table that exists to make a MISCONFIGURATION legible, so the
 * tests are about the states and about what the page refuses to be:
 *
 *   - `enabled` and `implemented` are two independent facts, and the state that
 *     matters is the one where they disagree -- a detector switched on that this
 *     build cannot run refuses the whole board, so from the board alone it looks
 *     like a broken instance rather than a misconfigured one;
 *   - a retired row must remain VISIBLE (retirement is a rollback tool, not a
 *     delete);
 *   - there is no control on the page, and the absence is asserted with its
 *     subject counted first;
 *   - and an empty registry is a server fault, not a quiet instance.
 */

type Definitions = Parameters<typeof LintDefinitions>[0]["definitions"][number];

const def = (over: Partial<Definitions> = {}): Definitions =>
  ({
    __typename: "LintQuestDefinition" as const,
    slug: "duplicate_performer_url",
    description: "Two performers record the same URL",
    enabled: true,
    bountyPoints: 10,
    isBuiltin: true,
    implemented: true,
    ...over,
  }) as Definitions;

describe("LintDefinitions", () => {
  it("shows a running detector as running, with its price and its origin", () => {
    render(
      <LintDefinitions
        definitions={[def({ slug: "dup_url", bountyPoints: 25 })]}
      />,
    );

    expect(screen.getByText("dup_url")).toBeInTheDocument();
    expect(screen.getByText("Running")).toBeInTheDocument();
    expect(screen.getByText("25")).toBeInTheDocument();
    expect(screen.getByText("Ships with this fork")).toBeInTheDocument();
    expect(screen.getByText(/1 of 1 running/)).toBeInTheDocument();
  });

  it("names a detector switched on that this build cannot run, and says the board is refused", () => {
    render(
      <LintDefinitions
        definitions={[
          def({
            slug: "kept_after_rollback",
            enabled: true,
            implemented: false,
          }),
        ]}
      />,
    );

    // The row is stranded: enabled, unimplemented.
    expect(screen.getByText("kept_after_rollback")).toBeInTheDocument();
    expect(
      screen.getByText(/switched on, but this build cannot run it/i),
    ).toBeInTheDocument();
    // And the consequence is stated, because "the board is refused" is the fact a
    // curator experiences and cannot otherwise connect to this row.
    expect(screen.getByText(/the board is refused/i)).toBeInTheDocument();
    // The count must not claim it is running.
    expect(screen.getByText(/0 of 1 running/)).toBeInTheDocument();
  });

  it("keeps a retired detector visible, and says whether this build still has it", () => {
    render(
      <LintDefinitions
        definitions={[
          def({ slug: "retired_here", enabled: false, implemented: true }),
          def({ slug: "retired_and_gone", enabled: false, implemented: false }),
        ]}
      />,
    );

    // Subject first: both rows are on the page at all -- retirement is a rollback
    // tool, so a retired row is a fact about the instance, not a deletion.
    expect(screen.getByText("retired_here")).toBeInTheDocument();
    expect(screen.getByText("retired_and_gone")).toBeInTheDocument();

    expect(screen.getByText("Retired")).toBeInTheDocument();
    expect(
      screen.getByText("Retired, and not in this build"),
    ).toBeInTheDocument();
    // A retired row is not a problem state, so there is no danger notice.
    expect(
      screen.queryByText(/switched on that this build/i),
    ).not.toBeInTheDocument();
  });

  it("has no control on it, and names where the registry is actually edited", () => {
    render(
      <LintDefinitions
        definitions={[def({ slug: "a" }), def({ slug: "b", enabled: false })]}
      />,
    );

    // SUBJECT FIRST: the table really rendered rows, so "no control" is a statement
    // about a populated page rather than about a page that failed to render.
    expect(screen.getByText("a")).toBeInTheDocument();
    expect(screen.getByText("b")).toBeInTheDocument();

    expect(screen.queryAllByRole("button")).toHaveLength(0);
    expect(screen.queryAllByRole("checkbox")).toHaveLength(0);

    // And the page says who does edit it: a read-only view that does not name the
    // writer leaves an operator with nowhere to go.
    expect(screen.getByText(/lintquests -ensure/)).toBeInTheDocument();
  });

  it("reads an empty registry as a fault, not as a quiet instance", () => {
    render(<LintDefinitions definitions={[]} />);

    expect(screen.getByText(/seed did not run/i)).toBeInTheDocument();
    // It must NOT borrow the "N of M running" summary of a real registry: an empty
    // table reported as "0 of 0 running" reads as a quiet instance.
    expect(screen.queryByText(/of \d+ running/)).not.toBeInTheDocument();
    expect(screen.queryByText("Detectors")).not.toBeInTheDocument();
  });
});

describe("describeRegistryError", () => {
  it("names a role refusal as a role refusal", () => {
    const { variant, text } = describeRegistryError("not authorized");
    expect(variant).toBe("warning");
    expect(text).toMatch(/needs the ADMIN role/);
    expect(text).toMatch(/nothing is broken/i);
  });

  it("calls anything else a fault, and quotes the server", () => {
    const { variant, text } = describeRegistryError(
      "dial tcp: connection refused",
    );
    expect(variant).toBe("danger");
    expect(text).toMatch(/This is a fault rather than a permission/);
    expect(text).toMatch(/dial tcp: connection refused/);
  });
});
