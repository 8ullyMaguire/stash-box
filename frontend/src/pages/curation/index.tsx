import type { FC } from "react";
import { Route, Routes } from "react-router-dom";
import Title from "src/components/title";
import CurationDashboard from "./CurationDashboard";
import FingerprintDemandBoard from "./FingerprintDemandBoard";
import LintQuestBoard from "./LintQuestBoard";
import LintQuestDefinitions from "./LintQuestDefinitions";
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
    {/*
      The registry sits UNDER the board's path rather than beside it, because it is
      the same subject at a different audience: /curation/lint is the work, and this
      is the table the work comes from. React Router ranks the longer static path
      first, so the order of these two entries does not decide anything.
    */}
    <Route
      path="/lint/definitions"
      element={
        <>
          <Title page="Detector registry" />
          <LintQuestDefinitions />
        </>
      }
    />
    {/* Relative, like every other nested child here: see the note above about the
        absolute constants that matched nothing. The demand board sits BESIDE the
        lint board rather than under it -- the two are different questions (what we
        have got wrong, and what we have not got at all). */}
    <Route
      path="/fingerprint-demand"
      element={
        <>
          <Title page="Fingerprint demand" />
          <FingerprintDemandBoard />
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
