import type { FC } from "react";
import { Alert, Button, Table } from "react-bootstrap";
import { Link } from "react-router-dom";

import { ROUTE_LIST_EDIT } from "src/constants/route";
import {
  type ListEntityTypeEnum,
  type ListQuery,
  usePublishList,
  useRemoveListItem,
  useUnpublishList,
} from "src/graphql";
import { createHref } from "src/utils";

type List = NonNullable<ListQuery["list"]>;

interface Props {
  list: List;
  isOwner: boolean;
}

const entityLink = (entityType: ListEntityTypeEnum, entityId: string) => {
  // Only PERFORMER and SCENE have their own routes in this frontend. Linking a studio or a
  // site id to a performer route would produce a 404 that looks like broken data, so the
  // unknown case renders the id as text -- which is honest, and the entity can gain a link
  // when its page exists.
  switch (entityType) {
    case "PERFORMER":
      return `/performers/${entityId}`;
    case "SCENE":
      return `/scenes/${entityId}`;
    default:
      return null;
  }
};

const ListItems: FC<{ list: List; isOwner: boolean }> = ({ list, isOwner }) => {
  const [removeItem] = useRemoveListItem({
    refetchQueries: ["List"],
    awaitRefetchQueries: true,
  });

  if (!list.items.length) {
    return (
      <Alert variant="secondary" className="mt-3">
        This list is empty.
      </Alert>
    );
  }

  return (
    <Table striped bordered hover responsive className="mt-3">
      <thead>
        <tr>
          <th style={{ width: "6rem" }}>#</th>
          <th>Type</th>
          <th>Entity</th>
          {isOwner && <th style={{ width: "8rem" }} />}
        </tr>
      </thead>
      <tbody>
        {list.items.map((item) => {
          const href = entityLink(item.entityType, item.entityId);
          return (
            <tr key={item.id}>
              {/* The stored position, not the index. They differ whenever an item was
                  removed from the middle, and showing the index would tell the user their
                  item moved when nothing did. */}
              <td>{item.position}</td>
              <td>{item.entityType}</td>
              <td>
                {href ? (
                  <Link to={href}>{item.entityId}</Link>
                ) : (
                  <span className="text-muted">{item.entityId}</span>
                )}
              </td>
              {isOwner && (
                <td className="text-end">
                  <Button
                    variant="outline-danger"
                    size="sm"
                    onClick={() =>
                      removeItem({ variables: { id: item.id } })
                    }
                  >
                    Remove
                  </Button>
                </td>
              )}
            </tr>
          );
        })}
      </tbody>
    </Table>
  );
};

// The publish toggle, and the place where the product decision is visible in the UI.
//
// There is one control for two states rather than a publish button and an unpublish button.
// A list is either private or public, and the state is one nullable timestamp on the server
// -- there is no third state to represent, so a two-button control would offer an action
// that does not exist.
const PublishToggle: FC<{ list: List }> = ({ list }) => {
  const [publish, { loading: publishing }] = usePublishList({
    refetchQueries: ["List", "MyLists", "PublishedLists"],
    awaitRefetchQueries: true,
  });
  const [unpublish, { loading: unpublishing }] = useUnpublishList({
    refetchQueries: ["List", "MyLists", "PublishedLists"],
    awaitRefetchQueries: true,
  });

  const busy = publishing || unpublishing;

  if (list.publishedAt) {
    return (
      <div className="d-flex align-items-center gap-3">
        <span className="badge bg-success">Published</span>
        <span className="text-muted small">
          since {new Date(list.publishedAt).toLocaleString()}
        </span>
        <Button
          variant="outline-secondary"
          size="sm"
          disabled={busy}
          onClick={() => unpublish({ variables: { id: list.id } })}
        >
          Make private
        </Button>
      </div>
    );
  }

  return (
    <div className="d-flex align-items-center gap-3">
      <span className="badge bg-secondary">Private</span>
      <Button
        variant="primary"
        size="sm"
        disabled={busy}
        onClick={() => publish({ variables: { id: list.id } })}
      >
        Publish
      </Button>
    </div>
  );
};

const ListView: FC<Props> = ({ list, isOwner }) => {
  return (
    <div className="List">
      <div className="d-flex justify-content-between align-items-start">
        <div>
          <h1 className="List-name">{list.name}</h1>
          {list.description && <p className="text-muted">{list.description}</p>}
          <p className="text-muted small mb-2">
            by{" "}
            {list.owner ? (
              <Link to={`/users/${list.owner.id}`}>{list.owner.name}</Link>
            ) : (
              "an unknown user"
            )}{" "}
            &middot; {list.itemCount}{" "}
            {list.itemCount === 1 ? "entry" : "entries"}
          </p>
        </div>

        <div className="text-end">
          {isOwner ? (
            <>
              <PublishToggle list={list} />
              <div className="mt-2">
                <Link
                  className="btn btn-outline-primary btn-sm"
                  to={createHref(ROUTE_LIST_EDIT, list)}
                >
                  Edit
                </Link>
              </div>
            </>
          ) : (
            // A viewer who is not the owner gets no controls at all, not disabled ones. The
            // publish action is the owner's alone, and showing a disabled button invites the
            // question of why it is disabled.
            <span className="text-muted small">
              {list.publishedAt ? "Published list" : "Private list"}
            </span>
          )}
        </div>
      </div>

      <hr />

      <ListItems list={list} isOwner={isOwner} />

      {list.auditTrail.length > 0 && (
        <>
          <h4 className="mt-4">History</h4>
          <Table borderless size="sm" className="text-muted small">
            <tbody>
              {list.auditTrail.map((entry) => (
                <tr key={entry.id}>
                  <td style={{ width: "12rem" }}>
                    {new Date(entry.createdAt).toLocaleString()}
                  </td>
                  <td style={{ width: "8rem" }}>
                    {entry.action === "PUBLISH" ? "Published" : "Made private"}
                  </td>
                  <td>
                    {/* A null actor is a deleted account, not a missing one. The record
                        survived on purpose -- deleting a user must not erase what they did --
                        so it renders as "a former member" rather than hiding the row. */}
                    {entry.actor ? (
                      <Link to={`/users/${entry.actor.id}`}>{entry.actor.name}</Link>
                    ) : (
                      "a former member"
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </Table>
        </>
      )}
    </div>
  );
};

export default ListView;
