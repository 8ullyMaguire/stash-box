import type { FC } from "react";
import { Alert, Badge, Table } from "react-bootstrap";

/**
 * The instance's counters (spec §16.2, phase 16 slice 2), rendered as a table.
 *
 * A METRIC IS A NUMBER PLUS A DEFINITION, and this component exists to keep the two
 * together on the page as well as on the wire. Two readers of a bare "scenes_total" who
 * disagree about whether deleted scenes are counted are reading two different figures,
 * and nothing in a table of numbers would tell them so -- so the definition is a COLUMN
 * here, not a tooltip, because a definition behind a hover is invisible to a reader
 * printing the page and to one reading it with a screen reader.
 *
 * THREE THINGS THIS COMPONENT DELIBERATELY DOES NOT DO:
 *
 *  - It does not invent a prettier label for a figure. "members_total" is the name the
 *    reader will match against a report, a log line and this page's own JSON, and a
 *    second name invented here would be a second thing to keep in step.
 *  - It does not hide or skip a figure that arrives without a definition. That figure
 *    cannot be read, and dropping it would let the page look complete while a number
 *    went unexplained; it is shown WITH the fact that its definition is missing.
 *  - It does not decide for itself which figures are windowed. The server's own unit
 *    test requires every key containing "window" to say so in its definition, and this
 *    page marks exactly those keys -- so a figure bound to a window cannot be read here
 *    as a lifetime total.
 */

/** One figure as the server sends it. */
export interface InstanceMetricType {
  key: string;
  unit: string;
  definition: string;
  value: number;
}

/**
 * Thousands separators, ASCII commas, explicitly not the browser's locale.
 *
 * Deliberate: these figures are read beside the transparency report (§16.1) and beside a
 * terminal, and a page that grouped "1,104,375" one way in one locale and "1 104 375" in
 * another would make two readers compare different-looking numbers for one value.
 */
export const groupDigits = (value: number): string =>
  value.toString().replace(/\B(?=(\d{3})+(?!\d))/g, ",");

/**
 * Whether a figure is bound to the window, decided by its own key.
 *
 * The mirror of the server-side rule (`internal/service/metrics/metrics_test.go`): every
 * key containing "window" must carry a definition that says so, and those are exactly the
 * figures marked here. The vitest suite asserts the two halves agree on the fixture, so
 * the marker cannot drift away from the definitions it is explaining.
 */
export const isWindowed = (key: string): boolean => key.includes("window");

interface Props {
  metrics: InstanceMetricType[];
  /** The width of the window, as the response named it. */
  windowDays: number;
  /** The start of the window, as the response named it. */
  since: string;
}

const MetricsList: FC<Props> = ({ metrics, windowDays, since }) => {
  if (metrics.length === 0) {
    return (
      <Alert variant="info">
        This instance published no figures. That is a fact about the response
        and not about the catalogue: an empty list and a working instance that
        counts nothing are different things, and this page will not present the
        first as the second.
      </Alert>
    );
  }

  return (
    <>
      <p className="text-muted">
        Every figure marked <Badge bg="secondary">window</Badge> covers the{" "}
        {windowDays} days from {since}, and not the instance&apos;s whole
        history. The ones without the mark are lifetime totals: the two are
        different questions and are not added together anywhere on this page.
      </p>
      <Table striped bordered responsive>
        <thead>
          <tr>
            <th>Figure</th>
            <th>Value</th>
            <th>What it counts</th>
          </tr>
        </thead>
        <tbody>
          {metrics.map((metric) => (
            <tr key={metric.key}>
              <td>
                {metric.key}
                {isWindowed(metric.key) && (
                  <>
                    {" "}
                    <Badge bg="secondary">window</Badge>
                  </>
                )}
              </td>
              <td>
                {groupDigits(metric.value)}
                {metric.unit !== "count" && ` ${metric.unit}`}
              </td>
              <td>
                {metric.definition ? (
                  metric.definition
                ) : (
                  <span className="text-danger">
                    This figure arrived without a definition, so there is no way
                    to tell what it counts and it should not be trusted until
                    the instance explains it.
                  </span>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </Table>
    </>
  );
};

export default MetricsList;
