import { faCircleExclamation } from "@fortawesome/free-solid-svg-icons";
import type { FC } from "react";
import { Card, ProgressBar } from "react-bootstrap";
import { Link } from "react-router-dom";
import { Icon } from "src/components/fragments";
import {
  ROUTE_PERFORMER_EDIT,
  ROUTE_SCENE_EDIT,
  ROUTE_SITE_EDIT,
  ROUTE_STUDIO_EDIT,
  ROUTE_TAG_EDIT,
} from "src/constants/route";

/**
 * A completion bar with a "help complete this" call to action (SPEC §7.7, item 6).
 *
 * One component for all five entity types, because §7.7's "progress bars
 * everywhere" only works if the bar is the same everywhere — five near-identical
 * implementations is five chances to render one of them wrong. The score itself
 * is computed by the backend on read (`internal/service/completion`), never
 * stored, so it is correct the moment an edit lands.
 *
 * Three decisions worth stating, because each is a place where the obvious
 * implementation is worse:
 *
 * 1. **The missing fields are shown, not just a number.** A bare "40%" tells a
 *    curator THAT something is missing and not WHAT, and "improve this performer"
 *    is not a task anyone can act on. The schema says this outright and it is
 *    right: `missing` is the actionable half.
 *
 * 2. **A complete entity renders as a plain confirmation, not a full-width green
 *    bar.** A 100% bar on every well-curated page is noise, and it competes with
 *    the pages that actually need help. The bar appears when there is something
 *    to do.
 *
 * 3. **The CTA links to the edit form, it does not open a modal.** Completing an
 *    entity is a form with a dozen fields and an edit history; a modal would be a
 *    second, worse version of it.
 *
 * Nothing here pressures the reader. No "N entities need you", no countdown. The
 * same line §7.25 and the StreakCard contract hold: a page states a fact and
 * offers the work.
 */

/** Human labels for the field names the backend reports as missing. */
const FIELD_LABELS: Record<string, string> = {
  birthdate: "birthdate",
  birthdate_accuracy: "how confident the birthdate is",
  gender: "gender",
  ethnicity: "ethnicity",
  country: "country",
  eye_color: "eye color",
  hair_color: "hair color",
  height: "height",
  weight: "weight",
  cup_size: "cup size",
  band_size: "band size",
  hip_size: "hip size",
  waist_size: "waist size",
  breast_type: "breast type",
  measurements: "measurements",
  career_start_year: "career start year",
  career_end_year: "career end year",
  tattoos: "tattoos",
  piercings: "piercings",
  aliases: "aliases",
  duration: "duration",
  details: "details",
  director: "director",
  urls: "links",
  parent_studio: "parent studio",
  category: "category",
  site: "site",
  description: "description",
};

export interface CompletionData {
  entityType: string;
  score: number;
  missing: string[];
  total: number;
  earned: number;
}

/**
 * Each entity type has its own edit route, so the CTA has to build the right
 * one. The pattern is the same everywhere, so it is built from a template and
 * NOT hand-written per type: five near-identical literals is five chances to
 * point at the wrong entity.
 */
const EDIT_ROUTES: Record<string, string> = {
  performer: ROUTE_PERFORMER_EDIT,
  scene: ROUTE_SCENE_EDIT,
  studio: ROUTE_STUDIO_EDIT,
  site: ROUTE_SITE_EDIT,
  tag: ROUTE_TAG_EDIT,
};

const CompletionBar: FC<{
  completion: CompletionData | null | undefined;
  entityId: string;
  /** Hide the CTA on pages where editing is not the obvious next step. */
  showCTA?: boolean;
}> = ({ completion, entityId, showCTA = true }) => {
  // A null completion is the schema's "could not be scored", which is different
  // from a score of 0. Showing "0% complete" for an entity nobody could score
  // would be a false claim about the record.
  if (!completion) return null;

  if (completion.score >= 100) {
    return (
      <Card className="mb-3">
        <Card.Body className="py-2">
          <span className="text-muted small">Complete — nothing missing.</span>
        </Card.Body>
      </Card>
    );
  }

  const label = (f: string) => FIELD_LABELS[f] ?? f.replace(/_/g, " ");
  const editTemplate = EDIT_ROUTES[completion.entityType];
  // An unknown entityType must NOT render a link to somewhere arbitrary: the
  // bar's value is the missing-field list, so it still renders, just without a
  // CTA rather than with a broken one.
  const editPath = editTemplate
    ? editTemplate.replace(":id", entityId)
    : undefined;

  return (
    <Card className="mb-3">
      <Card.Body>
        <div className="d-flex justify-content-between align-items-baseline mb-1">
          <span className="small">
            <strong>{completion.score}%</strong>{" "}
            <span className="text-muted">
              {/* Points, not field count. The backend scores weighted fields
                  (a birthdate is worth more than an eye colour), so "0 of 80"
                  would read as "80 fields are missing" when 12 are. The count
                  of missing fields is already on screen below, so the weight
                  only needs to be readable, not prominent. */}
              complete &mdash; {completion.earned} of {completion.total}{" "}
              weighted fields filled
            </span>
          </span>
        </div>

        <ProgressBar
          now={completion.score}
          // The label is on screen already; a second one inside the bar is
          // duplicated text at a smaller size.
          label=""
          variant={completion.score >= 70 ? "success" : "warning"}
          className="mb-2"
        />

        {completion.missing.length > 0 && (
          <>
            <div className="small text-muted mb-1">
              Missing {completion.missing.length}{" "}
              {completion.missing.length === 1 ? "field" : "fields"}:
            </div>
            <ul className="small mb-2 ps-3">
              {completion.missing.map((f) => (
                <li key={f}>{label(f)}</li>
              ))}
            </ul>
          </>
        )}

        {showCTA && editPath && (
          /* A plain <a class="btn">, NOT <Button as={Link}>: react-bootstrap
             adds role="button" when `as` is a component, which OVERRIDES the
             anchor's implicit link role. A screen reader then announces
             "button" for something that navigates, and middle-click / open-in-
             new-tab silently do not work. Bootstrap's own button classes are
             applied directly so the look is identical. Caught by a role query,
             which is the only kind of assertion that sees it -- this repo has no
             other test that queries by role, which is why it survived review. */
          <Link to={editPath} className="btn btn-outline-primary btn-sm">
            <Icon icon={faCircleExclamation} className="me-1" />
            Help complete this
          </Link>
        )}
      </Card.Body>
    </Card>
  );
};

export default CompletionBar;
