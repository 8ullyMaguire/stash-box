import { screen } from "@testing-library/react";
import type { ComponentProps } from "react";
import {
  ConfigDocument,
  CurationDashboardDocument,
  DEFAULT_COMPLETION_THRESHOLD,
  RoleEnum,
  UserStreakDocument,
} from "src/graphql";
import CurationDashboard from "src/pages/curation/CurationDashboard";
import { renderForm } from "src/test/renderForm";
import { describe, expect, it } from "vitest";
import UserComponent from "../User";

// Reachability, not rendering (SPEC docs/spec/feature-streak-placement.md).
//
// The bug these exist for is a fact that renders perfectly but cannot be
// reached: the streak was selected inside CurationDashboard.gql, and the only
// route to that query was /curation, which is gated on VOTE, while `userStreak`
// itself is gated on READ. A component test cannot catch that — it mounts the
// component directly, which is precisely the thing production never does. So
// every test here is about a LINK or a GATE, and each is paired with the
// mutation that must turn it red.

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

const streakMock = {
  request: { query: UserStreakDocument },
  result: {
    data: {
      userStreak: {
        __typename: "Streak",
        currentStreak: 4,
        longestStreak: 17,
        totalActiveDays: 88,
        activeToday: false,
        lastActiveDay: "2026-10-01",
      },
    },
  },
  maxUsageCount: 5,
};

// A private user: the fields User.tsx reads when showPrivate is true.
// tsc wants the __typename on every generated selection, which a hand-written
// fixture cannot reasonably carry for edit_count's thirteen fields. The cast is
// deliberate and narrow: the fixture is passed to a component that reads named
// fields, and the alternative -- building it out of generated types -- would make
// this fixture break every time codegen reshapes the User selection, which is
// exactly the churn a reachability test should not have.
const privateAlice = {
  id: "u-alice",
  name: "alice",
  email: "alice@example.test",
  roles: [RoleEnum.READ],
  api_key: "key",
  api_calls: 0,
  invited_by: null,
  invite_tokens: 0,
  invite_codes: [],
  vote_count: {
    accept: 0,
    reject: 0,
    immediate_accept: 0,
    immediate_reject: 0,
    abstain: 0,
  },
  edit_count: {
    immediate_accepted: 0,
    immediate_rejected: 0,
    accepted: 0,
    rejected: 0,
    failed: 0,
    canceled: 0,
    pending: 0,
    immediate_accepted_bot: 0,
    immediate_rejected_bot: 0,
    accepted_bot: 0,
    rejected_bot: 0,
    failed_bot: 0,
    canceled_bot: 0,
    pending_bot: 0,
  },
  notification_subscriptions: [],
  image_type_preferences: [],
  image_type_group_preferences: [],
};

const aliceAuth = {
  authenticated: true,
  user: { id: "u-alice", name: "alice", roles: [RoleEnum.READ] },
};

const bobAuth = {
  authenticated: true,
  user: { id: "u-bob", name: "bob", roles: [RoleEnum.READ] },
};

// The streak's headline number. Used as the "the card is here" probe, because it
// is the one string unique to StreakCard's content.
const STREAK_HEADLINE = "88";

describe("streak placement", () => {
  it("shows the streak on the viewer's own profile", async () => {
    // MUTATION: delete the {isSelf(user) && <StreakCard .../>} block from
    // User.tsx -> this goes red.
    renderForm(
      <UserComponent
        user={
          privateAlice as unknown as ComponentProps<
            typeof UserComponent
          >["user"]
        }
        refetch={() => {}}
      />,
      {
        auth: aliceAuth,
        mocks: [configMock, streakMock],
      },
    );

    // Count before asserting absence later: an absence assertion with no count
    // first is vacuously true if the card never renders at all.
    expect(
      await screen.findByText(/active days, all time/i),
    ).toBeInTheDocument();
    expect(screen.getByText(STREAK_HEADLINE)).toBeInTheDocument();
  });

  it("does not show a streak card on another user's profile", async () => {
    // MUTATION: remove BOTH guards — `!isSelf(user)` from the useUserStreak call
    // and the {isSelf(user) &&} around the JSX -> this goes red.
    //
    // Two wrong turns here are worth recording, because both produced a test that
    // passed while asserting nothing:
    //
    // 1. Removing only the JSX guard is caught by NOTHING. With the query
    //    skipped there is no data, and StreakCard returns null on `!streak`, so
    //    the card is invisible either way.
    // 2. Removing both guards is also caught by nothing — but only as long as
    //    this render supplies no streak mock. Absent data, absent card; the test
    //    passes for the third time for the wrong reason.
    //
    // So the leak is asserted with the streak data PRESENT. streakMock is
    // included here deliberately: with both guards gone, bob's profile would
    // render alice's streak numbers, and this fails on that. The `skip` flag is
    // the thing actually under test — it is what prevents the request, and it is
    // the only guard that can, since the card renders nothing without data.
    //
    // What must not happen: bob viewing alice sees her contribution history.
    // `userStreak` takes no id: precisely so this is unexpressible in the API,
    // and a client-side guard that fails would reintroduce it at the front end.
    renderForm(
      <UserComponent
        user={
          privateAlice as unknown as ComponentProps<
            typeof UserComponent
          >["user"]
        }
        refetch={() => {}}
      />,
      {
        auth: bobAuth,
        mocks: [configMock, streakMock],
      },
    );

    // Wait for something only this page renders, so the absence below is
    // asserted against a loaded page and not against an empty container.
    await screen.findByText("alice");

    // AND then wait for the streak query to settle before asserting it is
    // absent. This is the part that is easy to get wrong: the card mounts in a
    // later paint than the name does, so asserting right after findByText races
    // the render and passes whatever the guards do. Under the both-guards-removed
    // mutation this exact test passed while the page was visibly showing alice's
    // streak — a race, not a green.
    //
    // MockedProvider records no query when one is skipped, so the observable
    // signal for "the streak request was never made" is that nothing settles.
    // Waiting a beat and re-asserting is what turns the race into a check.
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(screen.queryByText(/active days, all time/i)).toBeNull();
    expect(screen.queryByText(STREAK_HEADLINE)).toBeNull();
  });

  it("reaches the streak without the VOTE role", async () => {
    // The reported bug, restated as the thing that must hold.
    //
    // aliceAuth holds READ only — no VOTE — and the profile still renders the
    // streak. Under the old wiring this was impossible: the streak rode in on
    // CurationDashboard.gql, whose route is gated on canVote.
    //
    // MUTATION: wrap the streak in canVote(user) && -> this goes red.
    expect(aliceAuth.user.roles).toEqual([RoleEnum.READ]);
    expect(aliceAuth.user.roles).not.toContain(RoleEnum.VOTE);

    renderForm(
      <UserComponent
        user={
          privateAlice as unknown as ComponentProps<
            typeof UserComponent
          >["user"]
        }
        refetch={() => {}}
      />,
      {
        auth: aliceAuth,
        mocks: [configMock, streakMock],
      },
    );

    expect(await screen.findByText(STREAK_HEADLINE)).toBeInTheDocument();
  });

  it("keeps a streak copy on the curation dashboard", async () => {
    // MUTATION: delete <StreakCard streak={streakData?.userStreak} .../> from
    // CurationDashboard.tsx -> this goes red.
    //
    // The curation copy is not redundancy. /curation is role-gated and the
    // profile is not, so for a READ-only user the profile is the only copy, and
    // for a curator arriving to vote the dashboard is the expected place. Both
    // read one query and one component so they cannot drift apart.
    renderForm(<CurationDashboard />, {
      auth: aliceAuth,
      mocks: [
        configMock,
        streakMock,
        {
          request: {
            query: CurationDashboardDocument,
            variables: { threshold: DEFAULT_COMPLETION_THRESHOLD },
          },
          result: {
            data: {
              performersBelow: 1,
              scenesBelow: 2,
              studiosBelow: 3,
              sitesBelow: 4,
              tagsBelow: 5,
            },
          },
          maxUsageCount: 5,
        },
      ],
    });

    expect(await screen.findByText(STREAK_HEADLINE)).toBeInTheDocument();
  });
});

describe("feature reachability", () => {
  it("links notification preferences from the profile", async () => {
    // MUTATION: delete the Notification Preferences <Link> from User.tsx -> red.
    //
    // Audit finding C: /users/:name/notifications was mounted with a working page
    // and linked from nowhere, so it was reachable only by typing the URL.
    renderForm(
      <UserComponent
        user={
          privateAlice as unknown as ComponentProps<
            typeof UserComponent
          >["user"]
        }
        refetch={() => {}}
      />,
      {
        auth: aliceAuth,
        mocks: [configMock, streakMock],
      },
    );

    const link = await screen.findByRole("link", {
      name: /notification preferences/i,
    });
    expect(link).toHaveAttribute("href", "/users/alice/notifications");
  });

  it("reaches the self-service pages that have no other entry point", async () => {
    // A count first, then each absence. Without the count an empty page would
    // satisfy every one of these trivially.
    renderForm(
      <UserComponent
        user={
          privateAlice as unknown as ComponentProps<
            typeof UserComponent
          >["user"]
        }
        refetch={() => {}}
      />,
      {
        auth: aliceAuth,
        mocks: [configMock, streakMock],
      },
    );

    await screen.findByRole("link", { name: /image preferences/i });

    expect(
      screen.getByRole("link", { name: /my fingerprints/i }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: /change password/i }),
    ).toBeInTheDocument();
  });

  it("does not show another user's self-service buttons", async () => {
    // The negative half of the previous test. Self-service actions for someone
    // else would be a privilege bug, and it is the same isOwner gate.
    renderForm(
      <UserComponent
        user={
          privateAlice as unknown as ComponentProps<
            typeof UserComponent
          >["user"]
        }
        refetch={() => {}}
      />,
      {
        auth: bobAuth,
        mocks: [configMock],
      },
    );

    await screen.findByText("alice");

    expect(
      screen.queryByRole("link", { name: /notification preferences/i }),
    ).toBeNull();
    expect(screen.queryByRole("link", { name: /my fingerprints/i })).toBeNull();
  });
});
