import { type FC, useState } from "react";
import { useNavigate } from "react-router-dom";

import {
  isMergeEdit,
  type MergeSource,
  MergeSourceEditor,
} from "src/components/mergeSourceEditor";
import TagSelect from "src/components/tagSelect";
import Title from "src/components/title";
import { ROUTE_EDIT } from "src/constants";
import {
  type EditUpdateQuery,
  type TagFragment as Tag,
  type TagEditDetailsInput,
  useTagEditUpdate,
} from "src/graphql";
import { TagFragmentDoc } from "src/graphql/types";
import { useEntities } from "src/hooks";
import { createHref, isTag, isTagEdit } from "src/utils";
import TagForm from "./tagForm";

type EditUpdate = NonNullable<EditUpdateQuery["findEdit"]>;

export const TagEditUpdate: FC<{ edit: EditUpdate }> = ({ edit }) => {
  const navigate = useNavigate();
  const [submissionError, setSubmissionError] = useState("");
  const isMerge = isMergeEdit(edit.operation);

  // Seeded from the edit so the current sources are visible immediately and can
  // be added to or removed from (#703: the update form used to render the plain
  // tag form with no way to touch the merge at all).
  const [mergeSources, setMergeSources] = useState<MergeSource[]>(
    edit.merge_sources.map((s) => ({ id: s.id, name: "" })),
  );

  // Resolves the seeded ids into names. The edit query returns ids only.
  const { sources: loadedSources } = useEntities<Tag>(
    mergeSources,
    "findTag",
    TagFragmentDoc,
    { enabled: isMerge },
  );

  const [updateTagEdit, { loading: saving }] = useTagEditUpdate({
    onCompleted: (result) => {
      if (submissionError) setSubmissionError("");
      if (result.tagEditUpdate.id)
        navigate(createHref(ROUTE_EDIT, result.tagEditUpdate));
    },
    onError: (error) => setSubmissionError(error.message),
  });

  if (!isTagEdit(edit.details) || (edit.target && !isTag(edit.target)))
    return null;

  const doUpdate = (updateData: TagEditDetailsInput, editNote: string) => {
    updateTagEdit({
      variables: {
        id: edit.id,
        tagData: {
          edit: {
            id: edit.target?.id,
            operation: edit.operation,
            comment: editNote,
            merge_source_ids: isMerge
              ? mergeSources.map((s) => s.id)
              : edit.merge_sources.map((s) => s.id),
          },
          details: updateData,
        },
      },
    });
  };

  const tagName = edit.target?.name ?? edit.details.name;

  // Names arrive asynchronously; show the fetched name once it is known.
  const nameById = new Map(loadedSources.map((t) => [t.id, t.name]));
  const named = mergeSources.map((s) => ({
    ...s,
    name: nameById.get(s.id) ?? s.name,
  }));

  return (
    <div>
      <Title page={`Update tag edit for "${tagName}"`} />
      <h3>
        Update tag edit for
        <i className="ms-2">
          <b>{tagName}</b>
        </i>
      </h3>
      <hr />
      {isMerge && (
        <>
          <MergeSourceEditor
            sources={named}
            onChange={setMergeSources}
            targetId={edit.target?.id}
          >
            {(excludeIds) => (
              <TagSelect
                tags={[]}
                onChange={(tags) =>
                  setMergeSources((curr) => [
                    ...curr,
                    ...tags.map((t) => ({ id: t.id, name: t.name })),
                  ])
                }
                message="Select tags to merge:"
                excludeTags={excludeIds}
                inputId="tag-merge-source-select"
              />
            )}
          </MergeSourceEditor>
          <hr className="my-4" />
        </>
      )}
      <TagForm
        tag={edit.target}
        initial={edit.details}
        callback={doUpdate}
        saving={saving}
      />
      {submissionError && (
        <div className="text-danger text-end col-9">
          Error: {submissionError}
        </div>
      )}
    </div>
  );
};
