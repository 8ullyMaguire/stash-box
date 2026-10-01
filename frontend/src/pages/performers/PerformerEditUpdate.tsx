import { type FC, useState } from "react";
import { useNavigate } from "react-router-dom";
import { toTypedImages } from "src/components/editImages";

import {
  isMergeEdit,
  type MergeSource,
  MergeSourceEditor,
} from "src/components/mergeSourceEditor";
import PerformerSelect from "src/components/performerSelect";
import {
  type EditUpdateQuery,
  type PerformerEditDetailsInput,
  type PerformerFragment,
  usePerformerEditUpdate,
} from "src/graphql";
import { PerformerFragmentDoc } from "src/graphql/types";
import { useEntities } from "src/hooks";
import { createHref, isPerformer, isPerformerEdit } from "src/utils";
import PerformerForm from "./performerForm";

type EditUpdate = NonNullable<EditUpdateQuery["findEdit"]>;

import Title from "src/components/title";
import { ROUTE_EDIT } from "src/constants";

export const PerformerEditUpdate: FC<{ edit: EditUpdate }> = ({ edit }) => {
  const navigate = useNavigate();
  const [submissionError, setSubmissionError] = useState("");
  const isMerge = isMergeEdit(edit.operation);

  // Seeded from the edit so the current sources are visible and adjustable
  // (#703: the update form offered no way to touch the merge).
  const [mergeSources, setMergeSources] = useState<MergeSource[]>(
    edit.merge_sources.map((s) => ({ id: s.id, name: "" })),
  );

  const { sources: loadedSources } = useEntities<PerformerFragment>(
    mergeSources,
    "findPerformer",
    PerformerFragmentDoc,
    { enabled: isMerge },
  );

  const [updatePerformerEdit, { loading: saving }] = usePerformerEditUpdate({
    onCompleted: (result) => {
      if (submissionError) setSubmissionError("");
      if (result.performerEditUpdate.id)
        navigate(createHref(ROUTE_EDIT, result.performerEditUpdate));
    },
    onError: (error) => setSubmissionError(error.message),
  });

  if (
    !isPerformerEdit(edit.details) ||
    (edit.target && !isPerformer(edit.target))
  )
    return null;

  const doUpdate = (
    updateData: PerformerEditDetailsInput,
    editNote: string,
    setModifyAliases: boolean,
  ) => {
    if (!isPerformerEdit(edit.details)) return;

    const details: PerformerEditDetailsInput = {
      ...updateData,
      draft_id: edit.details.draft_id,
    };
    updatePerformerEdit({
      variables: {
        id: edit.id,
        performerData: {
          edit: {
            id: edit.target?.id,
            operation: edit.operation,
            comment: editNote,
            merge_source_ids: isMerge
              ? mergeSources.map((s) => s.id)
              : edit.merge_sources.map((s) => s.id),
          },
          options: {
            set_modify_aliases: setModifyAliases,
            set_merge_aliases: edit.options?.set_merge_aliases,
          },
          details,
        },
      },
    });
  };

  const performerName = edit.target?.name ?? edit.details.name;

  const nameById = new Map(loadedSources.map((t) => [t.id, t.name]));
  const named = mergeSources.map((s) => ({
    ...s,
    name: nameById.get(s.id) ?? s.name,
  }));

  return (
    <div>
      <Title page={`Update performer edit for "${performerName}"`} />
      <h3>
        Update performer edit for
        <i className="ms-2">
          <b>{performerName}</b>
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
              <PerformerSelect
                performers={[]}
                onChange={(performers) =>
                  setMergeSources((curr) => [
                    ...curr,
                    ...performers.map((p) => ({ id: p.id, name: p.name })),
                  ])
                }
                message="Search for performers to merge..."
                excludePerformers={excludeIds}
                inputId="performer-merge-source-select"
              />
            )}
          </MergeSourceEditor>
          <hr className="my-4" />
        </>
      )}
      <PerformerForm
        performer={edit.target}
        initial={{
          ...edit.details,
          images: toTypedImages(edit.details.images),
        }}
        options={edit.options}
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
