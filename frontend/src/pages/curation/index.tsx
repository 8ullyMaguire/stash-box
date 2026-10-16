import type { FC } from "react";
import { Route, Routes } from "react-router-dom";
import Title from "src/components/title";
import CurationDashboard from "./CurationDashboard";
import LintQuestBoard from "./LintQuestBoard";
import MatchupVote from "./MatchupVote";

const CurationRoutes: FC = () => (
  <Routes>
    {/*
      The paths here are RELATIVE to this area's parent splat route, which is why
      they do not use the ROUTE_CURATION_* constants: those are absolute URLs, for
      `<Link to>`, and a Link and a Route take different forms of the same
      destination. That asymmetry is what hid a shipped defect --

      both nested routes below were declared with the ABSOLUTE constant, so neither
      matched, and every /curation/<anything> URL fell through to the `/*` child
      and rendered the dashboard. /curation/matchup has never once rendered the
      matchup UI, and nothing caught it because no test visited it. This walk found
      it only because a new page was asked for and did not appear.

      The leading slash is React Router's own relative form (it is how the rest of
      this codebase writes nested children: `path="/"`, `path="/:id/*"`), and it
      stays inside the parent.
    */}
    <Route
      path="/matchup"
      element={
        <>
          <Title page="Compare Performers" />
          <MatchupVote />
        </>
      }
    />
    <Route
      path="/lint"
      element={
        <>
          <Title page="Data lint" />
          <LintQuestBoard />
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
