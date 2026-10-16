// SPEC §7.24.5's demand board, at its own URL.
//
// Why a spec for a page whose query is already integration-tested: this file's
// sibling note in pages/curation/index.tsx records that BOTH nested curation routes
// were once declared with the ABSOLUTE route constant, so neither matched and every
// /curation/<anything> URL fell through to the `/*` child and rendered the
// dashboard. `/curation/matchup` had never once rendered the matchup UI and nothing
// caught it, because no test visited it.
//
// So this test asserts the page is the page: the board's own heading is there, and
// the fallback dashboard's heading is NOT. An empty board and a full one both pass --
// what is under test is which component rendered, not what data it found.

import { expect, test } from "../support/fixtures";

test("the demand board renders at its route rather than the curation fallback", async ({
  adminPage,
}) => {
  await adminPage.goto("/curation/fingerprint-demand");

  await expect(
    adminPage.getByRole("heading", { name: /Unmatched-fingerprint demand/i }),
  ).toBeVisible();

  // The absence, with its subject present: the dashboard DID render somewhere before
  // this assertion ran, so "the dashboard is not here" is about the route and not
  // about a page that failed to load at all.
  await expect(
    adminPage.getByRole("heading", { name: /What the archive is getting wrong/i }),
  ).toHaveCount(0);

  // The board's own rule, which holds whether or not it found anything: the figure is
  // named as a lower bound in the copy, not only in the column header.
  await expect(adminPage.getByText(/lower bound/i).first()).toBeVisible();
});
