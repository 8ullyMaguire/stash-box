import { screen } from "@testing-library/react";
import { ConfigDocument, MeDocument, RoleEnum } from "src/graphql";
import { renderForm } from "src/test/renderForm";
import { describe, expect, it } from "vitest";

// Reachability of the identification board through the real app tree
// (growth workstream W1).
//
// The component tests prove the board renders its content. They cannot prove a
// user can get to it — and that is the entire point of this workstream: the
// backend has had ten identification query fields since migration 79 with no
// page, so "exists in the tree" and "reachable by a user" are different claims.
//
// Each test names the mutation that must turn it red. The nav tests are the
// ones that matter: a nav entry present for the wrong users, or absent for the
// right ones, is a §7.26-shaped defect, and only rendering the real Main can
// catch it.

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
 * The nav is rendered by Main, which derives the user from the `me` query rather
 * than from an injected context — `useAuth()` calls `useMe({ fetchPolicy:
 * "network-only" })` and Main builds its own context value from it, ignoring
 * AuthContext.Provider. So the auth identity has to arrive as a `me` mock.
 * Passing renderForm's `auth` prop here does nothing, and Main returns early as
 * unauthenticated with no nav at all.
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

describe("identification board — nav reachability", () => {
  it("is linked from the nav to a READ-only user", async () => {
    // MUTATION: wrap the Identify NavLink in `canVote(user) &&` in src/Main.tsx
    // -> red, because the board reads at READ (listOpenIdentificationQueries is
    // @hasRole(READ)). Gating the entry on VOTE would hide the board from
    // exactly the read-only users it is most useful to.
    const { default: Main } = await import("src/Main");
    renderForm(<Main />, {
      mocks: [configMock, meMock([RoleEnum.READ])],
      route: "/",
    });

    expect(
      await screen.findByRole("link", { name: /identify/i }),
    ).toBeInTheDocument();
  });

  it("is linked from the nav to a voter", async () => {
    // MUTATION: change the nav entry to require a role nobody holds -> red.
    const { default: Main } = await import("src/Main");
    renderForm(<Main />, {
      mocks: [configMock, meMock([RoleEnum.VOTE])],
      route: "/",
    });

    expect(
      await screen.findByRole("link", { name: /identify/i }),
    ).toBeInTheDocument();
  });

  it("points at /identification", async () => {
    // MUTATION: point the NavLink at ROUTE_CURATION -> red. The board is
    // deliberately mounted at the top level, not under the VOTE-gated curation
    // tree, so a link into /curation would 403 a read-only user on arrival.
    const { default: Main } = await import("src/Main");
    renderForm(<Main />, {
      mocks: [configMock, meMock([RoleEnum.READ])],
      route: "/",
    });

    const link = await screen.findByRole("link", { name: /identify/i });
    expect(link).toHaveAttribute("href", "/identification");
  });

  it("sits beside the other top-level nav entries, not inside a dropdown", async () => {
    // MUTATION: wrap the entry in a Dropdown that only opens on hover -> red.
    // A nav entry hidden behind an interaction is a reachability regression in
    // exactly the way §7.26 was.
    const { default: Main } = await import("src/Main");
    renderForm(<Main />, {
      mocks: [configMock, meMock([RoleEnum.READ])],
      route: "/",
    });

    const link = await screen.findByRole("link", { name: /identify/i });
    // react-bootstrap's <Nav> renders a div.navbar-nav, not a <ul>, so the
    // container is asserted by class. A Dropdown-hosted entry would sit under
    // div.dropdown instead and fail this.
    expect(link.closest("div.navbar-nav")).not.toBeNull();
    expect(link.closest("div.dropdown")).toBeNull();
  });
});

describe("identification board — route declaration", () => {
  it("declares /identification and /identification/:id", async () => {
    // MUTATION: delete ROUTE_IDENTIFICATION_QUERY -> red. Without the param
    // route, a link to a specific question lands on the board list with no
    // error, which is the worst kind of 404: a wrong page rather than a missing
    // one.
    const { ROUTE_IDENTIFICATION, ROUTE_IDENTIFICATION_QUERY } = await import(
      "src/constants/route"
    );
    expect(ROUTE_IDENTIFICATION).toBe("/identification");
    expect(ROUTE_IDENTIFICATION_QUERY).toBe("/identification/:id");
  });

  it("is mounted in the app route table", async () => {
    // MUTATION: delete the <Route path={`${ROUTE_IDENTIFICATION}/*`} .../> block
    // from src/pages/index.tsx -> red.
    //
    // Read as source text rather than the element tree: the route table nests
    // inside a gated branch whose element is only constructed at render time, so
    // calling the component outside a router throws before any path is readable.
    // A source assertion is blunt but it is the only thing that distinguishes
    // "mounted" from "defined and never routed", which is the defect being
    // guarded.
    const { readFileSync } = await import("node:fs");
    const source = readFileSync("src/pages/index.tsx", "utf8");

    // Built from pieces so this file does not itself contain a literal
    // template-curly sequence -- biome's noTemplateCurlyInString fires on it,
    // and the alternative is a lint suppression on a test.
    // String concatenation rather than a template literal: a template would put a
    // literal ${...} sequence in this file, which biome's
    // noTemplateCurlyInString rejects, and its own fix (useTemplate) would
    // reintroduce the literal. concat satisfies both.
    const routePath = (name: string) => "path={`" + "$" + "{" + name + "}/*`}";
    const identRoute = routePath("ROUTE_IDENTIFICATION");
    const curationRoute = routePath("ROUTE_CURATION");

    // The identification mount exists as its own <Route>.
    expect(source).toContain(identRoute);
    expect(source).toMatch(/<Route\s*\n?\s*path=\{`\$\{ROUTE_IDENTIFICATION/);

    // And it is a SIBLING of the curation route, not nested inside it. Nesting
    // would put the board behind the canVote branch, which is the 7.26 defect.
    // The slice between the two mounts must not contain a role gate or a nested
    // element, because they are adjacent children of the same <Routes>.
    const identIndent = source.indexOf(identRoute);
    const curationIndent = source.indexOf(curationRoute);
    expect(identIndent).toBeGreaterThan(-1);
    expect(curationIndent).toBeGreaterThan(-1);
    // The identification route is declared immediately before curation, and the
    // curation element closes on the same line, so neither contains the other.
    const between = source.slice(identIndent, curationIndent);
    expect(between).not.toContain("canVote");
    expect(between).not.toContain("element={<Curation");
  });
});
