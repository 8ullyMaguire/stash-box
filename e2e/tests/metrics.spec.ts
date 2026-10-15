// Tier 4 -- the instance's own counters (spec 16.2, phase 16 slice 2).
//
// The Go integration tests recompute every figure against a database they migrate themselves, which is where
// a wrong number is caught. What only a BROWSER can prove is the two things this spec is about:
//
//   1. THE PAGE RENDERS WHAT THE SERVER SENT, definition and all. The assertion is against a definition
//      STRING WRITTEN BY THE SERVER (`internal/service/metrics/metrics.go`), not against a label invented
//      here -- so a page that rendered its own wording, or a definition that went missing in the trip, fails
//      rather than passes.
//   2. THE REFUSAL IS NAMED AS A REFUSAL. A member without ADMIN gets a sentence telling them it is a role
//      rather than a fault, which is the difference between "ask an administrator" and "something is broken
//      and you should try again". And the figures are asserted to be ABSENT, with the count taken first, so
//      a page that rendered both the notice and the table cannot pass on the notice alone.

import { expect, test } from "../support/fixtures";
import { graphqlAs } from "../support/helpers/graphql";

const QUERY = `query { instanceMetrics { window_days metrics { key definition value } } }`;

test("an admin reads the counters over GraphQL and a read-only member is refused", async () => {
  const admin = await graphqlAs("e2e_admin");
  const adminRes = await admin.post("/graphql", {
    data: { query: QUERY },
    headers: { "content-type": "application/json" },
  });
  const adminBody = (await adminRes.json()) as {
    data?: { instanceMetrics?: { window_days: number; metrics: unknown[] } };
    errors?: unknown[];
  };
  expect(adminBody.errors ?? []).toEqual([]);
  expect(adminBody.data?.instanceMetrics?.window_days).toBe(30);
  expect((adminBody.data?.instanceMetrics?.metrics ?? []).length).toBeGreaterThan(0);
  await admin.dispose();

  const read = await graphqlAs("e2e_read");
  const readRes = await read.post("/graphql", {
    data: { query: QUERY },
    headers: { "content-type": "application/json" },
  });
  if (readRes.status() === 200) {
    const body = (await readRes.json()) as { errors?: { message: string }[] };
    expect(body.errors?.[0]?.message ?? "").toMatch(/not authorized/i);
  } else {
    expect(readRes.status()).toBeGreaterThanOrEqual(400);
  }
  await read.dispose();
});

test("the metrics page shows the figures with the definitions the server sent", async ({
  adminPage,
}) => {
  await adminPage.goto("/metrics");
  await expect(
    adminPage.getByRole("heading", { name: "Instance metrics" }),
  ).toBeVisible({ timeout: 10_000 });

  // The figure an operator opens this page for, and its server-written definition.
  await expect(adminPage.getByText("scenes_total", { exact: false }).first()).toBeVisible();
  await expect(
    adminPage.getByText("Catalogue scenes not marked deleted.").first(),
  ).toBeVisible();

  // The window is named with its width and its start, because "in the window" means nothing to a reader who
  // does not know where it begins.
  await expect(
    adminPage.getByText(/covers the 30\s+days from/).first(),
  ).toBeVisible();
});

test("a member without the role is told it is a role and not a fault", async ({
  readPage,
}) => {
  await readPage.goto("/metrics");
  await expect(
    readPage.getByRole("heading", { name: "Instance metrics" }),
  ).toBeVisible({ timeout: 10_000 });
  await expect(readPage.getByText(/needs the ADMIN role/).first()).toBeVisible();

  // The absence, with its subject counted first: "the figures are not shown" is vacuously true of a page
  // that failed to render at all, so the heading above is asserted before this.
  await expect(
    readPage.getByText("Catalogue scenes not marked deleted."),
  ).toHaveCount(0);
});

test("the nav entry goes to an admin and not to a read-only member", async ({
  adminPage,
  readPage,
}) => {
  const link = (page: typeof adminPage) =>
    page.getByRole("link", { name: "Metrics", exact: true });

  await adminPage.goto("/");
  await expect(link(adminPage)).toHaveCount(1);
  await expect(link(adminPage)).toBeVisible();

  await readPage.goto("/");
  await expect(link(readPage)).toHaveCount(0);
});
