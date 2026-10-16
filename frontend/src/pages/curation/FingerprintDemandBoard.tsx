import type { FC } from "react";
import { ErrorMessage, LoadingIndicator } from "src/components/fragments";
import { useFingerprintDemand } from "src/graphql";
import DemandTable from "./DemandTable";

/**
 * The unmatched-fingerprint demand board (SPEC §7.24.5, plan phase 3a gate 6).
 *
 * WHAT THIS SHOWS. An aggregate by hash: the algorithm, the hash, and how many
 * distinct members hold it. There is deliberately no link from a hash to a scene
 * search, which is the obvious next thing to add and the thing the boundary
 * forbids -- see DemandTable, which is where that absence is asserted.
 *
 * THE NUMBER IS A LOWER BOUND and the page says so: `count(DISTINCT user_id)`
 * makes one member with three copies contribute once, because the submission table
 * cannot tell three copies from one upload claimed three times. It can understate
 * how many copies exist and never overstate them, and a board that called it a
 * count would be wrong in the direction that matters.
 *
 * NO PARAMETERS ARE PASSED, following the lint board: the server has a default and
 * clamps whatever it is given, and passing a number here would make a second place
 * that decides how much of the board a curator sees. The floor the server applied
 * comes back in the response, which is what the copy reports.
 */
const FingerprintDemandBoard: FC = () => {
  const { data, loading, error } = useFingerprintDemand();

  if (loading)
    return <LoadingIndicator message="Aggregating fingerprints by hash..." />;
  if (error)
    return (
      <ErrorMessage
        error={`The demand board could not be produced: ${error.message}`}
      />
    );

  const demand = data?.fingerprintDemand;

  return (
    <>
      <h2 className="h5">Unmatched-fingerprint demand</h2>
      <p className="text-muted small">
        Hashes that more than one member has fingerprinted. The figure is a{" "}
        <strong>lower bound</strong> on how many copies exist: a member holding
        three copies counts once. Showing hashes held by{" "}
        {demand?.min_holders ?? 0} or more members.
      </p>
      <DemandTable
        entries={demand?.entries ?? []}
        floor={demand?.min_holders ?? 0}
      />
    </>
  );
};

export default FingerprintDemandBoard;
