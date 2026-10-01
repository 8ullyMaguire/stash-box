import { faFire } from "@fortawesome/free-solid-svg-icons";
import type { FC } from "react";
import { Badge, Card, Col, Row } from "react-bootstrap";
import { Icon } from "src/components/fragments";

interface Streak {
  currentStreak: number;
  longestStreak: number;
  totalActiveDays: number;
  activeToday: boolean;
  lastActiveDay?: string | null;
}

interface Props {
  // lastActiveDay is optional because the generated GraphQL type makes it
  // `string | null | undefined` -- optional in the schema sense as well as the
  // nullable one -- and a component that cannot accept the type the query
  // produces is a type error waiting to surface at the call site.
  streak?: Streak | null;
  loading?: boolean;
}

/**
 * The activity streak, shown as a fact and nothing else.
 *
 * There is no way to LOSE a streak here, and that is a deliberate constraint
 * rather than an absence of features:
 *
 * - `totalActiveDays` is the number that only ever goes up, and it is the largest
 *   thing on the card. A user who stopped months ago still sees their record
 *   intact. The growth-oriented number is the headline, so nothing on this card
 *   can decay to zero.
 * - `currentStreak` is rendered as neutral text, not as a flame, and never with
 *   the word "lost". A streak of 0 reads "No streak yet", never "You lost your
 *   streak" -- there was nothing taken away, because nothing was stored to take.
 * - `activeToday` is separate from the streak, so a live streak does not imply
 *   today is done. The backend exposes both for exactly this.
 * - `lastActiveDay` exists so "your last contribution was 14 days ago" can be
 *   said plainly instead of leaving someone watching a number silently go to
 *   zero and wondering when.
 *
 * The flame icon is on the streak, never on a countdown, and there is no
 * reminder, no streak freeze and no way to lose an earned badge. A mechanic that
 * penalises absence is a lever rather than a measurement, and this card reports
 * contribution instead.
 */
const StreakBadge: FC<Props> = ({ streak, loading }) => {
  if (loading) return null;

  if (!streak) return null;

  const {
    currentStreak,
    longestStreak,
    totalActiveDays,
    activeToday,
    lastActiveDay,
  } = streak;

  return (
    <Card>
      <Card.Header>
        <h4 className="mb-0">Your curation</h4>
      </Card.Header>
      <Card.Body>
        <Row className="text-center">
          <Col>
            <div className="display-6">{totalActiveDays}</div>
            <div className="text-muted small">active days, all time</div>
          </Col>
          <Col>
            <div className="display-6">
              {currentStreak > 0 && <Icon icon={faFire} className="me-1" />}
              {currentStreak}
            </div>
            <div className="text-muted small">
              {currentStreak > 0 ? "day streak" : "No streak yet"}
            </div>
          </Col>
          <Col>
            <div className="display-6">{longestStreak}</div>
            <div className="text-muted small">longest streak</div>
          </Col>
        </Row>

        <div className="mt-3 small">
          {activeToday ? (
            <Badge bg="success">Active today</Badge>
          ) : (
            <span className="text-muted">
              {lastActiveDay
                ? `Last active ${lastActiveDay}`
                : "No contributions yet — approve or submit an edit to start"}
            </span>
          )}
        </div>
      </Card.Body>
    </Card>
  );
};

export default StreakBadge;
