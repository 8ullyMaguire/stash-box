import { faRobot } from "@fortawesome/free-solid-svg-icons";
import cx from "classnames";
import type { FC } from "react";
import { Card, Col, Row } from "react-bootstrap";
import { Link } from "react-router-dom";
import { Icon, Tooltip } from "src/components/fragments";

import { type EditFragment, OperationEnum, VoteStatusEnum } from "src/graphql";

import {
  editHref,
  formatDateTime,
  formatElapsed,
  formatOrdinals,
  parseInstant,
  userHref,
} from "src/utils";
import AddComment from "./AddComment";
import EditComment from "./EditComment";
import EditExpiration from "./EditExpiration";
import EditHeader from "./EditHeader";
import EditStatus from "./EditStatus";
import ModifyEdit from "./ModifyEdit";
import type { EditCardEdit } from "./types";
import VoteBar from "./VoteBar";
import Votes from "./Votes";

const CLASSNAME = "EditCard";

type Props = { showVotes?: boolean } & (
  | { edit: EditCardEdit; compact: true }
  | { edit: EditFragment; compact?: false }
);

// The waiting line is what a review queue's "aging" means: how long a pending
// edit has been waiting for votes (SPEC 7.24.10 -- pending edits nobody votes on
// block the multi-user verification 7.7 depends on). Only PENDING edits have a
// wait; a decided one is done waiting, and saying how long it waited would be a
// different fact about a different thing.
//
// Exported as a render function rather than inlined, following the file's
// neighbours (ModifyEdit exports renderPerformerDetails): a component that needs
// the whole router and GraphQL provider cannot be asserted with the helpers this
// suite has, and a rule that cannot be asserted is a rule nobody checks.
export const renderWaiting = (edit: {
  status: VoteStatusEnum;
  created: string;
}) => {
  if (edit.status !== VoteStatusEnum.PENDING) return null;
  const created = parseInstant(edit.created);
  if (!created) return null;

  return (
    <div data-testid="edit-waiting">
      <b className="me-2">Waiting:</b>
      <span title={formatDateTime(new Date(edit.created))}>
        {formatElapsed(created)}
      </span>
    </div>
  );
};

const EditCardComponent: FC<Props> = (props) => {
  const { edit, showVotes = false } = props;
  const compact = props.compact === true;
  const title = `${edit.operation.toLowerCase()} ${edit.target_type.toLowerCase()}`;
  const created = new Date(edit.created);

  return (
    <Card className={cx(CLASSNAME, "mb-3")}>
      <Card.Header className="row">
        <div className="flex-column col-4">
          <Link to={editHref(edit)}>
            <h5 className="text-capitalize">{title.toLowerCase()}</h5>
          </Link>
          <div>
            <b className="me-2">Author:</b>
            {edit.user ? (
              <Link to={userHref(edit.user)}>
                <span>{edit.user.name}</span>
              </Link>
            ) : (
              <span>Deleted User</span>
            )}
            {edit.bot && (
              <Tooltip
                text="Edit submitted by an automated script"
                delay={50}
                placement="auto"
              >
                <span>
                  <Icon icon={faRobot} className="ms-2" />
                </span>
              </Tooltip>
            )}
          </div>
          <div>
            <b className="me-2">Created:</b>
            <span>{formatDateTime(created)}</span>
          </div>
          {renderWaiting(edit)}
          {edit.updated && edit.update_count > 0 && (
            <div>
              <b className="me-2">Updated:</b>
              <span>{formatDateTime(edit.updated)}</span>
              <small className="text-muted align-text-top ms-2">{`${formatOrdinals(edit.update_count)} revision`}</small>
            </div>
          )}
        </div>
        <div className="flex-column col-4 ms-auto text-end">
          <div>
            <b className="me-2">Status:</b>
            <EditStatus {...edit} />
            <EditExpiration edit={edit} />
          </div>
        </div>
      </Card.Header>
      <hr />
      <Card.Body>
        <EditHeader edit={edit} compact={compact} />
        {props.compact ? (
          showVotes && <Votes edit={edit} />
        ) : (
          <>
            {props.edit.operation === OperationEnum.CREATE && (
              <ModifyEdit details={props.edit.details} />
            )}
            {props.edit.operation !== OperationEnum.CREATE && (
              <ModifyEdit
                details={props.edit.details}
                oldDetails={props.edit.old_details}
                options={props.edit.options ?? undefined}
              />
            )}
            <Row className="mt-2">
              <Col md={{ offset: 4, span: 8 }}>
                {showVotes && <Votes edit={edit} />}
                {(props.edit.comments ?? []).map((comment, index) => (
                  <EditComment
                    {...comment}
                    isPrimary={index === 0}
                    key={comment.id}
                  />
                ))}
                <AddComment editID={edit.id} />
                {!compact && <VoteBar edit={edit} />}
              </Col>
            </Row>
          </>
        )}
      </Card.Body>
    </Card>
  );
};

export default EditCardComponent;
