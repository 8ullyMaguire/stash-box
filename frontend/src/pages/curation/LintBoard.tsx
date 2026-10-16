import type { FC } from "react";
import { Alert, Badge, Card, ListGroup } from "react-bootstrap";
import { Link } from "react-router-dom";
import { ROUTE_PERFORMER, ROUTE_SCENE } from "src/constants/route";
import type { LintQuestBoardQuery } from "src/graphql";

/**
 * One detector's findings: what it looks for, what it is worth, and the records it
 * found.
 *
 * The board exists to be ACTED ON by hand, and that is a design constraint rather
 * than a note. There is no "merge" and no "fix" on any row, and no mutation behind
 * this component: SPEC §7.24.4 says a detector emits a quest and never
 * auto-merges, because the merge path is the consensus flow, and an unattributable
 * merge is worse than the duplicate it would have fixed. Each candidate therefore
 * links to the record and stops there -- the curator's next click is the ordinary
 * edit or merge, with its ordinary votes.
 *
 * Everything here is re-derived from the archive on every read, so a finding a
 * curator has just fixed is gone on the next load. Nothing on this page is stored,
 * which is why there is no "dismissed" state to get out of date.
 */

type Reports = LintQuestBoardQuery["lintQuestBoard"];
type Report = Reports[number];
type Candidate = Report["candidates"][number];

// Only performers and scenes have a detail route the board can point at, and the
// map is deliberate rather than inferred from a string: a candidate for an entity
// kind with no page (or one this build does not route) renders WITHOUT a link
// instead of linking to a 404, which is the failure a "just build the path"
// implementation ships with.
//
// Hrefs use the route CONSTANTS rather than literal paths, so a route rename is a
// compile error here rather than a broken link discovered by a user.
const detailHref = (entityType: string, entityId: string): string | null => {
  switch (entityType) {
    case "performer":
      return ROUTE_PERFORMER.replace(":id", entityId);
    case "scene":
      return ROUTE_SCENE.replace(":id", entityId);
    default:
      return null;
  }
};

const CandidateRow: FC<{ entityType: string; candidate: Candidate }> = ({
  entityType,
  candidate,
}) => {
  const href = detailHref(entityType, candidate.entityId);
  const name = candidate.entityName || "(unnamed record)";

  return (
    <ListGroup.Item>
      <div>{href ? <Link to={href}>{name}</Link> : <span>{name}</span>}</div>
      {/* The detail is the detector's own sentence about WHY this is a finding --
          "dated 2015-01-01, before X born 2020" rather than a rule id -- so it is
          rendered as written rather than re-worded here. */}
      <div className="text-muted small">{candidate.detail}</div>
    </ListGroup.Item>
  );
};

const DetectorCard: FC<{ report: Report }> = ({ report }) => (
  <Card>
    <Card.Header className="d-flex justify-content-between align-items-center">
      <h4 className="mb-0">{report.description}</h4>
      <Badge bg="secondary">{report.bountyPoints} points each</Badge>
    </Card.Header>
    <Card.Body className="p-0">
      {report.count === 0 ? (
        // "Nothing found" is the ordinary state of a healthy detector, and it is
        // written plainly rather than as an error or as an absence: a card that
        // renders nothing at all would leave a reader unable to tell a clean
        // detector from one that failed to run.
        <p className="text-muted p-3 mb-0">
          Nothing found. That is a clean result, not a missing one.
        </p>
      ) : (
        <ListGroup variant="flush">
          {report.candidates.map((candidate) => (
            <CandidateRow
              key={`${report.entityType}-${candidate.entityId}`}
              entityType={report.entityType}
              candidate={candidate}
            />
          ))}
        </ListGroup>
      )}
    </Card.Body>
    {report.limitNotice && (
      // A truncated board must not look like a complete one: a detector showing
      // exactly its limit of a detector that found thousands reads as an archive
      // in good shape, which is the opposite of what it is.
      <Card.Footer className="text-muted small">
        {report.limitNotice}
      </Card.Footer>
    )}
  </Card>
);

/**
 * The data-lint quest board (SPEC §7.24.4).
 *
 * Presentational on purpose: the page fetches, this renders, and the tests render
 * it directly with the shapes the query produces. That is the only way the design
 * constraints below can be asserted without a server -- and they are constraints a
 * compiler cannot see.
 */
const LintBoard: FC<{ reports: Reports; loading?: boolean }> = ({
  reports,
  loading,
}) => {
  if (loading) return null;

  const total = reports.reduce((sum, r) => sum + r.count, 0);

  return (
    <div className="d-flex flex-column gap-3">
      <Card>
        <Card.Body>
          <h5 className="mb-2">What needs attention</h5>
          {/*
            THREE states, not two, and the test that renders an EMPTY LIST is what
            found the third: with no detectors at all, `total === 0` is true and
            the clean-archive sentence below was shown -- "no findings right now,
            every detector below ran" -- which is exactly the false clean report
            the server's own registry validation exists to prevent. A board with
            no detectors on it is a server fault, and it says so.
          */}
          {reports.length === 0 ? (
            <p className="text-muted mb-0">
              No detectors were returned, so nothing has been checked. This is a
              server fault rather than a clean archive.
            </p>
          ) : total === 0 ? (
            <p className="text-muted mb-0">
              No findings right now. Every detector below ran, and each one
              reports what it looked for — so this is a clean archive rather
              than a page that failed to load.
            </p>
          ) : (
            <p className="text-muted mb-0">
              {total.toLocaleString()} finding{total === 1 ? "" : "s"} across{" "}
              {reports.length} detector{reports.length === 1 ? "" : "s"}. Each
              one links to the record it is about; fixing it is the ordinary
              edit, which goes through the usual votes. Nothing here is changed
              automatically.
            </p>
          )}
        </Card.Body>
      </Card>

      {reports.map((report) => (
        <DetectorCard key={report.slug} report={report} />
      ))}

      {reports.length === 0 && (
        <Alert variant="warning" className="mb-0">
          The instance returned no detectors at all. That is not a clean archive
          — it means the lint registry has nothing in it, which is a server
          problem rather than a curation one.
        </Alert>
      )}
    </div>
  );
};

export default LintBoard;
