import { render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";
import LintBoard from "../LintBoard";

/**
 * These tests are about the DESIGN constraints of the lint board, because those
 * are the parts a compiler cannot see and an unremarkable edit can undo:
 *
 *   - a detector that found nothing must read as a clean result, not as a missing
 *     one, and must not vanish from the page;
 *   - a truncated detector must say so;
 *   - an entity kind with no route must render as text rather than link somewhere
 *     wrong;
 *   - and there must be NO way to apply, merge or fix anything from here -- SPEC
 *     §7.24.4's rule is that a detector emits a quest and never auto-merges, and
 *     this page is the whole front end of that promise.
 *
 * Every absence assertion below is preceded by counting its subject, because an
 * absence assertion whose subject is not there proves nothing.
 */

type Report = Parameters<typeof LintBoard>[0]["reports"][number];

const report = (over: Partial<Report> = {}): Report =>
  ({
    slug: "duplicate_performer_url",
    description: "Two performers share a URL",
    entityType: "performer",
    bountyPoints: 10,
    count: 0,
    limitNotice: null,
    candidates: [],
    ...over,
  }) as Report;

const renderBoard = (reports: Report[]) =>
  render(
    <MemoryRouter>
      <LintBoard reports={reports} />
    </MemoryRouter>,
  );

describe("LintBoard", () => {
  it("renders a finding with its record, its explanation and a link to the record", () => {
    renderBoard([
      report({
        count: 2,
        candidates: [
          {
            __typename: "LintCandidate" as const,
            entityId: "01a0f15f-809e-7841-9252-88d1adf5dafb",
            entityName: "Etoile",
            detail: "https://example.test/x is recorded on 2 performers",
          },
          {
            __typename: "LintCandidate" as const,
            entityId: "01a0f1a2-1a77-7f8d-994a-653fa934929b",
            entityName: "Nina",
            detail: "https://example.test/x is recorded on 2 performers",
          },
        ],
      }),
    ]);

    // The subject is present before anything is asserted about it. Counted by NAME
    // rather than by a list role: react-bootstrap's ListGroup.Item renders a div,
    // so `getAllByRole("listitem")` finds nothing and a count of zero would have
    // made the absence checks below vacuous.
    expect(screen.getByText("Etoile")).toBeInTheDocument();
    expect(screen.getByText("Nina")).toBeInTheDocument();

    // The name is a LINK to the record's page: the curator's next click is the
    // ordinary edit, not an action this page invents.
    const link = screen.getByRole("link", { name: "Etoile" });
    expect(link).toHaveAttribute(
      "href",
      "/performers/01a0f15f-809e-7841-9252-88d1adf5dafb",
    );

    // And the detector's own sentence is shown, not a rule id re-worded here.
    expect(screen.getAllByText(/is recorded on 2 performers/)).toHaveLength(2);
  });

  it("says a detector found nothing, and keeps the detector on the page", () => {
    renderBoard([report({ count: 0, candidates: [] })]);

    // The card is still there -- an empty detector that disappears is
    // indistinguishable from a detector that never ran.
    expect(screen.getByText("Two performers share a URL")).toBeInTheDocument();
    expect(screen.getByText(/Nothing found/i)).toBeInTheDocument();
    // And the summary says there is nothing, rather than counting findings.
    expect(screen.getByText(/No findings right now/i)).toBeInTheDocument();
    expect(screen.queryByText(/findings across/)).not.toBeInTheDocument();
  });

  it("says a truncated detector stopped at its limit", () => {
    renderBoard([
      report({
        count: 1,
        candidates: [
          {
            __typename: "LintCandidate" as const,
            entityId: "a",
            entityName: "Someone",
            detail: "shares a url",
          },
        ],
        limitNotice:
          "at least 1 finding(s): this detector stopped at its limit",
      }),
    ]);

    expect(screen.getByText(/stopped at its limit/)).toBeInTheDocument();
  });

  it("renders an entity kind with no route as text, not as a link to nowhere", () => {
    renderBoard([
      report({
        entityType: "content_item",
        count: 1,
        candidates: [
          {
            __typename: "LintCandidate" as const,
            entityId: "abc",
            entityName: "A content item",
            detail: "something",
          },
        ],
      }),
    ]);

    // The subject exists...
    expect(screen.getByText("A content item")).toBeInTheDocument();
    // ...and it is NOT a link, because a link built from a route the client has no
    // page for is a 404 the compiler cannot catch.
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
  });

  it("offers no way to apply, merge or fix anything", () => {
    renderBoard([
      report({
        count: 2,
        candidates: [
          {
            __typename: "LintCandidate" as const,
            entityId: "a",
            entityName: "Etoile",
            detail: "alias collision",
          },
          {
            __typename: "LintCandidate" as const,
            entityId: "b",
            entityName: "Nina",
            detail: "alias collision",
          },
        ],
      }),
    ]);

    // SUBJECT FIRST: the board really did render findings, so "no control" is a
    // statement about a populated page rather than about an empty one.
    expect(screen.getByText("Etoile")).toBeInTheDocument();
    expect(screen.getByText("Nina")).toBeInTheDocument();
    expect(screen.getAllByText(/alias collision/)).toHaveLength(2);

    expect(screen.queryAllByRole("button")).toHaveLength(0);
    // And nothing that looks like the action even as text: the whole point of the
    // item is that the merge happens through consensus, not from a work list.
    expect(
      screen.queryByText(/merge|apply|fix this|resolve now/i),
    ).not.toBeInTheDocument();
  });

  it("distinguishes an empty registry from a clean archive", () => {
    renderBoard([]);

    // No detectors at all is a SERVER fault -- the registry has nothing in it --
    // and saying "nothing found" here would be the false clean report that the
    // service's own validation exists to prevent.
    expect(
      screen.getByText(/returned no detectors at all/i),
    ).toBeInTheDocument();
    // AND the clean-archive sentence is absent -- this is the assertion that found
    // the component showing "no findings right now, every detector below ran" for a
    // board with no detectors on it at all.
    expect(
      screen.queryByText(/No findings right now/i),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByText(/Every detector below ran/i),
    ).not.toBeInTheDocument();
  });

  it("counts findings across detectors and is explicit that nothing changes by itself", () => {
    renderBoard([
      report({
        slug: "a",
        description: "First detector",
        count: 2,
        candidates: [
          {
            __typename: "LintCandidate" as const,
            entityId: "a",
            entityName: "One",
            detail: "x",
          },
          {
            __typename: "LintCandidate" as const,
            entityId: "b",
            entityName: "Two",
            detail: "x",
          },
        ],
      }),
      report({
        slug: "b",
        description: "Second detector",
        count: 1,
        candidates: [
          {
            __typename: "LintCandidate" as const,
            entityId: "c",
            entityName: "Three",
            detail: "y",
          },
        ],
      }),
    ]);

    expect(
      screen.getByText(/3 findings across 2 detectors/),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/Nothing here is changed automatically/i),
    ).toBeInTheDocument();
    // The count is the sum, not the number of cards.
    expect(within(screen.getByText(/3 findings/)).getByText(/3/)).toBeTruthy();
  });
});
