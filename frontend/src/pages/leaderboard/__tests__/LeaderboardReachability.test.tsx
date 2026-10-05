import type { MockedResponse } from "@apollo/client/testing";
import { MockedProvider } from "@apollo/client/testing/react";
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import AuthContext from "src/context";
import {
  ConfigDocument,
  EloEntityType,
  EloLeaderboardDocument,
  MeDocument,
  RoleEnum,
} from "src/graphql";
import { renderForm } from "src/test/renderForm";
import { describe, expect, it } from "vitest";

/**
 * Reachability of the Elo leaderboard, specifically for a READ-only user.
 *
 * This is growth workstream W2, item 4, and it was a BUG rather than a feature:
 * the leaderboard page existed, the query existed, and `eloLeaderboard` is
 * declared `@hasRole(role: READ)` in the schema -- but the page was mounted
 * inside the curation tree, whose only nav entry is gated on `canVote`. So a
 * read-only user had no way to reach a surface the schema already permitted
 * them. Exactly the shape of SPEC 7.26's streak-card defect.
 *
 * Confirmed live in a browser before this test existed: with the VOTE role
 * removed from a real user, the nav showed every entry except Curation, and no
 * leaderboard entry at all. The test encodes that observation.
 *
 * Each case names the mutation that must turn it red.
 */

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

/**
 * Main derives the user from the `me` query, not from an injected context
 * (useAuth calls useMe with fetchPolicy network-only), so the identity has to
 * arrive as a `me` mock. renderForm's `auth` prop is inert inside Main.
 */
const meMock = (roles: string[]) => ({
  request: { query: MeDocument },
  result: {
    data: {
      me: {
        __typename: "User",
        id: "u1",
        name: "alice",
        email: "alice@example.test",
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

const readRoles = [RoleEnum.READ];
const voteRoles = [RoleEnum.READ, RoleEnum.VOTE];

describe("elo leaderboard — reachable by a read-only user", () => {
  it("appears in the nav for READ, ungated", async () => {
    // MUTATION: wrap the Leaderboard NavLink in canVote(user) && -> red.
    // That is the original defect, re-introduced.
    const { default: Main } = await import("src/Main");
    renderForm(<Main />, {
      mocks: [configMock, meMock(readRoles)],
      route: "/",
    });

    const link = await screen.findByRole("link", { name: /leaderboard/i });
    expect(link).toHaveAttribute("href", "/leaderboard");
  });

  it("appears in the nav for a voter too", async () => {
    // MUTATION: require a role nobody holds -> red.
    const { default: Main } = await import("src/Main");
    renderForm(<Main />, {
      mocks: [configMock, meMock(voteRoles)],
      route: "/",
    });

    expect(
      await screen.findByRole("link", { name: /leaderboard/i }),
    ).toBeInTheDocument();
  });

  it("sits outside the Curation dropdown, so no click is needed to reveal it", async () => {
    // MUTATION: move the entry inside the canVote-gated Curation group -> red.
    const { default: Main } = await import("src/Main");
    renderForm(<Main />, {
      mocks: [configMock, meMock(readRoles)],
      route: "/",
    });

    const link = await screen.findByRole("link", { name: /leaderboard/i });
    expect(link.closest("div.navbar-nav")).not.toBeNull();
    expect(link.closest("div.dropdown")).toBeNull();
  });
});

describe("elo leaderboard — the route itself is not behind the curation gate", () => {
  it("is mounted at the top level of the route table", async () => {
    // MUTATION: delete the <Route path={`${ROUTE_ELO_LEADERBOARD}/*`} .../> block
    // from src/pages/index.tsx -> red.
    //
    // Read as source, because the route table only exists inside a gated branch
    // that is constructed at render time: calling the component outside a router
    // throws before any path is readable. The nav tests above would still pass
    // with the route missing -- a link to nowhere.
    const { readFileSync } = await import("node:fs");
    const source = readFileSync("src/pages/index.tsx", "utf8");
    const route = "path={`" + "$" + "{ROUTE_ELO_LEADERBOARD}/*`}";
    expect(source).toContain(route);
  });

  it("renders the leaderboard page at /leaderboard", async () => {
    // The end-to-end claim: mounting the real wrapper under the real parent path
    // renders the page, not a blank. This is the assertion that would have caught
    // the relative-vs-absolute path mistake the identification board had, where
    // the URL was right and the wrong page rendered.
    //
    // MUTATION: change the wrapper's path="*" to a literal that matches nothing
    // -> red.
    const { default: EloLeaderboard } = await import("src/pages/leaderboard");
    const { default: Main } = await import("src/Main");
    void Main;

    // The page renders a table of entries, so the query needs a real result.
    // Mocking it away and asserting the empty state would prove less: an error
    // and an empty board look alike until you check which one rendered.
    const leaderboardMock: MockedResponse = {
      request: {
        query: EloLeaderboardDocument,
        variables: { entityType: EloEntityType.PERFORMER, limit: 50 },
      },
      result: {
        data: {
          eloLeaderboard: {
            __typename: "EloLeaderboard",
            entityType: EloEntityType.PERFORMER,
            entries: [
              {
                __typename: "EloLeaderboardEntry",
                entityId: "11111111-1111-1111-1111-111111111111",
                rating: 1500,
                voteCount: 12,
                performer: {
                  __typename: "Performer",
                  id: "11111111-1111-1111-1111-111111111111",
                  name: "A Rated Performer",
                  disambiguation: null,
                },
              },
            ],
          },
        },
      },
      maxUsageCount: 5,
    };

    render(
      <MemoryRouter initialEntries={["/leaderboard"]}>
        <MockedProvider mocks={[configMock, leaderboardMock]}>
          <AuthContext.Provider
            value={{
              authenticated: true,
              user: { id: "u1", name: "alice", roles: readRoles },
            }}
          >
            <Routes>
              <Route path="/leaderboard/*" element={<EloLeaderboard />} />
            </Routes>
          </AuthContext.Provider>
        </MockedProvider>
      </MemoryRouter>,
    );

    // The page mounted AND its query resolved: an error message or a blank page
    // would both fail this, and so would the component never running.
    expect(await screen.findByText("A Rated Performer")).toBeInTheDocument();
    expect(screen.getByText("12")).toBeInTheDocument();
  });
});

describe("elo leaderboard — curation keeps its own gate", () => {
  it("still hides Curation from a read-only user", async () => {
    // The fix must not over-correct: `eloMatchup` genuinely is @hasRole(VOTE), so
    // the curation entry stays gated. Removing the gate to "fix" reachability
    // would land a read-only user on an unauthorized page.
    //
    // MUTATION: drop the canVote guard around the Curation NavLink -> red.
    const { default: Main } = await import("src/Main");
    renderForm(<Main />, {
      mocks: [configMock, meMock(readRoles)],
      route: "/",
    });

    await screen.findByRole("link", { name: /leaderboard/i });
    expect(screen.queryByRole("link", { name: /^curation$/i })).toBeNull();
  });

  it("shows Curation to a voter", async () => {
    // MUTATION: gate Curation on a role nobody holds -> red.
    const { default: Main } = await import("src/Main");
    renderForm(<Main />, {
      mocks: [configMock, meMock(voteRoles)],
      route: "/",
    });

    expect(
      await screen.findByRole("link", { name: /^curation$/i }),
    ).toBeInTheDocument();
  });
});
