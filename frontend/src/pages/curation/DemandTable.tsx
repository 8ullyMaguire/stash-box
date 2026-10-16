import type { FC } from "react";

/**
 * The demand board's table (SPEC §7.24.5).
 *
 * Split from the container the way the lint board is, because the rules here are
 * about what the table MAY NOT contain, and a rule that can only be checked by
 * mounting a component with a router and a GraphQL provider is a rule nobody
 * checks:
 *
 *   - there is NO LINK on a hash. The obvious next thing to add is a link into a
 *     scene search, and it is the thing the boundary forbids: a row that pointed at
 *     a copy would make this board a locator for content the instance does not hold.
 *     The integration suite asserts the same thing about the API's field set.
 *   - the count is labelled as a LOWER BOUND, in the column header rather than a
 *     tooltip, because one member with three copies counts once.
 */
const DemandTable: FC<{
  entries: ReadonlyArray<{
    algorithm: string;
    hash: string;
    holder_lower_bound: number;
  }>;
  floor: number;
}> = ({ entries, floor }) => {
  if (entries.length === 0)
    return (
      <p className="text-muted">
        No hash is held by {floor} or more members yet. That is the answer to
        the question, not a failure to load it.
      </p>
    );

  return (
    <table className="table table-striped">
      <thead>
        <tr>
          <th scope="col">Algorithm</th>
          <th scope="col">Hash</th>
          {/* "at least" belongs in the header: a bare number reads as a count. */}
          <th scope="col">Holders (at least)</th>
        </tr>
      </thead>
      <tbody>
        {entries.map((entry) => (
          <tr key={`${entry.algorithm}-${entry.hash}`}>
            <td>{entry.algorithm}</td>
            <td>
              <code>{entry.hash}</code>
            </td>
            <td>{entry.holder_lower_bound}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
};

export default DemandTable;
