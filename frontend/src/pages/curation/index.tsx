import type { FC } from "react";
import { Route, Routes } from "react-router-dom";
import Title from "src/components/title";
import {
  ROUTE_CURATION_LEADERBOARD,
  ROUTE_CURATION_MATCHUP,
} from "src/constants/route";
import CurationDashboard from "./CurationDashboard";
import EloLeaderboardPage from "./EloLeaderboardPage";
import MatchupVote from "./MatchupVote";

const CurationRoutes: FC = () => (
  <Routes>
    <Route
      path={ROUTE_CURATION_MATCHUP}
      element={
        <>
          <Title page="Compare Performers" />
          <MatchupVote />
        </>
      }
    />
    <Route
      path={ROUTE_CURATION_LEADERBOARD}
      element={
        <>
          <Title page="Performer Leaderboard" />
          <EloLeaderboardPage />
        </>
      }
    />
    <Route
      path="/*"
      element={
        <>
          <Title page="Curation" />
          <CurationDashboard />
        </>
      }
    />
  </Routes>
);

export default CurationRoutes;
