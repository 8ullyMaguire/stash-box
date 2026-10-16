import type { FC } from "react";
import { Alert } from "react-bootstrap";
import { useLintFindingsFor } from "src/graphql";
import { useAuth } from "src/hooks";
import { canVote } from "src/utils";
import Findings from "./Findings";
import "./styles.scss";

interface Props {
  // "performer" or "scene" -- the two entity types the validation pass has detectors for.
  entityType: string;
  entityId: string;
}

// What ONE record is flagged for (SPEC §7.24.4's validation pass, asked about a single id).
//
// THE QUERY IS GATED VOTE AT THE SCHEMA, so a reader who cannot vote must not send it: the component
// renders nothing for them, and does not send the request either (the hook is skipped). A refusal is
// the server's to state and not this panel's to show.
//
// THREE STATES, and the difference between the second and the third is the whole reason this is a
// container rather than a one-line map over a list:
//
//   findings   -- the panel, and only the flows that already exist elsewhere in the app.
//   none       -- NOTHING AT ALL. A record with no findings says nothing, so that the records which
//                 are flagged are the ones a reader notices. (A card saying "no problems" on every
//                 page is a card nobody reads.)
//   a fault    -- the refusal, named. A stranded detector makes the pass refuse the whole board, and a
//                 panel that rendered that as "nothing here" would be reporting a quiet instance
//                 while the check had in fact stopped running.
const RecordFindings: FC<Props> = ({ entityType, entityId }) => {
  const { user } = useAuth();
  const allowed = canVote(user);
  const { data, error, loading } = useLintFindingsFor(
    entityType,
    entityId,
    !allowed || !entityId,
  );

  if (!allowed) return null;

  if (error) {
    return (
      <Alert variant="danger" className="RecordFindings">
        <span className="RecordFindings-heading">
          The data checks could not be run for this record.
        </span>
        <span className="RecordFindings-sub">{error.message}</span>
      </Alert>
    );
  }

  // Loading renders nothing, like a clean record: a strip that appears after the page has settled is
  // readable, while one that flashes "flagged!" and then empties is not.
  if (loading) return null;

  const reports = data?.lintFindingsFor ?? [];
  if (reports.length === 0) return null;

  return (
    <Findings entityType={entityType} entityId={entityId} reports={reports} />
  );
};

export default RecordFindings;
