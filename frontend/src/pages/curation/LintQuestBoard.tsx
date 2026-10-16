import type { FC } from "react";
import { Link } from "react-router-dom";
import { ErrorMessage, LoadingIndicator } from "src/components/fragments";
import { ROUTE_CURATION_LINT_DEFINITIONS } from "src/constants/route";
import { useLintQuestBoard } from "src/graphql";
import { useAuth } from "src/hooks";
import { isAdmin } from "src/utils";
import LintBoard from "./LintBoard";

/**
 * The data-lint quest board (SPEC §7.24.4, plan phase 3a).
 *
 * NO LIMIT IS PASSED, and that is a decision rather than an omission: the server
 * has a default and clamps whatever it is given, so a caller that does not know
 * how much room it has can send nothing and get a sane page. Passing a number
 * here would make a second place that decides how much of a detector's findings a
 * curator sees, and the two would drift.
 *
 * THE ERROR IS SHOWN VERBATIM. The service validates the registry before it
 * answers, and its refusal names the slug an operator has to fix ("enabled
 * detector(s) [x] have no implementation in this build (it implements [y])").
 * A generic "failed to load" would hide the one sentence that says what to do --
 * and this is an operator-visible instance fault, not a user error to apologise
 * for.
 *
 * The role gate is on the ROUTE (VOTE, like the rest of the curation tree) and on
 * the query field, so a user who cannot curate never renders this and never
 * fetches it.
 */
const LintQuestBoard: FC = () => {
  const { data, loading, error } = useLintQuestBoard();
  const { user } = useAuth();

  if (loading)
    return <LoadingIndicator message="Running the lint detectors..." />;
  if (error)
    return (
      <ErrorMessage
        error={`The lint board could not be produced: ${error.message}`}
      />
    );

  return (
    <>
      <LintBoard reports={data?.lintQuestBoard ?? []} />
      {/* An ADMIN reading the board can step one page over to the registry, which is
          the only place a detector that this build cannot run is visible at all --
          the board is refused while such a row stands, and the refusal names the
          slug without explaining where it comes from. Gated on the same predicate
          the nav uses, because the query behind the link is ADMIN. */}
      {isAdmin(user) && (
        <p className="text-muted small mt-3 mb-0">
          Running this archive? The{" "}
          <Link to={ROUTE_CURATION_LINT_DEFINITIONS}>detector registry</Link>{" "}
          lists every row, including the ones the board does not run.
        </p>
      )}
    </>
  );
};

export default LintQuestBoard;
