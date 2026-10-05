import type { FC } from "react";
import { Route, Routes } from "react-router-dom";
import Title from "src/components/title";
import EloLeaderboardPage from "src/pages/curation/EloLeaderboardPage";

/**
 * Top-level leaderboard route.
 *
 * The leaderboard USED to live inside the curation tree, which is gated on
 * canVote in Main.tsx -- so a READ-only user had no way to see a leaderboard at
 * all. That is the same reachability defect as SPEC 7.26 (the streak card) and
 * the reason this file exists rather than a nav link.
 *
 * The schema already allows it: `eloLeaderboard` is @hasRole(READ) and only
 * `eloMatchup` is @hasRole(VOTE) (graphql/schema/types/elo.graphql:122,134). The
 * backend and the page were both fine; only the routing hid a permitted surface
 * behind a role it does not need. So the curation route is untouched and still
 * requires VOTE, which is correct for the matchup it exists to host.
 *
 * The relative-vs-absolute path shape is the same trap as the identification
 * board's: this is mounted as `${ROUTE_ELO_LEADERBOARD}/*`, so the child path is
 * the remainder. `path="*"` is the page.
 */
const EloRoutes: FC = () => (
  <Routes>
    <Route
      path="*"
      element={
        <>
          <Title page="Leaderboard" />
          <EloLeaderboardPage />
        </>
      }
    />
  </Routes>
);

export default EloRoutes;
