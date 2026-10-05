import type { FC } from "react";
import { Alert, Table } from "react-bootstrap";
import { Link } from "react-router-dom";

import { ErrorMessage, LoadingIndicator } from "src/components/fragments";
import Title from "src/components/title";
import { ROUTE_LIST, ROUTE_LISTS } from "src/constants/route";
import { usePublishedLists } from "src/graphql";
import { createHref } from "src/utils";

const PER_PAGE = 25;

// The public browse listing.
//
// Every row here is public by construction, and the reason the page needs no filter control
// at all is that `publishedLists` cannot return a draft -- there is no argument that would
// ask it to. The status column is therefore omitted rather than rendered: on this page every
// row is published, and a column of identical badges is noise that implies the possibility
// of something else.
const PublishedLists: FC = () => {
  const { data, loading } = usePublishedLists({ perPage: PER_PAGE });

  if (loading) return <LoadingIndicator message="Loading..." />;
  if (!data?.publishedLists)
    return <ErrorMessage error="Could not load published lists" />;

  const { lists, count } = data.publishedLists;

  return (
    <div className="PublishedLists">
      <Title page="Public Lists" />

      <div className="d-flex justify-content-between align-items-center mb-3">
        <h1 className="mb-0">
          Public Lists{" "}
          <span className="fs-5 text-muted">
            {count} {count === 1 ? "list" : "lists"}
          </span>
        </h1>
        <Link className="btn btn-outline-secondary btn-sm" to={ROUTE_LISTS}>
          My lists
        </Link>
      </div>

      {!lists.length ? (
        <Alert variant="secondary">
          Nobody has published a list yet.
        </Alert>
      ) : (
        <Table striped bordered hover responsive>
          <thead>
            <tr>
              <th>Name</th>
              <th style={{ width: "12rem" }}>By</th>
              <th style={{ width: "7rem" }}>Entries</th>
              <th style={{ width: "12rem" }}>Published</th>
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
                  {list.owner ? (
                    <Link to={`/users/${list.owner.id}`}>{list.owner.name}</Link>
                  ) : (
                    <span className="text-muted">unknown</span>
                  )}
                </td>
                <td>{list.itemCount}</td>
                <td className="text-muted small">
                  {list.publishedAt
                    ? new Date(list.publishedAt).toLocaleDateString()
                    : ""}
                </td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
    </div>
  );
};

export default PublishedLists;
