import {
  faChevronDown,
  faChevronRight,
} from "@fortawesome/free-solid-svg-icons";
import type { FC } from "react";
import { useId, useState } from "react";
import { Button } from "react-bootstrap";
import { Link } from "react-router-dom";

import { Icon } from "src/components/fragments";
import { ROUTE_USER } from "src/constants/route";
import { createHref, formatDateTime } from "src/utils";
import AmendmentAuditDetails from "./AmendmentAuditDetails";
import {
  CommentHideAuditDetails,
  CommentUpdateAuditDetails,
} from "./CommentAuditDetails";
import DeleteAuditDetails from "./DeleteAuditDetails";

const actionLabels: Record<string, string> = {
  EDIT_DELETE: "Edit Deleted",
  EDIT_AMENDMENT: "Edit Amended",
  EDIT_COMMENT_UPDATE: "Comment Edited",
  EDIT_COMMENT_HIDE: "Comment Visibility Changed",
};

export interface AuditRowProps {
  audit: {
    id: string;
    action: string;
    user?: { id: string; name: string } | null;
    target_id: string;
    target_type: string;
    data: string;
    reason?: string | null;
    created_at: string;
  };
}

const AuditRow: FC<AuditRowProps> = ({ audit }) => {
  const [expanded, setExpanded] = useState(false);
  const actionLabel = actionLabels[audit.action] ?? audit.action;
  // useId, not audit.id: this component's props do not carry one, and an id that exists only to be pointed
  // at does not need to be stable across renders -- it needs to be unique on the page.
  const detailsId = useId();

  return (
    <>
      <tr>
        <td className="text-nowrap" style={{ width: "40px" }}>
          {/* An icon-only button has no accessible name: axe reported button-name on five nodes of /audits,
              one per row, because the chevron is an <svg> and nothing else. The label is CONSTANT and the
              state lives in aria-expanded -- a label that says "Collapse" or "Expand" renames the control
              under a screen reader every time it is pressed, and the state it is describing is already in
              the accessibility tree. aria-controls points at the detail row, which used to be hidden with a
              class only. */}
          <Button
            variant="link"
            size="sm"
            onClick={() => setExpanded(!expanded)}
            className="p-0"
            aria-label="Audit entry details"
            aria-expanded={expanded}
            aria-controls={detailsId}
          >
            <Icon
              icon={expanded ? faChevronDown : faChevronRight}
              aria-hidden="true"
            />
          </Button>
        </td>
        <td className="text-nowrap">{formatDateTime(audit.created_at)}</td>
        <td>{actionLabel}</td>
        <td>
          {audit.user ? (
            <Link to={createHref(ROUTE_USER, audit.user)}>
              {audit.user.name}
            </Link>
          ) : (
            <em>Deleted User</em>
          )}
        </td>
        <td>
          {audit.target_type === "EDIT" ? (
            <span>Edit {audit.target_id.slice(0, 8)}</span>
          ) : audit.target_type === "EDIT_COMMENT" ? (
            <span>Comment {audit.target_id.slice(0, 8)}</span>
          ) : (
            audit.target_id
          )}
        </td>
        <td className="text-truncate" style={{ maxWidth: "300px" }}>
          {audit.reason || <em>No reason provided</em>}
        </td>
      </tr>
      <tr className={expanded ? "" : "d-none"} id={detailsId}>
        <td colSpan={6} className="p-0 border-0">
          {audit.action === "EDIT_DELETE" && (
            <DeleteAuditDetails data={audit.data} />
          )}
          {audit.action === "EDIT_AMENDMENT" && (
            <AmendmentAuditDetails data={audit.data} />
          )}
          {audit.action === "EDIT_COMMENT_UPDATE" && (
            <CommentUpdateAuditDetails data={audit.data} />
          )}
          {audit.action === "EDIT_COMMENT_HIDE" && (
            <CommentHideAuditDetails data={audit.data} />
          )}
        </td>
      </tr>
    </>
  );
};

export default AuditRow;
