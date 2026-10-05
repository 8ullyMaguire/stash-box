import type { FC } from "react";
import { Alert, Table } from "react-bootstrap";
import { Link } from "react-router-dom";

import { ErrorMessage, LoadingIndicator } from "src/components/fragments";
import Title from "src/components/title";
import { ROUTE_LIST, ROUTE_LIST_ADD, ROUTE_LIST_PUBLISHED } from "src/constants/route";
import { useMyLists } from "src/graphql";
import { createHref } from "src/utils";

// "My lists", drafts included.
//
// The published/draft split is rendered as a badge on each row rather than as two tabs or a
// filter control. It is one bit per list, and a control that hides half the rows is a
// control that makes people wonder where the others went -- while a badge tells the truth
// about every row without removing any.
const MyLists: FC = () => {
  const { data, loading } = useMyLists();

  if (loading) return <LoadingIndicator message="Loading..." />;
  if (!data)
    return <ErrorMessage error="Could not load your lists" />;

  const lists = data.lists;

  return (
    <div className="MyLists">
      <Title page="My Lists" />

      <div className="d-flex justify-content-between align-items-center mb-3">
        <h1 className="mb-0">My Lists</h1>
        <div>
          <Link className="btn btn-outline-secondary btn-sm" to={ROUTE_LIST_PUBLISHED}>
            Browse public lists
          </Link>{" "}
          <Link className="btn btn-primary btn-sm" to={ROUTE_LIST_ADD}>
            New list
          </Link>
        </div>
      </div>

      {!lists.length ? (
        <Alert variant="secondary">
          You have no lists yet.{" "}
          <Link to={ROUTE_LIST_ADD}>Create one</Link>.
        </Alert>
      ) : (
        <Table striped bordered hover responsive>
          <thead>
            <tr>
              <th>Name</th>
              <th style={{ width: "8rem" }}>Status</th>
              <th style={{ width: "7rem" }}>Entries</th>
            </tr>
          </thead>
          <tbody>
            {lists.map((list) => (
              <tr key={list.id}>
                <td>
                  <Link to={createHref(ROUTE_LIST, list)}>{list.name}</Link>
                  {list.description && (
                    <div className="text-muted small">{list.description}</div>
                  )}
                </td>
                <td>
                  {list.publishedAt ? (
                    <span className="badge bg-success">Published</span>
                  ) : (
                    <span className="badge bg-secondary">Private</span>
                  )}
                </td>
                <td>{list.itemCount}</td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
    </div>
  );
};

export default MyLists;
