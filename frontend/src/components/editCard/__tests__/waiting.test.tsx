import { screen } from "@testing-library/react";
import { VoteStatusEnum } from "src/graphql";
import { renderForm } from "src/test/renderForm";
import { describe, expect, it } from "vitest";
import { renderWaiting } from "../EditCard";

const created = "2026-10-13T12:00:00Z";

const render = (status: VoteStatusEnum, when = created) =>
  renderForm(
    <div data-testid="root">
      {renderWaiting({ status, created: when })}
      {/* An always-rendered sibling, so "the waiting line is absent" is about the
          rule and not about a tree that rendered nothing at all. Without it,
          not.toBeEmptyDOMElement() is exactly as false as the absence assertion. */}
      <span data-testid="control" />
    </div>,
  );

describe("renderWaiting", () => {
  it("shows how long a PENDING edit has been waiting", () => {
    render(VoteStatusEnum.PENDING);

    const line = screen.queryByTestId("edit-waiting");
    // Count the subject before asserting anything about it: `not.toBeNull()` on an
    // element that is not in the document passes for the wrong reason in the other
    // direction, and a query that found nothing is indistinguishable from a rule
    // that stopped rendering.
    expect(screen.getByTestId("control")).toBeInTheDocument();
    expect(line).toBeInTheDocument();
    expect(line).toHaveTextContent(/^Waiting:/);
    expect(line?.textContent).toMatch(
      /\d+ (day|days|hour|hours|minute|minutes|second|seconds)/,
    );
  });

  it("carries the absolute instant as a title, so the relative text is not the only fact", () => {
    render(VoteStatusEnum.PENDING);

    const title = screen.getByTestId("edit-waiting").querySelector("span");
    expect(title?.getAttribute("title")).toBeTruthy();
    // 2026-10-13 is what `created` says; the tooltip must agree with it rather than
    // restating "3 days".
    expect(title?.getAttribute("title")).toContain("2026");
  });

  // One render per test, not a loop inside one: the helpers do not clean up between
  // renders within a case, so a loop leaves two roots and `getByTestId` refuses with
  // "found multiple elements" -- a harness complaint that reads like a rule failure.
  it.each([
    VoteStatusEnum.ACCEPTED,
    VoteStatusEnum.REJECTED,
    VoteStatusEnum.CANCELED,
  ])("says nothing on a %s edit, with a positive control", (decided) => {
    render(decided);

    // The control: the sibling is there, so "absent" is about the rule and not about
    // a tree that rendered nothing.
    expect(screen.getByTestId("control")).toBeInTheDocument();
    expect(screen.queryByTestId("edit-waiting")).not.toBeInTheDocument();
  });

  it("refuses to invent a number when created is unparseable", () => {
    render(VoteStatusEnum.PENDING, "not-a-time");

    expect(screen.getByTestId("control")).toBeInTheDocument();
    expect(screen.queryByTestId("edit-waiting")).not.toBeInTheDocument();
  });
});
