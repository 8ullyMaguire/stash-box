import { type FC, useState } from "react";
import { useNavigate } from "react-router-dom";
import { toTypedImages } from "src/components/editImages";

import {
  isMergeEdit,
  type MergeSource,
  MergeSourceEditor,
} from "src/components/mergeSourceEditor";
import StudioSelect from "src/components/studioSelect";
import {
  type EditUpdateQuery,
  type StudioEditDetailsInput,
  type StudioFragment,
  useStudioEditUpdate,
} from "src/graphql";
import { StudioFragmentDoc } from "src/graphql/types";
import { useEntities } from "src/hooks";
import { createHref, isStudio, isStudioEdit } from "src/utils";
import StudioForm from "./studioForm";

type EditUpdate = NonNullable<EditUpdateQuery["findEdit"]>;

import Title from "src/components/title";
import { ROUTE_EDIT } from "src/constants";

export const StudioEditUpdate: FC<{ edit: EditUpdate }> = ({ edit }) => {
  const navigate = useNavigate();
  const [submissionError, setSubmissionError] = useState("");
  const isMerge = isMergeEdit(edit.operation);

  // Seeded from the edit so the current sources are visible and adjustable
  // (#703: the update form offered no way to touch the merge).
  const [mergeSources, setMergeSources] = useState<MergeSource[]>(
    edit.merge_sources.map((s) => ({ id: s.id, name: "" })),
  );

  const { sources: loadedSources } = useEntities<StudioFragment>(
    mergeSources,
    "findStudio",
    StudioFragmentDoc,
    { enabled: isMerge },
  );

  const [updateStudioEdit, { loading: saving }] = useStudioEditUpdate({
    onCompleted: (result) => {
      if (submissionError) setSubmissionError("");
      if (result.studioEditUpdate.id)
        navigate(createHref(ROUTE_EDIT, result.studioEditUpdate));
    },
    onError: (error) => setSubmissionError(error.message),
  });

  if (
    !isStudioEdit(edit.details) ||
    (edit.target !== null && !isStudio(edit.target))
  )
    return null;

  const doUpdate = (updateData: StudioEditDetailsInput, editNote: string) => {
    updateStudioEdit({
      variables: {
        id: edit.id,
        studioData: {
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

  const studioName = edit?.target?.name ?? edit.details?.name;

  const nameById = new Map(loadedSources.map((t) => [t.id, t.name]));
  const named = mergeSources.map((s) => ({
    ...s,
    name: nameById.get(s.id) ?? s.name,
  }));

  return (
    <div>
      <Title page={`Update studio edit for "${studioName}"`} />
      <h3>
        Update studio edit for
        <i className="ms-2">
          <b>{studioName}</b>
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
            {() => (
              // StudioSelect is single-value, so each pick adds one source.
              <StudioSelect
                onChange={(studio) => {
                  if (!studio) return;
                  setMergeSources((curr) =>
                    curr.some((s) => s.id === studio.id)
                      ? curr
                      : [...curr, { id: studio.id, name: studio.name }],
                  );
                }}
                excludeStudio={edit.target?.id}
                isClearable
                inputId="studio-merge-source-select"
              />
            )}
          </MergeSourceEditor>
          <hr className="my-4" />
        </>
      )}
      <StudioForm
        studio={edit.target}
        initial={{
          ...edit.details,
          images: toTypedImages(edit.details.images),
        }}
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
