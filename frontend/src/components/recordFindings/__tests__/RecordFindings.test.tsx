import { act, screen, waitFor, within } from "@testing-library/react";
import type { LintFindingsForQuery } from "src/graphql";
import { LintFindingsForDocument, MeDocument, RoleEnum } from "src/graphql";
import { renderForm } from "src/test/renderForm";
import { describe, expect, it } from "vitest";
import RecordFindings from "..";
import Findings from "../Findings";

type Report = LintFindingsForQuery["lintFindingsFor"][number];

const ME = "01a00000-0000-7000-8000-000000000001";
const THEM = "01a00000-0000-7000-8000-000000000002";
const FLAGGED = "01a00000-0000-7000-8000-000000000003";

// `__typename` is required by the generated type and vitest does not type-check: omitting it left the
// suite green and `tsc --noEmit` red, which is how the same class of defect shipped once already.
const cand = (entityId: string, entityName: string, detail: string) => ({
  __typename: "LintCandidate" as const,
  entityId,
  entityName,
  detail,
});

const report = (over: Partial<Report> = {}): Report => ({
  __typename: "LintQuestReport",
  slug: "duplicate_performer_url",
  description: "Two performers record the same URL",
  entityType: "performer",
  bountyPoints: 10,
  count: 2,
  limitNotice: null,
  candidates: [
    cand(ME, "Me", "https://example.test/a is recorded on 2 performers"),
    cand(THEM, "Them", "https://example.test/a is recorded on 2 performers"),
  ],
  ...over,
});

// THE IDENTITY ARRIVES AS A `me` QUERY, NOT AS A CONTEXT. `useAuth` calls `useMe` with
// fetchPolicy network-only and reads `data.me`, so renderForm's `auth` prop is inert here -- the same
// discovery, with the same comment, is in pages/leaderboard/__tests__/LeaderboardReachability.test.tsx.
// A container test without this mock renders nothing for a reason that has nothing to do with the
// panel, which is exactly how the first run of these tests failed.
const meMock = (roles: string[]) => ({
  request: { query: MeDocument },
  result: {
    data: {
      me: {
        __typename: "User",
        id: ME,
        name: "Voter",
        email: "voter@example.test",
        roles,
        vote_count: 0,
        user_vote_candidate_count: 0,
        created_at: "2026-01-01T00:00:00Z",
        last_login: "2026-10-01T00:00:00Z",
        fingerprint_scopes: [],
        scene_vote_up_count: 0,
        scene_vote_down_count: 0,
        studio_vote_up_count: 0,
        studio_vote_down_count: 0,
        performer_vote_up_count: 0,
        performer_vote_down_count: 0,
        gallery_vote_up_count: 0,
        gallery_vote_down_count: 0,
      },
    },
  },
  maxUsageCount: 5,
});

const votRoles = [RoleEnum.READ, RoleEnum.VOTE];

// "Nothing rendered" and "nothing yet" look the same to an assertion, and `waitFor` on a NEGATIVE
// assertion resolves on the first tick, so it passes before the query has had a chance to answer.
// Where a positive control is available it is used instead; this flush is only for the one case where
// the answer is "no panel", and it is applied to a test whose control is asserted first.
const settle = async (ms = 50) => {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, ms));
  });
};

describe("what a finding offers a curator", () => {
  // THE CANDIDATE ACTION. A duplicate is fixed by merging the two records, through the ordinary
  // consensus flow: the link has to carry BOTH ids -- the record being looked at is the merge target
  // and the other half is the source -- or the reader arrives at a merge page that knows nothing about
  // the finding they came from.
  it("links a duplicate to the merge flow with both halves named", () => {
    renderForm(
      <Findings entityType="performer" entityId={ME} reports={[report()]} />,
    );

    const merge = screen.getByRole("link", {
      name: /Merge Them into this record/,
    });
    expect(merge).toHaveAttribute(
      "href",
      `/performers/${ME}/merge?source=${THEM}`,
    );
  });

  // An alias collision reports the ALIAS HOLDER, which is never the record being looked at: the row a
  // curator came from is the one whose name is claimed. The action must still be a merge of the two.
  it("links an alias collision to the merge flow too, from whichever side was asked about", () => {
    renderForm(
      <Findings
        entityType="performer"
        entityId={ME}
        reports={[
          report({
            slug: "performer_alias_collision",
            description:
              "A performer's alias is another performer's name or alias",
            count: 1,
            candidates: [
              cand(THEM, "Claimer", "Me is also the name of Claimer"),
            ],
          }),
        ]}
      />,
    );

    const merge = screen.getByRole("link", {
      name: /Merge Claimer into this record/,
    });
    expect(merge).toHaveAttribute(
      "href",
      `/performers/${ME}/merge?source=${THEM}`,
    );
  });

  // AND EVERY OTHER FINDING MUST NOT. A birthdate that postdates a scene is fixed on the record, and
  // offering a merge for it would be sending a curator to combine two records over a typo. This is the
  // assertion that makes the allow-list load-bearing: without it, a component that offered a merge for
  // every finding with two candidates would pass every other test in this file.
  it("offers the edit page and no merge for a finding that is not a merge", () => {
    renderForm(
      <Findings
        entityType="performer"
        entityId={ME}
        reports={[
          report({
            // TWO records and not one of them mergeable: this is the case that tells the allow-list
            // apart from the shape of the answer. Without the second candidate, the slug test and the
            // "one other record" test agree, and dropping either of them from the component would go
            // unnoticed -- measured: the first version of this test survived exactly that mutation.
            slug: "impossible_scene_date",
            description: "A scene is dated before the performer was born",
            count: 3,
            candidates: [
              cand(ME, "Me", "born 1990, in a scene dated 1985"),
              cand(THEM, "Them", "also in a scene dated 1985"),
            ],
          }),
        ]}
      />,
    );

    expect(
      screen.getByRole("link", { name: "Fix it on the edit page" }),
    ).toHaveAttribute("href", `/performers/${ME}/edit`);
    expect(screen.queryByRole("link", { name: /Merge/ })).toBeNull();
  });

  // The absence assertion above needs its subject counted first, or it passes on an empty panel.
  it("renders the finding it was given, and one link per action", () => {
    renderForm(
      <Findings entityType="performer" entityId={ME} reports={[report()]} />,
    );

    // The detail sentence appears twice and that is faithful: the pass gives every record in a finding
    // its own sentence, and for a duplicate URL the sentence is the same for both halves.
    expect(
      screen.getAllByText(/is recorded on 2 performers/).length,
    ).toBeGreaterThan(0);
    expect(screen.getAllByRole("link")).toHaveLength(2);
    expect(screen.getAllByText("Them").length).toBeGreaterThan(0);
  });

  // Lint never merges and never applies anything (SPEC §7.24.4): this panel is a reading, and the only
  // controls on it are links to flows that already exist and that somebody votes on.
  it("holds no control that changes a record", () => {
    renderForm(
      <Findings
        entityType="performer"
        entityId={ME}
        reports={[report(), report({ slug: "low_resolution_performer_image" })]}
      />,
    );

    expect(screen.getAllByRole("link").length).toBeGreaterThan(0);
    expect(document.querySelectorAll("button")).toHaveLength(0);
  });

  // A finding with no other half (nothing to merge with) is still reported, and still offers no merge.
  it("does not offer a merge when the finding names no other record", () => {
    renderForm(
      <Findings
        entityType="performer"
        entityId={ME}
        reports={[
          report({
            candidates: [cand(ME, "Me", "an image too small to use")],
          }),
        ]}
      />,
    );

    expect(screen.queryByRole("link", { name: /Merge/ })).toBeNull();
    expect(
      screen.getByRole("link", { name: "Fix it on the edit page" }),
    ).toBeInTheDocument();
  });

  // A truncated answer says so, here as on the board: a list that stopped early and does not say so
  // reads as a complete one.
  it("says when the pass stopped before it was finished", () => {
    renderForm(
      <Findings
        entityType="performer"
        entityId={ME}
        reports={[
          report({
            limitNotice: "The duplicate URL detector stopped at its limit.",
          }),
        ]}
      />,
    );

    expect(screen.getByText(/stopped at its limit/)).toBeInTheDocument();
  });
});

describe("when the panel is shown at all", () => {
  const mock = (data: Report[], entityId = ME) => [
    {
      request: {
        query: LintFindingsForDocument,
        variables: { entityType: "performer", entityId },
      },
      result: { data: { lintFindingsFor: data } },
      maxUsageCount: 5,
    },
  ];

  it("shows the findings for a curator who can vote", async () => {
    renderForm(<RecordFindings entityType="performer" entityId={ME} />, {
      mocks: [meMock(votRoles), ...mock([report()])],
    });

    await waitFor(() =>
      expect(screen.getByText(/flags this record/)).toBeInTheDocument(),
    );
    expect(screen.getAllByText("Them").length).toBeGreaterThan(0);
  });

  // A CLEAN RECORD SAYS NOTHING, so that the records which are flagged are the ones a reader notices.
  // The subject is counted in the test above: this render is the same component with the same auth and
  // a different answer, not an empty page passing vacuously.
  it("renders nothing at all for a clean record, beside one that is flagged", async () => {
    // THE CONTROL IS THE POINT. "Nothing rendered" and "nothing yet" are the same DOM, and an
    // assertion on the absence of an element resolves on the first tick -- so a clean-record test on
    // its own stays green even with the guard deleted (measured: it did). Rendering a FLAGGED record
    // through the same component, auth and mock link makes the answer's arrival the thing that is
    // waited for; only then does the clean side's emptiness mean anything.
    renderForm(
      <div>
        <div data-testid="clean">
          <RecordFindings entityType="performer" entityId={ME} />
        </div>
        <div data-testid="flagged">
          <RecordFindings entityType="performer" entityId={FLAGGED} />
        </div>
      </div>,
      { mocks: [meMock(votRoles), ...mock([]), ...mock([report()], FLAGGED)] },
    );

    await waitFor(() =>
      expect(
        within(screen.getByTestId("flagged")).getByText(/flags this record/),
      ).toBeInTheDocument(),
    );

    const clean = within(screen.getByTestId("clean"));
    expect(clean.queryByText(/flags this record/)).toBeNull();
    expect(clean.queryByText(/detectors flag/)).toBeNull();
    expect(clean.queryByText(/could not be run/)).toBeNull();
    expect(clean.queryByRole("link")).toBeNull();
  });

  // A STRANDED DETECTOR MAKES THE PASS REFUSE, and a refusal rendered as "nothing here" reports a quiet
  // instance while the check has in fact stopped running.
  it("names the refusal rather than reporting a quiet record", async () => {
    renderForm(<RecordFindings entityType="performer" entityId={ME} />, {
      mocks: [
        meMock(votRoles),
        {
          request: {
            query: LintFindingsForDocument,
            variables: { entityType: "performer", entityId: ME },
          },
          error: new Error(
            "the data checks are refused: a detector is enabled that this build does not implement",
          ),
        },
      ],
    });

    await waitFor(() =>
      expect(
        screen.getByText(/could not be run for this record/),
      ).toBeInTheDocument(),
    );
    expect(screen.getByText(/does not implement/)).toBeInTheDocument();
  });

  // The schema refuses this query to a reader without VOTE, so the panel is not theirs to see -- and it
  // must not be a card that says "nothing is wrong here" to somebody who was never asked. The SKIP that
  // keeps the request from being sent is the hook's own argument; that it is not observable here is
  // stated rather than implied, and the same gate is asserted against a real server in
  // internal/api/lint_quest_graphql_integration_test.go.
  it("renders nothing for a reader who cannot vote", async () => {
    renderForm(<RecordFindings entityType="performer" entityId={ME} />, {
      mocks: [meMock([RoleEnum.READ]), ...mock([report()])],
    });

    await settle();
    expect(screen.queryByText(/flags this record/)).toBeNull();
    expect(screen.queryByText(/could not be run/)).toBeNull();
    expect(screen.queryByRole("link", { name: /Merge/ })).toBeNull();
  });
});
