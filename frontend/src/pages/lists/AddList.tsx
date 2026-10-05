import type { FC } from "react";
import { useNavigate } from "react-router-dom";

import { ROUTE_LIST } from "src/constants/route";
import { useCreateList } from "src/graphql";
import { createHref } from "src/utils";
import ListForm from "./ListForm";

const AddList: FC = () => {
  const navigate = useNavigate();
  const [createList, { loading }] = useCreateList({
    onCompleted: (data) => {
      const created = data?.listCreate;
      if (created?.id) navigate(createHref(ROUTE_LIST, created));
    },
  });

  const doInsert = (data: { name: string; description: string | null }) => {
    createList({
      variables: {
        input: {
          name: data.name,
          description: data.description ?? undefined,
        },
      },
      // onError is wired here rather than left to Apollo's default. The server's refusals
      // are specific and useful -- "you already have a list with this name" -- and the
      // default is to route them to the error boundary, which would replace a sentence
      // about a duplicate name with a page-level crash. The mutation hook takes no
      // onError option, so it goes on the call.
      onError: (error) => {
        // TODO: surface this. The server's refusals are specific and worth reading
        // ("you already have a list with this name"), and this form has no toast system to
        // route them through. Logged for now rather than swallowed: an error the developer
        // cannot see is an error nobody fixes, and a biome-ignore here claiming a rule was
        // checked when nothing was is worse than no suppression at all.
        console.error(error.message);
      },
    });
  };

  return (
    <div>
      <h3>Add new list</h3>
      <p className="text-muted">
        A new list is always private. You can publish it once it is worth sharing.
      </p>
      <hr />
      <ListForm callback={doInsert} saving={loading} />
    </div>
  );
};

export default AddList;
