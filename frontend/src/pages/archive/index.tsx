import type { FC } from "react";
import { Alert, Spinner } from "react-bootstrap";

import Title from "src/components/title";
import { useArchiveStateQuery } from "src/graphql/queries";

import ArchiveState, { type ArchiveStateType } from "./ArchiveState";

/**
 * The /archive route: State of the Archive (SPEC §7.7, growth item 9).
 *
 * READ-gated, like the query it calls. It is deliberately a top-level route with
 * an ungated nav entry: the archive's own health is something every reader can
 * be told, and a health figure hidden behind VOTE would be a strange thing to
 * withhold from the people browsing the archive.
 */
const ArchiveStatePage: FC = () => {
  const { data, loading, error } = useArchiveStateQuery();

  if (loading) {
    return (
      <>
        <Title page="State of the Archive" />
        <Spinner animation="border" role="status" />
      </>
    );
  }

  if (error) {
    return (
      <>
        <Title page="State of the Archive" />
        <Alert variant="danger">
          The archive counts could not be read. This is usually a database that
          is still migrating, not a permissions problem — the query needs READ,
          which every signed-in role has.
        </Alert>
      </>
    );
  }

  // The five incomplete* fields are aliased per type because GraphQL has no way
  // to say "call this field once per item of another field". Pairing them here is
  // the one place that mapping exists, and it is a switch on the same enum
  // spellings the backend returns, so a new type shows up as a missing card
  // rather than as a silently wrong percentage.
  const incompleteByType: Record<string, number | undefined> = {
    performer: data?.incompletePerformers,
    scene: data?.incompleteScenes,
    studio: data?.incompleteStudios,
    site: data?.incompleteSites,
    tag: data?.incompleteTags,
  };

  const states: ArchiveStateType[] = (data?.archiveEntityCounts ?? []).map(
    (c) => ({
      entityType: String(c.entityType).toLowerCase(),
      total: c.count,
      incomplete: incompleteByType[String(c.entityType).toLowerCase()] ?? null,
    }),
  );

  return (
    <>
      <Title page="State of the Archive" />
      <ArchiveState states={states} />
    </>
  );
};

export default ArchiveStatePage;
