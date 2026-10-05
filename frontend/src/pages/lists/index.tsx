import type { FC } from "react";
import { Route, Routes, useParams } from "react-router-dom";

import { ErrorMessage, LoadingIndicator } from "src/components/fragments";
import Title from "src/components/title";
import { useList } from "src/graphql";
import { useCurrentUser } from "src/hooks";
import AddList from "./AddList";
import ListView from "./ListView";
import MyLists from "./MyLists";
import PublishedLists from "./PublishedLists";
import UpdateList from "./UpdateList";

// The owner gate, from the DATA rather than from a client-side "is this me" comparison.
//
// `owner { id }` comes back with the list and is compared against the session's id via
// useCurrentUser().isSelf -- the same helper Edit.tsx and User.tsx already use for this
// decision. A second, hand-rolled comparison would be a second source of truth for "am I
// the owner", and if it disagreed with the server's answer a stranger would be shown a
// Publish button that 403s.
const ListLoader: FC = () => {
  const { id } = useParams();
  const { isSelf } = useCurrentUser();
  const { data, loading } = useList({ id: id ?? "" }, !id);

  if (!id) return <ErrorMessage error="List ID is required" />;
  if (loading) return <LoadingIndicator message="Loading..." />;
  if (!data?.list)
    // "Not found, or you do not have access" -- one message for both, matching the
    // backend's deliberate refusal to distinguish them. Saying "you do not have access"
    // alone would confirm the list exists, which is the leak the backend goes to the
    // trouble of preventing.
    return <ErrorMessage error="List not found, or you do not have access to it" />;

  return (
    <Routes>
      {/* UpdateList's own loader, not the loaded data. /edit is reachable directly by
          URL, so the edit route must do its own visibility check rather than trust a
          parent that may never have been mounted. */}
      <Route path="/edit" element={<UpdateList />} />
      <Route
        path="/"
        element={
          <>
            <Title page={`List "${data.list.name}"`} />
            <ListView list={data.list} isOwner={isSelf(data.list.owner)} />
          </>
        }
      />
    </Routes>
  );
};

const ListRoutes: FC = () => (
  <Routes>
    <Route
      path="/"
      element={
        <>
          <Title page="Lists" />
          <MyLists />
        </>
      }
    />
    <Route path="/add" element={<AddList />} />
    <Route
      path="/published"
      element={
        <>
          <Title page="Public Lists" />
          <PublishedLists />
        </>
      }
    />
    <Route path="/:id/*" element={<ListLoader />} />
  </Routes>
);

export default ListRoutes;
