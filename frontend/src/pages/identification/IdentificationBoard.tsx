import { faClock } from "@fortawesome/free-solid-svg-icons";
import type { FC } from "react";
import { Badge, Card, Col, Row } from "react-bootstrap";
import { Link } from "react-router-dom";
import { ErrorMessage, Icon, LoadingIndicator } from "src/components/fragments";
import { ROUTE_IDENTIFICATION_QUERY } from "src/constants/route";
import {
  type IdentificationSummary,
  useIdentificationBoard,
} from "src/graphql";

/**
 * "Which Was That…?" — the open queue (SPEC §5).
 *
 * This is the board's reason to exist: a question with no answer yet, written by
 * somebody who could not find it any other way. The list is therefore open
 * questions only, newest first, and a query with a zero-vote candidate still
 * shows that candidate — that is exactly when somebody needs to see it.
 *
 * The board reads at READ and contributes at VOTE. So this page is reachable by
 * every logged-in user, and the controls that need VOTE are gated here rather
 * than left to fail. That split is the schema's, not a UI invention:
 * `listOpenIdentificationQueries` is `@hasRole(role: READ)`.
 *
 * **What this page deliberately does not do:** it does not rank, score or
 * pressure. A board that told someone their question had been open for 40 days
 * would manufacture exactly the loss-framing §7.25 and the StreakCard contract
 * refuse. A question's age is shown, never as a countdown.
 */

const TARGET_LABELS: Record<string, string> = {
  performer: "Performer",
  scene: "Scene",
  studio: "Studio",
  site: "Site",
  tag: "Tag",
};

const formatAge = (iso: string) => {
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return "";
  const days = Math.floor((Date.now() - then) / 86_400_000);
  if (days <= 0) return "asked today";
  if (days === 1) return "asked yesterday";
  return `asked ${days} days ago`;
};

const QueryRow: FC<{ query: IdentificationSummary }> = ({ query }) => {
  const votes = query.candidates.reduce((n, c) => n + c.voteCount, 0);

  return (
    <Card className="mb-2">
      <Card.Body>
        <Row className="align-items-start">
          <Col>
            <Badge bg="secondary" className="me-2">
              {TARGET_LABELS[query.targetType] ?? query.targetType}
            </Badge>
            <Link to={ROUTE_IDENTIFICATION_QUERY.replace(":id", query.id)}>
              {query.description}
            </Link>
          </Col>
          <Col xs="auto" className="text-end text-muted small">
            <div>
              <Icon icon={faClock} className="me-1" />
              {formatAge(query.createdAt)}
            </div>
            <div>
              {votes} {votes === 1 ? "vote" : "votes"}
              {query.candidates.length === 0 && " · no suggestions yet"}
            </div>
          </Col>
        </Row>
      </Card.Body>
    </Card>
  );
};

const IdentificationBoard: FC = () => {
  const { data, loading, error } = useIdentificationBoard(100);
  const queries = data?.listOpenIdentificationQueries;

  return (
    <div className="d-flex flex-column gap-3">
      <div>
        <h3>Which Was That…?</h3>
        <p className="text-muted mb-0">
          Questions about something half-remembered, waiting for someone to
          recognise it. A suggestion is evidence, never an answer — nothing here
          creates a record on its own.
        </p>
      </div>

      {loading && <LoadingIndicator message="Loading open questions..." />}
      {error && (
        <ErrorMessage error="Failed to load the identification board." />
      )}

      {!loading && !error && queries && queries.length === 0 && (
        <Card>
          <Card.Body className="text-muted">
            Nothing open. Every question has been answered or set aside.
          </Card.Body>
        </Card>
      )}

      {queries?.map((query) => (
        <QueryRow key={query.id} query={query} />
      ))}
    </div>
  );
};

export default IdentificationBoard;
