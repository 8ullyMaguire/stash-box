import {
  faCheck,
  faCircleNotch,
  faLightbulb,
} from "@fortawesome/free-solid-svg-icons";
import type { FC } from "react";
import { Badge, Button, Card, Col, Row } from "react-bootstrap";
import { Link } from "react-router-dom";
import { ErrorMessage, Icon, LoadingIndicator } from "src/components/fragments";
import {
  ROUTE_IDENTIFICATION,
  ROUTE_PERFORMER,
  ROUTE_SCENE,
  ROUTE_SITE,
  ROUTE_STUDIO,
  ROUTE_TAG,
} from "src/constants/route";
import {
  useIdentificationQuery,
  useVoteIdentificationCandidate,
} from "src/graphql";
import { useCurrentUser } from "src/hooks";

const ENTITY_HREFS: Record<string, (id: string) => string> = {
  performer: (id) => ROUTE_PERFORMER.replace(":id", id),
  scene: (id) => ROUTE_SCENE.replace(":id", id),
  studio: (id) => ROUTE_STUDIO.replace(":id", id),
  site: (id) => ROUTE_SITE.replace(":id", id),
  tag: (id) => ROUTE_TAG.replace(":id", id),
};

const TARGET_LABELS: Record<string, string> = {
  performer: "Performer",
  scene: "Scene",
  studio: "Studio",
  site: "Site",
  tag: "Tag",
};

/**
 * One question, its suggestions, and the ability to add a vote (SPEC Section 5).
 *
 * Three rules from the schema are load-bearing here, and each is visible in the
 * code rather than assumed:
 *
 * 1. A vote is one per person, enforced by the database. So the control renders
 *    as a state, not an action: `votedByMe` decides between Vote and Voted. A
 *    button that silently does nothing on a second tap is worse than one showing
 *    the vote is already cast, and a second tap is a constraint violation.
 *
 * 2. Contributing requires VOTE; reading does not. The control is gated on
 *    isVoter rather than rendered and left to fail. A READ user sees the whole
 *    thread and no dead buttons.
 *
 * 3. A candidate is a suggestion, never an answer. Nothing on this page resolves
 *    a query, and there is no button that would. The schema has
 *    resolveIdentificationQuery and this UI does not expose it: resolving needs
 *    a named human and records who, which belongs in the thread with its full
 *    history, not as a one-click action beside a list of guesses.
 *
 * A candidate whose entity has been deleted renders as a plain id with a note
 * rather than an empty link. `entity` is nullable in the schema precisely
 * because a board outlives its questions.
 *
 * `id` arrives as a prop, not from useParams: the test harness MemoryRouter has
 * no Routes, so useParams is undefined under test, and a page built on it would
 * look wired in a test while being broken in the app.
 */

const Candidate: FC<{
  candidate: {
    id: string;
    entityType: string;
    entityId: string;
    note?: string | null;
    voteCount: number;
    votedByMe: boolean;
    suggestedBy?: { id: string; name: string } | null;
    entity?: {
      id: string;
      name: string;
      disambiguation?: string | null;
    } | null;
  };
  canVote: boolean;
}> = ({ candidate, canVote }) => {
  const [vote, { loading }] = useVoteIdentificationCandidate();
  const href = ENTITY_HREFS[candidate.entityType]?.(candidate.entityId);

  // The vote mutation returns the candidate, so Apollo corrects the tally from
  // the server's answer. A local increment would drift the moment two people
  // vote at once.
  const onVote = () => {
    void vote({ variables: { candidateId: candidate.id } });
  };

  return (
    <Card className="mb-2">
      <Card.Body>
        <Row className="align-items-center">
          <Col>
            {candidate.entity ? (
              <Link to={href ?? "#"}>
                <strong>{candidate.entity.name}</strong>
                {candidate.entity.disambiguation && (
                  <span className="text-muted">
                    {candidate.entity.disambiguation}
                  </span>
                )}
              </Link>
            ) : (
              <span className="text-muted">
                <Icon icon={faLightbulb} className="me-1" />
                suggested entity no longer exists ({candidate.entityId})
              </span>
            )}
            <div className="small text-muted">
              {TARGET_LABELS[candidate.entityType] ?? candidate.entityType}
              {candidate.suggestedBy && (
                <> &middot; by {candidate.suggestedBy.name}</>
              )}
            </div>
            {candidate.note && (
              <div className="small mt-1">
                <Icon icon={faLightbulb} className="me-1" />
                {candidate.note}
              </div>
            )}
          </Col>
          <Col xs="auto" className="text-end">
            <div className="h5 mb-1">{candidate.voteCount}</div>
            {candidate.votedByMe ? (
              <Badge bg="success">
                <Icon icon={faCheck} /> Voted
              </Badge>
            ) : canVote ? (
              <Button
                size="sm"
                variant="outline-primary"
                disabled={loading}
                onClick={onVote}
              >
                {loading ? (
                  // The repo Icon has no spin prop, so the animation comes from a
                  // class the stylesheet already defines.
                  <Icon icon={faCircleNotch} className="fa-spin" />
                ) : (
                  "Vote"
                )}
              </Button>
            ) : (
              // No button for a user who cannot use it.
              // voteIdentificationCandidate is @hasRole(VOTE), so rendering one
              // produces a control that fails on click with "not authorized", and
              // a dead control reads as the board being broken rather than as a
              // role boundary.
              <span className="text-muted small">vote</span>
            )}
          </Col>
        </Row>
      </Card.Body>
    </Card>
  );
};

const IdentificationQueryPage: FC<{ id: string }> = ({ id }) => {
  const { isVoter } = useCurrentUser();
  const { data, loading, error } = useIdentificationQuery(id);
  const query = data?.identificationQuery;

  if (loading) return <LoadingIndicator message="Loading question..." />;
  if (error) return <ErrorMessage error="Failed to load this question." />;
  if (!query) {
    return (
      <ErrorMessage error="This question does not exist, or is no longer public." />
    );
  }

  return (
    <div className="d-flex flex-column gap-3">
      <div>
        <Link to={ROUTE_IDENTIFICATION} className="small">
          &larr; All open questions
        </Link>
        <h3 className="mt-2 mb-1">{query.description}</h3>
        <div className="text-muted small">
          Looking for a {TARGET_LABELS[query.targetType] ?? query.targetType}
          {query.targetId && (
            <>
              {" &middot; "}
              <Link
                to={ENTITY_HREFS[query.targetType]?.(query.targetId) ?? "#"}
              >
                the record in question
              </Link>
            </>
          )}
        </div>
        {query.status === "solved" && (
          <div className="mt-2">
            <Badge bg="success">Solved</Badge>{" "}
            <span className="small text-muted">
              resolved by {query.resolvedBy?.name ?? "a contributor"}
              {query.resolvedAt
                ? ` on ${new Date(query.resolvedAt).toLocaleDateString()}`
                : ""}
            </span>
          </div>
        )}
      </div>

      <div>
        <h5>
          Suggestions{" "}
          <span className="text-muted small">({query.candidates.length})</span>
        </h5>
        {query.candidates.length === 0 ? (
          <Card>
            <Card.Body className="text-muted">
              Nobody has suggested an answer yet. Knowing it is half the help.
            </Card.Body>
          </Card>
        ) : (
          query.candidates.map((candidate) => (
            <Candidate
              key={candidate.id}
              candidate={candidate}
              canVote={isVoter}
            />
          ))
        )}
      </div>

      {!isVoter && (
        <Card>
          <Card.Body className="text-muted small">
            You can read this board. Suggesting an answer or voting needs the
            voter role.
          </Card.Body>
        </Card>
      )}
    </div>
  );
};

export default IdentificationQueryPage;
