import type { FC } from "react";
import { Alert } from "react-bootstrap";
import { LoadingIndicator } from "src/components/fragments";
import { useLintQuestDefinitions } from "src/graphql";
import LintDefinitions from "./LintDefinitions";

/**
 * The detector registry (SPEC §7.24.4, plan phase 3a).
 *
 * ADMIN, unlike the board next to it, because the query behind it is: the board is
 * a curator's work list and this is the table an operator edits to retire a noisy
 * detector or price a new one. The nav entry and the schema agree, so the people
 * who see the link are the people the server will answer — and a VOTE-only curator
 * who reaches the URL anyway is told it is a permission rather than handed a fault.
 *
 * THE REFUSAL IS NAMED AS A REFUSAL. `/metrics` sets that precedent on this
 * instance and it is followed here rather than re-invented: "the server said not
 * authorized" sends an operator looking for a broken table, when what happened is
 * that they asked a question addressed to somebody else.
 */

export const describeRegistryError = (
  message: string,
): { variant: "warning" | "danger"; text: string } => {
  if (/not authorized/i.test(message)) {
    return {
      variant: "warning",
      text: "The detector registry is an operator's table, and reading it needs the ADMIN role. Ask an administrator if you need it — nothing is broken.",
    };
  }

  return {
    variant: "danger",
    text: `The detector registry could not be read. This is a fault rather than a permission: the server said ${message}`,
  };
};

const LintQuestDefinitions: FC = () => {
  const { data, loading, error } = useLintQuestDefinitions();

  if (loading)
    return <LoadingIndicator message="Reading the detector registry..." />;

  if (error) {
    // Rendered directly rather than through <ErrorMessage>, because that component
    // takes a single string and the whole point here is the variant: a refusal that
    // looks like a fault is the defect this branch exists to prevent.
    const { variant, text } = describeRegistryError(error.message);
    return (
      <Alert variant={variant} className="mb-0">
        {text}
      </Alert>
    );
  }

  return <LintDefinitions definitions={data?.lintQuestDefinitions ?? []} />;
};

export default LintQuestDefinitions;
