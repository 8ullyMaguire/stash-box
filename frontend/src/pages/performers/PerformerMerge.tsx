import { type FC, useEffect, useMemo, useState } from "react";
import { Button, Col, Form, Row } from "react-bootstrap";
import { useNavigate, useSearchParams } from "react-router-dom";
import { Help, LoadingIndicator } from "src/components/fragments";
import PerformerCard from "src/components/performerCard";
import PerformerSelect from "src/components/performerSelect";
import {
  type FullPerformerQuery,
  OperationEnum,
  type PerformerEditDetailsInput,
  type PerformerFragment,
  type SearchPerformersQuery,
  usePerformerEdit,
} from "src/graphql";
import {
  PerformerFragmentDoc,
  SearchPerformerFragmentDoc,
} from "src/graphql/types";
import { useEntities } from "src/hooks";
import { editHref } from "src/utils";
import PerformerForm from "./performerForm";
import { buildPerformerMerge } from "./performerForm/merge";

type Performer = NonNullable<FullPerformerQuery["findPerformer"]>;
type SearchPerformer = NonNullable<
  SearchPerformersQuery["searchPerformers"]["performers"][number]
>;

const UPDATE_ALIAS_MESSAGE = `Enabling this option sets each merged performer's name as an alias on every scene that performer does not have an alias on.
In most cases, it should be enabled when merging aliases of a performer, and disabled when the performers share the same name.
`;

const CLASSNAME = "PerformerMerge";

interface Props {
  performer: Performer;
}

const PerformerMerge: FC<Props> = ({ performer }) => {
  const navigate = useNavigate();
  const [submissionError, setSubmissionError] = useState("");
  const [mergeActive, setMergeActive] = useState(false);
  const [mergeSources, setMergeSources] = useState<SearchPerformer[]>([]);
  const [aliasUpdating, setAliasUpdating] = useState(true);

  // ?source=<id> is how a lint finding's action arrives here (SPEC §7.24.4): the record a curator was
  // looking at, which is a source of this merge rather than its target -- the target is the record
  // this page is about.
  //
  // It is HYDRATED BEFORE THE FORM OPENS and then becomes the actual selection. Both halves matter: the
  // select renders the objects it is given, so a bare id would draw a nameless card; and the merge
  // submits `mergeSources`, so a source only drawn in the select would disappear when Continue is
  // pressed -- the reader would have confirmed a merge of nothing.
  const [searchParams] = useSearchParams();
  const prefilledSourceId = searchParams.get("source") ?? "";
  const prefilledIds = useMemo(
    () => (prefilledSourceId ? [{ id: prefilledSourceId }] : []),
    [prefilledSourceId],
  );
  // Hydrated with the SEARCH fragment the select itself renders, not the edit fragment: the two differ
  // by fields the select needs (scene_count among them) and a source hydrated the wrong way draws a card
  // the type system has already said is not what a search result is.
  const { sources: prefilledSources, ready: prefillReady } =
    useEntities<SearchPerformer>(
      prefilledIds,
      "findPerformer",
      SearchPerformerFragmentDoc,
      { enabled: prefilledIds.length > 0 },
    );

  useEffect(() => {
    if (prefilledSources.length > 0 && mergeSources.length === 0) {
      setMergeSources(prefilledSources);
    }
  }, [prefilledSources, mergeSources.length]);

  // THE SELECT SEEDS ITS OWN STATE FROM ITS `performers` PROP, ONCE, AT MOUNT. Handing it the
  // prefilled source after it has mounted is invisible: the page's state would carry the source (the
  // merge would submit it, Continue would be armed) while the field on screen still read "Search for
  // performers to merge..." -- measured, in exactly that state, before this gate existed. So the field
  // is not rendered until the prefill has resolved, and it is mounted WITH the source.
  const awaitingPrefill = prefilledIds.length > 0 && !prefillReady;
  const initialMergeSources =
    mergeSources.length > 0 ? mergeSources : prefilledSources;

  const {
    sources: loadedSources,
    ready: sourcesReady,
    error: sourcesError,
  } = useEntities<PerformerFragment>(
    mergeSources,
    "findPerformer",
    PerformerFragmentDoc,
    { enabled: mergeActive },
  );

  const [insertPerformerEdit, { loading: saving }] = usePerformerEdit({
    onCompleted: (data) => {
      if (submissionError) setSubmissionError("");
      if (data.performerEdit.id) navigate(editHref(data.performerEdit));
    },
    onError: (error) => setSubmissionError(error.message),
  });

  const toggleMerge = () => {
    setMergeActive(true);
    const sameName = mergeSources.every(
      ({ name }) => name.trim() === performer.name.trim(),
    );
    // Don't update aliases by default if the names match
    setAliasUpdating(!sameName);
  };

  const doUpdate = (
    insertData: PerformerEditDetailsInput,
    editNote: string,
    setModifyAliases: boolean,
  ) => {
    insertPerformerEdit({
      variables: {
        performerData: {
          edit: {
            id: performer.id,
            operation: OperationEnum.MERGE,
            merge_source_ids: mergeSources.map((p) => p.id),
            comment: editNote,
          },
          details: insertData,
          options: {
            set_merge_aliases: aliasUpdating,
            set_modify_aliases: setModifyAliases,
          },
        },
      },
    });
  };

  const { initial, conflicts } = useMemo(
    () => buildPerformerMerge(performer, loadedSources),
    [performer, loadedSources],
  );

  return (
    <div className={CLASSNAME}>
      <h3>
        Merge performers into <em>{performer.name}</em>
      </h3>
      <hr />
      <div className="row">
        <div className="col-6">
          {!mergeActive && awaitingPrefill && (
            // A moment, not a state the reader has to understand: the source is being fetched by id.
            <LoadingIndicator />
          )}
          {!mergeActive && !awaitingPrefill && (
            <>
              <label
                htmlFor="performer-merge-source-select"
                className="form-label"
              >
                Merge sources
              </label>
              <PerformerSelect
                performers={initialMergeSources}
                onChange={(performers) => setMergeSources(performers)}
                message="Search for performers to merge..."
                excludePerformers={[
                  performer.id,
                  ...mergeSources.map((p) => p.id),
                ]}
                inputId="performer-merge-source-select"
              />
              {mergeSources.length > 0 && (
                <Button onClick={toggleMerge} className="ms-auto">
                  Continue
                </Button>
              )}
            </>
          )}
          {mergeActive && (
            <Row>
              <Col xs={3}>
                <h6 className="text-center">Merge Target</h6>
                <PerformerCard performer={performer} className="TargetCard" />
              </Col>
              <Col xs={9}>
                <Row className="mt-4">
                  {mergeSources.map((source) => (
                    <Col xs={4} key={source.id}>
                      <PerformerCard performer={source} />
                    </Col>
                  ))}
                </Row>
              </Col>
            </Row>
          )}
        </div>
        <div className="col-6">
          <p>
            Merging performers reassigns all scene performances of the sources
            to the target performer. The source <i>stashIds</i> will be
            redirected so that previously tagged content can be updated with the
            new performers metadata.
          </p>
          <p>
            This operation is not easily reversible and attention should be paid
            that all entities are truly the same.
          </p>
        </div>
      </div>
      {mergeActive && (
        <>
          <Form.Check
            id="merge-alias-updating"
            checked={aliasUpdating}
            onChange={() => setAliasUpdating(!aliasUpdating)}
            label="Update scene performance aliases on merged performers to old performer name."
            className="d-inline-block"
          />
          <Help message={UPDATE_ALIAS_MESSAGE} />
          <h5 className="mt-4">
            Update performer metadata for <em>{performer.name}</em>
          </h5>
          {sourcesError ? (
            <div className="text-danger">
              Failed to load performer details: {sourcesError.message}
            </div>
          ) : sourcesReady ? (
            <PerformerForm
              performer={performer}
              initial={initial}
              conflicts={conflicts}
              callback={doUpdate}
              saving={saving}
            />
          ) : (
            <LoadingIndicator message="Loading performer details..." />
          )}
          {submissionError && (
            <div className="text-danger text-end col-9">
              Error: {submissionError}
            </div>
          )}
        </>
      )}
    </div>
  );
};

export default PerformerMerge;
