import { screen } from "@testing-library/react";
import {
  ConfigDocument,
  IdentificationBoardDocument,
  IdentificationQueryDetailDocument,
  RoleEnum,
  VoteIdentificationCandidateDocument,
} from "src/graphql";
import IdentificationBoard from "src/pages/identification/IdentificationBoard";
import IdentificationQuery from "src/pages/identification/IdentificationQuery";
import { renderForm } from "src/test/renderForm";
import { describe, expect, it } from "vitest";

// Reachability and role gating for the identification board (growth workstream
// W1, SPEC §5).
//
// The board has had ten backend query fields since migration 79 and, until this
// commit, no page at all. These tests exist because a board that exists and
// cannot be reached is the defect this workstream exists to fix — and because
// the board's role split (reads at READ, contributes at VOTE) is exactly the
// kind of thing that renders perfectly and fails on click.
//
// Every test below is paired with the mutation that must turn it red.

const configMock = {
  request: { query: ConfigDocument },
  result: {
    data: {
      getConfig: {
        edit_update_limit: 0,
        host_url: "https://example.test",
        require_invite: false,
        require_activation: false,
        vote_promotion_threshold: 0,
        vote_application_threshold: 0,
        voting_period: 0,
        min_destructive_voting_period: 0,
        vote_cron_interval: "",
        guidelines_url: "",
        require_scene_draft: false,
        require_tag_role: false,
        enable_genital_attributes: false,
      },
    },
  },
  maxUsageCount: 5,
};

const boardMock = {
  request: { query: IdentificationBoardDocument, variables: { limit: 100 } },
  result: {
    data: {
      listOpenIdentificationQueries: [
        {
          __typename: "IdentificationQuery",
          id: "q1",
          targetType: "scene",
          targetId: "s1",
          description: "hotel room, rainy night, around 2016",
          status: "open",
          createdAt: "2026-10-01T12:00:00Z",
          candidates: [
            {
              __typename: "IdentificationCandidate",
              id: "c1",
              entityType: "scene",
              entityId: "s9",
              voteCount: 3,
            },
          ],
        },
      ],
    },
  },
  maxUsageCount: 5,
};

const queryMock = {
  request: {
    query: IdentificationQueryDetailDocument,
    variables: { id: "q1" },
  },
  result: {
    data: {
      identificationQuery: {
        __typename: "IdentificationQuery",
        id: "q1",
        targetType: "scene",
        targetId: "s1",
        description: "hotel room, rainy night, around 2016",
        status: "open",
        createdAt: "2026-10-01T12:00:00Z",
        snapshotId: null,
        resolvedType: null,
        resolvedId: null,
        resolvedAt: null,
        resolvedBy: null,
        candidates: [
          {
            __typename: "IdentificationCandidate",
            id: "c1",
            queryId: "q1",
            entityType: "performer",
            entityId: "p9",
            note: "the studio watermark is visible in frame 3",
            voteCount: 3,
            votedByMe: false,
            createdAt: "2026-10-01T13:00:00Z",
            suggestedBy: { __typename: "User", id: "u2", name: "bob" },
            entity: {
              __typename: "Performer",
              id: "p9",
              name: "Someone Remembered",
              disambiguation: null,
            },
          },
        ],
      },
    },
  },
  maxUsageCount: 5,
};

// READ only. The board must be fully readable at READ — that is the whole point
// of mounting it outside the VOTE-gated curation tree.
const readAuth = {
  authenticated: true,
  user: { id: "u1", name: "alice", roles: [RoleEnum.READ] },
};

const voterAuth = {
  authenticated: true,
  user: { id: "u1", name: "alice", roles: [RoleEnum.VOTE] },
};

describe("identification board — reachable content", () => {
  it("renders the open questions with their text and vote counts", async () => {
    // MUTATION: blank out `query.description` in the board's row rendering ->
    // this goes red. A board that renders ids instead of what people asked is
    // not a board.
    renderForm(<IdentificationBoard />, {
      auth: readAuth,
      mocks: [configMock, boardMock],
      route: "/identification",
    });

    expect(
      await screen.findByText("hotel room, rainy night, around 2016"),
    ).toBeInTheDocument();
    // The tally is summed across candidates and rendered as a phrase, not as a
    // bare number -- MUTATION: render `votes` without the noun -> this goes red
    // on the /\d+ votes/ match rather than passing on a stray "3" elsewhere on
    // the page, which is the mistake the first draft of this test made.
    expect(screen.getByText(/\b3 votes\b/)).toBeInTheDocument();
  });

  it("links each question at its own detail route, with the id in the href", async () => {
    // MUTATION: replace(":id", query.id) with replace(":id", "") -> this goes red.
    //
    // Found by mutation, not by reading: the href is the whole reachability
    // claim for the board, and a link that drops the id lands the user on the
    // list page looking at a board they already had. Nothing else in the suite
    // noticed, because the description still renders either way.
    renderForm(<IdentificationBoard />, {
      auth: readAuth,
      mocks: [configMock, boardMock],
      route: "/identification",
    });

    const link = await screen.findByRole("link", {
      name: /hotel room, rainy night, around 2016/i,
    });
    expect(link).toHaveAttribute("href", "/identification/q1");
  });

  it("says plainly when nothing is open", async () => {
    // MUTATION: delete this branch -> the empty state renders nothing and the
    // page looks broken rather than quiet.
    renderForm(<IdentificationBoard />, {
      auth: readAuth,
      mocks: [
        configMock,
        {
          request: {
            query: IdentificationBoardDocument,
            variables: { limit: 100 },
          },
          result: { data: { listOpenIdentificationQueries: [] } },
          maxUsageCount: 5,
        },
      ],
      route: "/identification",
    });

    expect(await screen.findByText(/nothing open/i)).toBeInTheDocument();
  });

  it("shows a candidate's note, because that is what makes it a suggestion", async () => {
    // MUTATION: remove the `candidate.note` block -> this goes red.
    //
    // The schema calls this out: "a candidate with a reason is the difference
    // between a suggestion and a guess". A board that shows only the entity has
    // thrown away the most useful thing on the card.
    renderForm(<IdentificationQuery id="q1" />, {
      auth: readAuth,
      mocks: [configMock, queryMock],
      route: "/identification/q1",
    });

    expect(
      await screen.findByText(/watermark is visible in frame 3/i),
    ).toBeInTheDocument();
  });

  it("links a candidate to the entity it names", async () => {
    // MUTATION: drop the <Link> around the entity name -> this goes red.
    renderForm(<IdentificationQuery id="q1" />, {
      auth: readAuth,
      mocks: [configMock, queryMock],
      route: "/identification/q1",
    });

    const link = await screen.findByRole("link", {
      name: /someone remembered/i,
    });
    expect(link).toHaveAttribute("href", "/performers/p9");
  });

  it("renders a candidate whose entity has been deleted without a dead link", async () => {
    // The schema makes `entity` nullable because a board outlives its questions.
    // A null entity must read as information, not as an empty link.
    //
    // MUTATION: render the null case as the link branch -> this goes red.
    renderForm(<IdentificationQuery id="q1" />, {
      auth: readAuth,
      mocks: [
        configMock,
        {
          ...queryMock,
          result: {
            data: {
              identificationQuery: {
                ...queryMock.result.data.identificationQuery,
                candidates: [
                  {
                    ...queryMock.result.data.identificationQuery.candidates[0],
                    entity: null,
                  },
                ],
              },
            },
          },
        },
      ],
      route: "/identification/q1",
    });

    expect(await screen.findByText(/no longer exists/i)).toBeInTheDocument();
  });
});

describe("identification board — role gating", () => {
  it("offers a vote button to a voter", async () => {
    // MUTATION: invert the canVote branch in Candidate -> this goes red.
    renderForm(<IdentificationQuery id="q1" />, {
      auth: voterAuth,
      mocks: [configMock, queryMock],
      route: "/identification/q1",
    });

    expect(
      await screen.findByRole("button", { name: /vote/i }),
    ).toBeInTheDocument();
  });

  it("offers NO vote button to a read-only user", async () => {
    // The load-bearing role test. `voteIdentificationCandidate` is
    // @hasRole(role: VOTE), so a button rendered for a READ user fails on click
    // with "not authorized" — and a control that fails looks like a broken board
    // rather than a role boundary.
    //
    // MUTATION: drop `canVote &&` around the Button in Candidate -> this goes RED
    // only if the mock list omits the vote mutation, because without a mock
    // MockedProvider errors rather than rendering a dead control. That is the
    // point: the assertion is on what is rendered, and a dead control is the bug.
    renderForm(<IdentificationQuery id="q1" />, {
      auth: readAuth,
      mocks: [configMock, queryMock],
      route: "/identification/q1",
    });

    await screen.findByText(/watermark is visible in frame 3/i);

    expect(screen.queryByRole("button", { name: /vote/i })).toBeNull();
    expect(screen.getByText(/needs the voter role/i)).toBeInTheDocument();
  });

  it("shows 'Voted' as a state once a vote is cast", async () => {
    // MUTATION: always render the Vote button regardless of votedByMe -> red.
    //
    // A vote button that silently does nothing on a second tap is worse than one
    // showing the vote is already cast: the schema says voting twice is a
    // constraint violation, so the second tap errors instead of doing nothing.
    renderForm(<IdentificationQuery id="q1" />, {
      auth: voterAuth,
      mocks: [
        configMock,
        {
          ...queryMock,
          result: {
            data: {
              identificationQuery: {
                ...queryMock.result.data.identificationQuery,
                candidates: [
                  {
                    ...queryMock.result.data.identificationQuery.candidates[0],
                    votedByMe: true,
                  },
                ],
              },
            },
          },
        },
      ],
      route: "/identification/q1",
    });

    expect(await screen.findByText(/voted/i)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /vote/i })).toBeNull();
  });

  it("does not expose a resolve control anywhere on the page", async () => {
    // The board's central rule: a candidate is a SUGGESTION. The schema has
    // `resolveIdentificationQuery`, and this UI deliberately does not call it —
    // resolving needs a named human and a full thread, not a one-click button
    // beside a list of guesses.
    //
    // MUTATION: add a resolve Button -> this goes red.
    renderForm(<IdentificationQuery id="q1" />, {
      auth: voterAuth,
      mocks: [configMock, queryMock],
      route: "/identification/q1",
    });

    await screen.findByRole("button", { name: /vote/i });

    expect(screen.queryByRole("button", { name: /resolve/i })).toBeNull();
    expect(screen.queryByText(/mark as solved/i)).toBeNull();
  });
});

describe("identification board — no coercive framing", () => {
  it("states a question's age without turning it into a deadline", async () => {
    // §7.25 and the StreakCard contract: no manufactured urgency, no
    // loss-framing. A board that said "open 40 days" pressures the reader and
    // punishes nobody, which is the worst of both.
    //
    // MUTATION: change formatAge to render "N days overdue" -> this goes red.
    renderForm(<IdentificationBoard />, {
      auth: readAuth,
      mocks: [configMock, boardMock],
      route: "/identification",
    });

    await screen.findByText("hotel room, rainy night, around 2016");

    const body = document.body.textContent ?? "";
    expect(body).not.toMatch(/overdue/i);
    expect(body).not.toMatch(/urgently/i);
    expect(body).not.toMatch(/no one has answered in/i);
  });
});

// Referenced so the document import is not flagged as unused by a stricter
// config later: the mutation this page does NOT expose is still named here,
// because a future edit reaching for it should find this comment.
void VoteIdentificationCandidateDocument;
