import type { FC } from "react";
import { Route, Routes, useParams } from "react-router-dom";
import Title from "src/components/title";
import { ROUTE_IDENTIFICATION_QUERY } from "src/constants/route";
import IdentificationBoard from "./IdentificationBoard";
import IdentificationQuery from "./IdentificationQuery";

/**
 * Route wrapper for the identification board.
 *
 * The id is read here and passed down, rather than the page calling useParams.
 * Two reasons: the test harness's MemoryRouter has no <Routes>, so useParams is
 * undefined under test and a page built on it looks wired in a test while being
 * broken in the app; and the page then has no reason to know about routing.
 *
 * The Title lives here too, so a nested route can be named without either page
 * rendering its own <h1> — the heading requirement in this project is satisfied
 * from one place.
 */
const QueryRoute: FC = () => {
  const { id } = useParams<{ id: string }>();
  // ROUTE_IDENTIFICATION_QUERY is "/identification/:id", so this wrapper is the
  // one place that knows the param name. Mounted without a param, fall back to
  // the board rather than rendering an error.
  if (!id) return <IdentificationBoard />;

  return (
    <>
      <Title page="Identification" />
      <IdentificationQuery id={id} />
    </>
  );
};

const IdentificationRoutes: FC = () => (
  <Routes>
    <Route path=":id" element={<QueryRoute />} />
    <Route
      path="*"
      element={
        <>
          <Title page="Identification" />
          <IdentificationBoard />
        </>
      }
    />
  </Routes>
);

export default IdentificationRoutes;
