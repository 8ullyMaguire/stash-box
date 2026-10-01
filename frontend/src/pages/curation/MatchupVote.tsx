import type { FC } from "react";
import { useState } from "react";
import { Button, Card, Col, Row } from "react-bootstrap";
import { Link } from "react-router-dom";
import { ErrorMessage, LoadingIndicator } from "src/components/fragments";
import { ROUTE_PERFORMER } from "src/constants/route";
import { EloEntityType, useEloMatchup, useVoteElo } from "src/graphql";

interface Side {
  id: string;
  name: string;
  disambiguation?: string | null;
  completion?: { score: number; missing: string[] } | null;
}

/**
 * The matchup vote loop: two performers, one click, next pair.
 *
 * The pair is not random. The backend picks the comparison where a vote moves
 * the rating most, which is why this is the highest-leverage curation surface in
 * the product -- a minute here changes the ordering that search results and the
 * leaderboard both depend on.
 *
 * Two things are deliberately absent.
 *
 * No streak or reward feedback on the screen. The rating the backend returns is
 * shown as a plain number because it is the actual consequence of the vote, not
 * as a reward for having voted. A curator who can lose something by not voting is
 * being given a reason to show up that is not "the database needs it", and this
 * page has no way to express that.
 *
 * No progress counter ("12 of 50"). The loop ends when the pairs run out, and an
 * invented denominator would tell the curator they owe a fixed number of votes.
 */
const MatchupVote: FC = () => {
  const [voted, setVoted] = useState<number | null>(null);
  const { data, loading, error, refetch } = useEloMatchup(
    EloEntityType.PERFORMER,
  );
  const [voteElo] = useVoteElo();

  const matchup = data?.eloMatchup;
  const left = matchup?.left as Side | undefined;
  const right = matchup?.right as Side | undefined;

  const handleVote = async (side: 0 | 1) => {
    if (!left || !right) return;
    setVoted(side);
    try {
      await voteElo({
        variables: {
          input: {
            matchup: {
              entityType: EloEntityType.PERFORMER,
              left: left.id,
              right: right.id,
            },
            pickedSide: side,
          },
        },
      });
      // Fetch the next pair rather than reusing the returned one: the backend
      // deliberately does not choose the next matchup in the vote response, so
      // showing the returned matchup would re-show the same pair with its ratings
      // changed.
      await refetch();
    } finally {
      setVoted(null);
    }
  };

  if (loading) return <LoadingIndicator message="Finding a pair..." />;
  if (error) return <ErrorMessage error="Failed to load a matchup." />;

  // An empty state, not an error. Having nothing to compare yet is ordinary --
  // it means every pair has been voted on -- and every curator sees it.
  if (!matchup || !left || !right)
    return (
      <Card>
        <Card.Body className="text-center">
          <p>Nothing to compare right now.</p>
          <p className="text-muted mb-0">
            Every available pair has been voted on. New performers will appear
            here as they are added.
          </p>
        </Card.Body>
      </Card>
    );

  const side = (p: Side, index: 0 | 1) => (
    <Col md={6}>
      <Card className="h-100 text-center">
        <Card.Body>
          <Link to={ROUTE_PERFORMER.replace(":id", p.id)}>
            <h5>
              {p.name}
              {p.disambiguation ? (
                <span className="text-muted"> ({p.disambiguation})</span>
              ) : null}
            </h5>
          </Link>
          {p.completion ? (
            <div className="text-muted small">
              {p.completion.score}% complete
              {p.completion.missing.length > 0 && (
                <> — missing {p.completion.missing.join(", ")}</>
              )}
            </div>
          ) : null}
          <Button
            className="mt-3 w-100"
            variant="primary"
            disabled={voted !== null}
            onClick={() => handleVote(index)}
          >
            {voted === index ? "Recorded" : "Better match"}
          </Button>
        </Card.Body>
      </Card>
    </Col>
  );

  return (
    <div className="d-flex flex-column gap-3">
      <p className="text-muted">
        Which is the better match for the name these performers are filed under?
        Your vote moves the ranking that search results use.
      </p>
      <Row className="g-3">
        {side(left, 0)}
        {side(right, 1)}
      </Row>
    </div>
  );
};

export default MatchupVote;
