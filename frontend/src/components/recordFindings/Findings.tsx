import type { FC } from "react";
import { Alert } from "react-bootstrap";
import { Link } from "react-router-dom";
import {
  ROUTE_PERFORMER_EDIT,
  ROUTE_PERFORMER_MERGE,
  ROUTE_SCENE_EDIT,
  ROUTE_SCENE_MERGE,
} from "src/constants";
import type { LintFindingsForQuery } from "src/graphql";

type Report = LintFindingsForQuery["lintFindingsFor"][number];

// DETECTORS WHOSE FIX IS A MERGE, as an ALLOW-LIST rather than "the answer named two records".
//
// SPEC §7.24.4 is explicit that lint emits a quest and never merges: the merge is the ordinary
// consensus flow, an edit somebody votes on. What this list decides is only which of those flows the
// finding points at -- a duplicated URL or a claimed alias is two records that belong as one, while a
// birthdate that postdates a scene or a picture too small to use is the record itself. Deriving it
// from the shape of the answer instead ("two candidates means merge") would start offering merges the
// day any detector returns two candidates for a reason nobody merged.
const MERGE_SLUGS = new Set([
  "duplicate_performer_url",
  "duplicate_scene_url",
  "performer_alias_collision",
]);

interface Props {
  // The record the reader is looking at, so the action can name both halves of the finding.
  entityType: string;
  entityId: string;
  reports: Report[];
}

// Presentational by design: it renders the answer it is handed and offers the ordinary flows. It
// cannot merge, dismiss, or hide anything -- there is no control here that changes a record, and the
// suite asserts that.
const Findings: FC<Props> = ({ entityType, entityId, reports }) => {
  const isPerformer = entityType === "performer";
  const editHref = isPerformer
    ? ROUTE_PERFORMER_EDIT.replace(":id", entityId)
    : ROUTE_SCENE_EDIT.replace(":id", entityId);
  const mergeOf = (other: string) =>
    `${
      isPerformer
        ? ROUTE_PERFORMER_MERGE.replace(":id", entityId)
        : ROUTE_SCENE_MERGE.replace(":id", entityId)
    }?source=${other}`;

  return (
    <div className="RecordFindings">
      <Alert variant="warning" className="mb-2">
        <span className="RecordFindings-heading">
          {reports.length === 1
            ? "One detector flags this record."
            : `${reports.length} detectors flag this record.`}
        </span>
        <span className="RecordFindings-sub">
          Nothing here has been changed. Merging and editing are the ordinary
          flows, and both need a vote.
        </span>
      </Alert>
      {reports.map((report) => {
        const others = report.candidates.filter((c) => c.entityId !== entityId);
        // ONE predicate, so that each half of it is provable on its own: dropping the slug test starts
        // offering merges for findings whose fix is an edit, and dropping the "exactly one other record"
        // test offers a merge of a record into itself the moment a finding names several.
        const mergeable = MERGE_SLUGS.has(report.slug) && others.length === 1;
        return (
          <Alert key={report.slug} variant="warning">
            <div className="RecordFindings-detail">
              {report.candidates.find((c) => c.entityId === entityId)?.detail ??
                report.description}
            </div>
            {others.length > 0 && (
              <ul className="RecordFindings-candidates">
                {others.map((c) => (
                  <li key={c.entityId}>
                    <span className="RecordFindings-name">{c.entityName}</span>
                    <span className="RecordFindings-note">{c.detail}</span>
                  </li>
                ))}
              </ul>
            )}
            <div className="RecordFindings-actions">
              {mergeable && (
                <Link
                  to={mergeOf(others[0].entityId)}
                  className="btn btn-warning btn-sm"
                >
                  Merge {others[0].entityName} into this record
                </Link>
              )}
              <Link to={editHref} className="btn btn-secondary btn-sm">
                Fix it on the edit page
              </Link>
            </div>
            {report.limitNotice && (
              // A truncated answer must say so, here as on the board: a list that stopped early and
              // does not say so reads as a complete one.
              <div className="RecordFindings-truncated">
                {report.limitNotice}
              </div>
            )}
          </Alert>
        );
      })}
    </div>
  );
};

export default Findings;
