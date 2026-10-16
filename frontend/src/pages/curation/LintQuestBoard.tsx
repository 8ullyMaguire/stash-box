import type { FC } from "react";
import { ErrorMessage, LoadingIndicator } from "src/components/fragments";
import { useLintQuestBoard } from "src/graphql";
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

  if (loading)
    return <LoadingIndicator message="Running the lint detectors..." />;
  if (error)
    return (
      <ErrorMessage
        error={`The lint board could not be produced: ${error.message}`}
      />
    );

  return <LintBoard reports={data?.lintQuestBoard ?? []} />;
};

export default LintQuestBoard;
