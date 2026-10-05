import type { FC } from "react";
import { Button, Card, Col, Row } from "react-bootstrap";
import { Link } from "react-router-dom";
import { ErrorMessage, LoadingIndicator } from "src/components/fragments";
import {
  ROUTE_CURATION_MATCHUP,
  ROUTE_PERFORMERS,
  ROUTE_SCENES,
  ROUTE_SITES,
  ROUTE_STUDIOS,
  ROUTE_TAGS,
} from "src/constants/route";
import {
  DEFAULT_COMPLETION_THRESHOLD,
  useCurationDashboard,
  useUserStreak,
} from "src/graphql";
import StreakCard from "./StreakCard";

const ENTITY_LINKS: {
  key: string;
  label: string;
  href: string;
}[] = [
  { key: "performersBelow", label: "Performers", href: ROUTE_PERFORMERS },
  { key: "scenesBelow", label: "Scenes", href: ROUTE_SCENES },
  { key: "studiosBelow", label: "Studios", href: ROUTE_STUDIOS },
  { key: "sitesBelow", label: "Sites", href: ROUTE_SITES },
  { key: "tagsBelow", label: "Tags", href: ROUTE_TAGS },
];

/**
 * The curation dashboard.
 *
 * The number that matters is how much of the database is still under-filled, and
 * it is recomputed server-side on every read rather than stored. That is why it
 * is safe to show: a cached count would eventually disagree with the database and
 * nothing would report the disagreement, so the dashboard would quietly ask
 * people to fix work that was already done.
 *
 * The framing is deliberately plain. Each count links to the list of incomplete
 * records, so the number is a door rather than a score. There is no percentage
 * of "your" contribution and no comparison to other curators: the work is shared,
 * and a number that ranks people by output is the pressure this feature was
 * designed to avoid.
 */
const CurationDashboard: FC = () => {
  const { data, loading, error } = useCurationDashboard(
    DEFAULT_COMPLETION_THRESHOLD,
  );

  // The curation dashboard keeps its own copy of the streak (SPEC
  // feature-streak-placement). It is a second fetch of the same query the user
  // profile makes, by design: /curation is role-gated on VOTE, so for a READ-only
  // user the profile is the only place a streak appears, and deleting this copy
  // would leave the original bug in place under a new address.
  //
  // Two independent copies can disagree in principle, so they read one query
  // document and one component rather than two implementations.
  const { data: streakData, loading: streakLoading } = useUserStreak();

  if (loading) return <LoadingIndicator message="Loading curation status..." />;
  if (error) return <ErrorMessage error="Failed to load curation status." />;
  if (!data) return null;

  const total = ENTITY_LINKS.reduce(
    (sum, e) => sum + (data[e.key as keyof typeof data] as number),
    0,
  );

  return (
    <div className="d-flex flex-column gap-3">
      <Card>
        <Card.Header>
          <h4 className="mb-0">Records needing curation</h4>
        </Card.Header>
        <Card.Body>
          <p className="text-muted">
            {total.toLocaleString()} records are below{" "}
            {DEFAULT_COMPLETION_THRESHOLD}% complete. Each links to the
            incomplete ones.
          </p>
          <Row className="text-center">
            {ENTITY_LINKS.map((e) => {
              const count = data[e.key as keyof typeof data] as number;
              return (
                <Col key={e.key}>
                  <div className="h4">{count.toLocaleString()}</div>
                  <Link
                    to={`${e.href}?complete=${DEFAULT_COMPLETION_THRESHOLD}`}
                  >
                    {e.label}
                  </Link>
                </Col>
              );
            })}
          </Row>
        </Card.Body>
      </Card>

      <StreakCard streak={streakData?.userStreak} loading={streakLoading} />

      <Card>
        <Card.Header>
          <h4 className="mb-0">Pick your contribution</h4>
        </Card.Header>
        <Card.Body>
          <p className="text-muted mb-3">
            Two performers, side by side, one click. The pair is chosen so that
            your vote changes the ranking most — which means your judgement is
            worth more on exactly the pairs that are hardest to call.
          </p>
          <Link to={ROUTE_CURATION_MATCHUP}>
            <Button variant="primary">Compare performers</Button>
          </Link>
        </Card.Body>
      </Card>
    </div>
  );
};

export default CurationDashboard;
