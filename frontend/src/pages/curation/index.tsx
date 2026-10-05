import type { FC } from "react";
import { Route, Routes } from "react-router-dom";
import Title from "src/components/title";
import { ROUTE_CURATION_MATCHUP } from "src/constants/route";
import CurationDashboard from "./CurationDashboard";
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
