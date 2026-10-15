import type { FC } from "react";
import { Alert, Spinner } from "react-bootstrap";
import Title from "src/components/title";
import { useInstanceMetricsQuery } from "src/graphql/queries";

import MetricsList from "./MetricsList";

/**
 * The /metrics route: this instance's own counters (spec §16.2, phase 16 slice 2).
 *
 * ADMIN-gated, because the query behind it is: the figures describe how the machine is
 * doing — how much of the catalogue has sources, how much the health sweep has looked at,
 * what is waiting in its queues — which is an operator's view rather than something
 * addressed to the public. What IS addressed to the public is the moderation accounting
 * at /transparency (§16.1), which is a different page with a different audience.
 *
 * THE ERROR NOTICE DISTINGUISHES TWO THINGS, and that is why it is a function rather than
 * a string. A member without the role gets a refusal from the server, and telling them
 * "something went wrong on the server" would send them looking for a fault that does not
 * exist — so the refusal is named as a refusal. Anything else really is a fault, and the
 * page says so rather than guessing.
 */

export const describeMetricsError = (
  message: string,
): { variant: "warning" | "danger"; text: string } => {
  if (/not authorized/i.test(message)) {
    return {
      variant: "warning",
      text: "These figures describe this instance's own operation, and reading them needs the ADMIN role. Ask an administrator if you need them — nothing is broken.",
    };
  }

  return {
    variant: "danger",
    text: `The instance's counters could not be read. This is a fault rather than a permission: the server said ${message}`,
  };
};

/**
 * The page's heading, and the reason it is an h1.
 *
 * `Title` sets the DOCUMENT title through Helmet and renders no heading at all — measured, by an
 * end-to-end test whose first assertion was `getByRole("heading", { name: "Instance metrics" })` and
 * which found nothing because this page (like its neighbours) rendered no heading of its own. A page a
 * screen reader arrives at with no heading is a page with no name, so this renders one; h1 rather than
 * the h3 its neighbours use, because a page's own heading is the h1 and `check-page-headings` (a sibling
 * project's gate) flags a page that opens at h2 or lower for exactly this reason.
 */
const MetricsHeading: FC = () => (
  <>
    <Title page="Instance metrics" />
    <h1>Instance metrics</h1>
  </>
);

const MetricsPage: FC = () => {
  const { data, loading, error } = useInstanceMetricsQuery();

  if (loading) {
    return (
      <>
        <MetricsHeading />
        <Spinner animation="border" role="status" />
      </>
    );
  }

  if (error || !data) {
    const notice = describeMetricsError(error?.message ?? "no response");
    return (
      <>
        <MetricsHeading />
        <Alert variant={notice.variant}>{notice.text}</Alert>
      </>
    );
  }

  const { window_days: windowDays, since, metrics } = data.instanceMetrics;

  return (
    <>
      <MetricsHeading />
      <p>
        What this instance can count about itself, each figure with the sentence
        that decides what it counts. Every figure is derived from the rows the
        features already write rather than incremented as things happen, so a
        figure cannot be wrong about the record without the record being wrong.
      </p>
      <MetricsList metrics={metrics} windowDays={windowDays} since={since} />
    </>
  );
};

export default MetricsPage;
