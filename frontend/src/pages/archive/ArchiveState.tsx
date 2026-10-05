import { faCircleExclamation } from "@fortawesome/free-solid-svg-icons";
import type { FC } from "react";
import { Card, Col, Row } from "react-bootstrap";
import { Link } from "react-router-dom";
import { Icon } from "src/components/fragments";

/**
 * State of the Archive (SPEC §7.7 item 9, growth item 9).
 *
 * One page answering "how much work is left, and where is it?" for all five
 * scored entity types. The backend already computes all of it — the per-type
 * `countIncompleteEntities` and `ListIncompleteEntities` exist and are tested —
 * so this is presentation, not a new subsystem.
 *
 * The framing is the whole design. A page whose headline number is "X incomplete"
 * is a debt report and reads as one. §7.7 wants a progress bar, and §7.25 draws
 * the line about pressure, so:
 *
 *  - the headline is what is DONE, not what is missing
 *  - the missing count is secondary, per type, and each one is a link to the
 *    entities concerned rather than a scolding
 *  - nothing here counts down, ranks a user, or says "only X left!"
 *
 * The score per type comes from `countIncompleteEntities(below: 100)` and the
 * type's total, so the two can never disagree: both are the same query, and
 * there is no client-side scoring anywhere on this page.
 */

/**
 * A type whose fraction is fully known: both the total and the incomplete count
 * were read.
 *
 * A named predicate rather than an inline arrow, because it is what lets
 * TypeScript narrow `total` and `incomplete` to non-null inside the reducers. An
 * inline `.filter(...)` leaves the compiler seeing the original nullable type, so
 * the arithmetic needs `?? 0` — and an unreachable `?? 0` is exactly what lets a
 * mutation become an equivalent mutant.
 */
export interface MeasuredArchiveStateType extends ArchiveStateType {
  total: number;
  incomplete: number;
}

export interface ArchiveStateType {
  entityType: string;
  /**
   * `countIncompleteEntities(entityType:, below: 100)`, or null when the
   * numerator could not be read for this type.
   *
   * Null is not 0. A type whose incomplete count failed to load must not be
   * shown as fully catalogued, which is what treating it as 0 would do — the
   * card would claim "All catalogued" about a type nobody has measured.
   */
  incomplete: number | null;
  /** Total entities of this type, or null when the count is unavailable. */
  total: number | null;
}

const LABELS: Record<string, { plural: string; route: string }> = {
  performer: { plural: "performers", route: "/performers" },
  scene: { plural: "scenes", route: "/scenes" },
  studio: { plural: "studios", route: "/studios" },
  site: { plural: "sites", route: "/sites" },
  tag: { plural: "tags", route: "/tags" },
};

const ArchiveState: FC<{ states: ArchiveStateType[] }> = ({ states }) => {
  // A type whose total could not be read contributes nothing to the totals and
  // is still listed. Dropping it would make the page quietly understate the
  // work; the alternative — showing 0 — would claim the archive is finished.
  // A type contributes to the totals only when BOTH halves of its fraction are
  // known. A denominator with no numerator would otherwise be counted as fully
  // catalogued, which is the one reading that is always wrong.
  const counted = states.filter(
    (s): s is MeasuredArchiveStateType =>
      s.total !== null && s.total > 0 && s.incomplete !== null,
  );
  // No `?? 0` and no Math.max here, both deliberately.
  //
  // `counted` has already excluded every null, so a `?? 0` fallback is
  // unreachable — and unreachable defensiveness is not free: it makes the
  // equivalent mutant `(s.incomplete ?? 0)` -> `0` survive every test, which
  // hides the fact that the arithmetic is no longer what is being checked.
  //
  // The floor at 0 is unreachable-in-effect for the same reason. `pct` clamps to
  // 0..100 below, so a negative numerator displays identically to a zero one.
  // Both were measured: with the floor removed all 17 tests still pass, which is
  // the definition of dead arithmetic. Removing it leaves one fewer thing that
  // cannot be killed by a test.
  const complete = counted.reduce((sum, s) => sum + s.total - s.incomplete, 0);
  const total = counted.reduce((sum, s) => sum + s.total, 0);
  const incomplete = states.reduce((sum, s) => sum + (s.incomplete ?? 0), 0);

  // Clamped for the same reason as the card: `complete` is floored at 0 but
  // `total` is not, so a type whose incomplete count exceeds its total (not
  // reachable from today's query, but the two numbers come from two queries)
  // would render a NEGATIVE percentage. A negative width is a broken bar, and
  // a negative percentage is not a figure any reader can interpret.
  const pct =
    total > 0
      ? Math.min(Math.max(Math.round((complete / total) * 100), 0), 100)
      : 0;

  return (
    <>
      <h2 className="mb-1">State of the Archive</h2>
      <p className="text-muted small">
        How much of this instance is catalogued. Every number below is counted
        from the records themselves, so it moves the moment an edit lands.
      </p>

      <Card className="mb-3">
        <Card.Body>
          <div className="d-flex justify-content-between align-items-baseline">
            <span>
              <strong>{pct}%</strong>{" "}
              <span className="text-muted">complete</span>
            </span>
            <span className="text-muted small">
              {complete.toLocaleString()} of {total.toLocaleString()} catalogued
            </span>
          </div>

          {/* The headline bar is the completion fraction, and its label is
              omitted because the number is directly above it. A second copy of
              "42%" inside a narrower bar is duplicated text. */}
          <div
            className={`progress mt-2 ${pct >= 70 ? "bg-success" : "bg-warning"}`}
            role="progressbar"
            aria-valuenow={pct}
            aria-valuemin={0}
            aria-valuemax={100}
            aria-label="Archive completion"
          >
            <div
              className="progress-bar"
              style={{ width: `${pct}%` }}
              // Bootstrap's own styling keys off these; the div carries no text
              // because the value is already on screen.
              aria-hidden="true"
            />
          </div>
        </Card.Body>
      </Card>

      <Row className="g-3">
        {states.map((s) => {
          const label = LABELS[s.entityType];
          // A type-guard-shaped local rather than a plain boolean: the card body
          // uses `s.incomplete` in four places, and a boolean `measurable` leaves
          // TypeScript seeing `number | null` at every one of them. Assigning the
          // narrowed values to locals is what makes the branch type-safe without
          // a non-null assertion.
          const t = s.total ?? 0;
          const missing = s.incomplete;
          const measurable = t > 0 && missing !== null;
          // Clamped to 100 for the same reason the headline is: an over-claimed
          // type (more held than a source claims) would otherwise render a bar
          // wider than its track and a percentage above 100, which is not a
          // number any reader can interpret. The clamp belongs on the DISPLAY;
          // the raw counts stay unclamped so the card can still show "14 of 12"
          // and the discrepancy stays visible rather than being hidden.
          const pctType = measurable
            ? Math.min(
                Math.max(Math.round(((t - (missing ?? 0)) / t) * 100), 0),
                100,
              )
            : 0;

          return (
            <Col key={s.entityType} xs={12} md={6} xl={4}>
              <Card className="h-100">
                <Card.Body>
                  <div className="d-flex justify-content-between align-items-baseline mb-1">
                    <span>
                      <strong>{pctType}%</strong>{" "}
                      <span className="text-muted">
                        {label?.plural ?? s.entityType}
                      </span>
                    </span>
                    <span className="text-muted small">
                      {/* Rendered outside the `measurable` guard below, so it
                          needs its own: an unmeasured type must not print
                          "400 of null", and must not print a count that implies
                          the completeness figure beside it is real. */}
                      {measurable ? `${t - (s.incomplete ?? 0)} of ${t}` : "—"}
                    </span>
                  </div>

                  {/* Order matters: an empty type has no numerator because
                      there is nothing to count, which is different from a
                      numerator that FAILED to load. Showing "not measured"
                      above "no records yet" contradicts itself on screen. */}
                  {t === 0 ? null : measurable ? (
                    <div
                      className={`progress mb-2 ${pctType >= 70 ? "bg-success" : "bg-warning"}`}
                      role="progressbar"
                      aria-valuenow={pctType}
                      aria-valuemin={0}
                      aria-valuemax={100}
                      aria-label={`${label?.plural ?? s.entityType} completion`}
                    >
                      <div
                        className="progress-bar"
                        style={{ width: `${pctType}%` }}
                        aria-hidden="true"
                      />
                    </div>
                  ) : (
                    <p className="small text-muted mb-2">
                      Completion not measured for this type.
                    </p>
                  )}

                  {s.incomplete !== null && s.incomplete > 0 ? (
                    <Link to={label?.route ?? "/"} className="small">
                      <Icon icon={faCircleExclamation} className="me-1" />
                      {s.incomplete.toLocaleString()}{" "}
                      {s.incomplete === 1 ? "record" : "records"} still
                      incomplete
                    </Link>
                  ) : (
                    <span className="small text-muted">
                      {/* Emptiness first. An empty type has no numerator
                          because there is nothing to count, which is not the
                          same as a numerator that failed to load -- checking
                          `incomplete === null` first made every empty type say
                          "Not measured" AND "No records yet" on one card, live. */}
                      {t === 0
                        ? "No records yet"
                        : s.incomplete === null
                          ? "Not measured"
                          : "All catalogued"}
                    </span>
                  )}
                </Card.Body>
              </Card>
            </Col>
          );
        })}
      </Row>

      {incomplete > 0 && (
        <p className="text-muted small mt-3 mb-0">
          {incomplete.toLocaleString()} incomplete{" "}
          {incomplete === 1 ? "record" : "records"} across this instance. Every
          entity page shows what is missing for that entity and links straight
          to the form that fills it.
        </p>
      )}
    </>
  );
};

export default ArchiveState;
