import type { FC } from "react";
import { Table } from "react-bootstrap";
import { Link } from "react-router-dom";
import { ErrorMessage, LoadingIndicator } from "src/components/fragments";
import { ROUTE_PERFORMER } from "src/constants/route";
import { EloEntityType, useEloLeaderboard } from "src/graphql";

/**
 * The performer Elo leaderboard.
 *
 * `voteCount` is rendered next to every rating, and that is the point of the
 * page rather than a detail. A rating is a mean over the votes cast, so a
 * three-vote rating and a three-hundred-vote rating can differ by a single point
 * while meaning very different things. The backend already breaks near-ties in
 * favour of the better-observed performer; showing the count is what makes that
 * rule visible instead of invisible, and stops a raw number from being read as a
 * settled fact.
 *
 * Nothing here rewards the top of the table either. This ranks performers, not
 * people, and the people who put them there are not credited on this page.
 */
const EloLeaderboard: FC = () => {
  const { data, loading, error } = useEloLeaderboard(
    EloEntityType.PERFORMER,
    50,
  );

  if (loading) return <LoadingIndicator message="Loading leaderboard..." />;
  if (error) return <ErrorMessage error="Failed to load the leaderboard." />;

  const entries = data?.eloLeaderboard?.entries ?? [];

  if (entries.length === 0)
    return (
      <p className="text-muted">
        Nothing has been rated yet. Voting on{" "}
        <Link to="/curation/matchup">matchups</Link> is what produces a ranking.
      </p>
    );

  return (
    <Table hover responsive>
      <thead>
        <tr>
          <th>#</th>
          <th>Performer</th>
          <th className="text-end">Rating</th>
          <th className="text-end">Votes</th>
        </tr>
      </thead>
      <tbody>
        {entries.map((e, i) => (
          <tr key={e.entityId}>
            <td>{i + 1}</td>
            <td>
              {e.performer ? (
                <Link to={ROUTE_PERFORMER.replace(":id", e.performer.id)}>
                  {e.performer.name}
                  {e.performer.disambiguation ? (
                    <span className="text-muted">
                      {" "}
                      ({e.performer.disambiguation})
                    </span>
                  ) : null}
                </Link>
              ) : (
                <span className="text-muted">deleted performer</span>
              )}
            </td>
            <td className="text-end">{e.rating}</td>
            {/* The vote count is shown for every row, including single-vote ones.
                A rating with no visible sample size is a number people trust too
                much. */}
            <td className="text-end text-muted">{e.voteCount}</td>
          </tr>
        ))}
      </tbody>
    </Table>
  );
};

export default EloLeaderboard;
