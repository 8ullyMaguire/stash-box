import type { FC } from "react";
import { useNavigate, useParams } from "react-router-dom";

import { ErrorMessage, LoadingIndicator } from "src/components/fragments";
import Title from "src/components/title";
import { ROUTE_LIST } from "src/constants/route";
import { type ListQuery, useList, useUpdateList } from "src/graphql";
import { createHref } from "src/utils";
import ListForm from "./ListForm";

type List = NonNullable<ListQuery["list"]>;

interface Props {
  list: List;
}

const UpdateList: FC<Props> = ({ list }) => {
  const navigate = useNavigate();
  const [updateList, { loading }] = useUpdateList({
    onCompleted: (result) => {
      if (result?.listUpdate?.id)
        navigate(createHref(ROUTE_LIST, result.listUpdate));
    },
  });

  const doUpdate = (data: { name: string; description: string | null }) => {
    updateList({
      variables: {
        input: {
          id: list.id,
          name: data.name,
          description: data.description ?? undefined,
        },
      },
      onError: (error) => {
        // TODO: surface this -- see AddList.tsx.
        console.error(error.message);
      },
    });
  };

  return (
    <div>
      <Title page={`Edit List "${list.name}"`} />
      <h3>
        Update <em>{list.name}</em>
      </h3>
      <hr />
      <ListForm list={list} callback={doUpdate} saving={loading} />
    </div>
  );
};

// The loader, rather than a useParams call inside UpdateList. This route is only reachable
// from a list the viewer can already see, but the loader re-checks anyway: if the list has
// been deleted, or was unpublished between navigating and landing, `list(id:)` answers null
// and this must say so rather than rendering a form for a row that is not there.
const UpdateListLoader: FC = () => {
  const { id } = useParams();
  const { data, loading } = useList({ id: id ?? "" }, !id);

  if (!id) return <ErrorMessage error="List ID is required" />;
  if (loading) return <LoadingIndicator message="Loading..." />;
  if (!data?.list)
    return <ErrorMessage error="List not found, or you do not have access to it" />;

  return <UpdateList list={data.list} />;
};

export default UpdateListLoader;
