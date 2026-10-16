import type { FC } from "react";
import { Badge, Card, Table } from "react-bootstrap";
import type { LintQuestDefinitionsQuery } from "src/graphql";

/**
 * The detector registry (SPEC §7.24.4), as an operator's table.
 *
 * THE THREE STATES ARE THE POINT, and they are three because the row carries two
 * independent facts -- `enabled` (an operator's switch, stored) and `implemented`
 * (whether THIS binary has a query for the slug, computed). Collapsing them into
 * one "active" flag is exactly what makes a misconfiguration unreadable:
 *
 *   1. RUNNING     enabled + implemented -- the board runs it.
 *   2. STRANDED    enabled + NOT implemented -- somebody switched on a detector
 *                  this build has never heard of. Nothing runs it, and the
 *                  service's own validation means the BOARD IS REFUSED while this
 *                  row stands, so every curator sees an error instead of a work
 *                  list. This is the state that must not be reachable-looking, and
 *                  it is why the registry is worth a page at all: from the board
 *                  alone it is indistinguishable from a broken instance.
 *   3. RETIRED     disabled, implemented or not -- switched off without deleting
 *                  the row that records its price and the fact that it once ran.
 *
 * There is NO CONTROL on this page, and that is not an omission: the registry is
 * written by migration 98 and by `cmd/lintquests -ensure`, which is where a
 * deploy's rows belong. A switch here would be a second writer with no audit
 * trail, and the schema exposes no mutation to be its reader. So the page names
 * the writer instead, which is the only honest thing a read-only view can do.
 */

type Definitions = LintQuestDefinitionsQuery["lintQuestDefinitions"];
type Definition = Definitions[number];

const stateOf = (def: Definition) => {
  if (def.enabled && !def.implemented) return "stranded" as const;
  if (def.enabled) return "running" as const;
  return "retired" as const;
};

const StateBadge: FC<{ def: Definition }> = ({ def }) => {
  switch (stateOf(def)) {
    case "running":
      return <Badge bg="success">Running</Badge>;
    case "stranded":
      // Named as what it does, not as an error code: "enabled but unimplemented"
      // is the operator's own vocabulary for the thing they did.
      return (
        <Badge bg="danger">Switched on, but this build cannot run it</Badge>
      );
    default:
      return (
        <Badge bg="secondary">
          {def.implemented ? "Retired" : "Retired, and not in this build"}
        </Badge>
      );
  }
};

const DefinitionsTable: FC<{ definitions: Definitions }> = ({
  definitions,
}) => (
  <Table responsive>
    <thead>
      <tr>
        <th>Detector</th>
        <th>What it looks for</th>
        <th>Points</th>
        <th>Origin</th>
        <th>State</th>
      </tr>
    </thead>
    <tbody>
      {definitions.map((def) => (
        <tr key={def.slug}>
          {/* The slug is the key an operator writes in the registry AND the name a
              build implements, so it is the string that has to match. Monospaced
              and unbroken, because the failure this page exists to make legible is
              a slug that does not. */}
          <td>
            <code>{def.slug}</code>
          </td>
          <td className="text-muted small">{def.description}</td>
          <td>{def.bountyPoints}</td>
          <td className="text-muted small">
            {def.isBuiltin ? "Ships with this fork" : "Added by an operator"}
          </td>
          <td>
            <StateBadge def={def} />
          </td>
        </tr>
      ))}
    </tbody>
  </Table>
);

const LintDefinitions: FC<{ definitions: Definitions; loading?: boolean }> = ({
  definitions,
  loading,
}) => {
  if (loading) return null;

  const stranded = definitions.filter((d) => stateOf(d) === "stranded");
  const running = definitions.filter((d) => stateOf(d) === "running");

  return (
    <div className="d-flex flex-column gap-3">
      {/*
        NO ROWS AT ALL is its own state, and it is a server fault rather than an
        empty table: migration 98 seeds the builtins at startup, so a registry with
        nothing in it means the seed did not run. Rendering that as "no detectors"
        would describe a working instance with nothing to do.
      */}
      {definitions.length === 0 ? (
        <Card>
          <Card.Body>
            <p className="text-muted mb-0">
              The registry came back empty. That is not a quiet instance — this
              build seeds its detectors at startup, so an empty registry means
              the seed did not run.
            </p>
          </Card.Body>
        </Card>
      ) : (
        <>
          {stranded.length > 0 && (
            <Card border="danger">
              <Card.Body>
                <h5 className="mb-2">
                  {stranded.length} detector{stranded.length === 1 ? "" : "s"}{" "}
                  switched on that this build cannot run
                </h5>
                <p className="text-muted mb-0">
                  {stranded.map((d) => d.slug).join(", ")} — the board is
                  refused while a row like this stands, so curators are seeing
                  an error rather than a work list. Either this instance was
                  rolled back past the release that shipped the detector, or the
                  slug was mistyped. Switch the row off, or put the matching
                  build back.
                </p>
              </Card.Body>
            </Card>
          )}

          <Card>
            <Card.Header className="d-flex justify-content-between align-items-center">
              <h4 className="mb-0">Detectors</h4>
              <span className="text-muted small">
                {running.length} of {definitions.length} running
              </span>
            </Card.Header>
            <Card.Body className="p-0">
              <DefinitionsTable definitions={definitions} />
            </Card.Body>
            <Card.Footer className="text-muted small">
              Nothing here is edited from this page. The registry is written by
              the migration that seeds it and by <code>lintquests -ensure</code>
              , so a change stays where a deploy can review it.
            </Card.Footer>
          </Card>
        </>
      )}
    </div>
  );
};

export default LintDefinitions;
