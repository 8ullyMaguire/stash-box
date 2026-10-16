// SPEC 7.24.10 -- the review queue's aging view.
//
// Two claims, both about the PAGE rather than about a query:
//
//   1. a pending edit shows how long it has been waiting (fragment:renderWaiting)
//   2. the queue OPENS on the oldest end of it (pages/edits/Edits.tsx passes
//      defaultDirection: ASC), which no unit test covers because it lives in the
//      page's props and the hook's parameter defaults
//
// Ordering is checked by reading the RENDERED waiting values, not by asking the
// API what it would have returned: a spec that calls the same query the page calls
// proves the API sorts, and the bug this guards against is the page not asking it
// to. The scale is explicit (days > hours > minutes) because "5 hours" and "2 days"
// are not comparable as numbers.

import { expect, test } from "../support/fixtures";
import { uniq } from "../support/helpers/seed";
import { submitStudioCreateEdit } from "../support/helpers/seed";
import { graphqlAs } from "../support/helpers/graphql";

const UNIT_MINUTES: Record<string, number> = {
  second: 1 / 60,
  minute: 1,
  hour: 60,
  day: 60 * 24,
};

/** "3 days" | "5 hours" | "20 minutes" -> minutes, or null if it is not that shape. */
const toMinutes = (text: string): number | null => {
  const m = /^(\d+)\s+(second|minute|hour|day)s?$/.exec(text.trim());
  if (!m) return null;
  return Number(m[1]) * UNIT_MINUTES[m[2]];
};

test("a pending edit in the queue says how long it has been waiting", async ({
  editPage,
}) => {
  // Seed our own pending edit so the assertion does not depend on what other specs
  // left behind: an empty queue would make "waiting is visible" fail for a reason
  // that is about fixtures.
  const editor = await graphqlAs("e2e_edit");
  await submitStudioCreateEdit(editor, uniq("Aging"));
  await editor.dispose();

  await editPage.goto("/edits");

  const waiting = editPage.getByTestId("edit-waiting");
  // Count the subject first: "the first one is visible" is vacuous when there are
  // none, and "none rendered" is exactly the defect this test exists to catch.
  await expect(waiting.first()).toBeVisible();
  expect(await waiting.count()).toBeGreaterThan(0);

  const first = (await waiting.first().innerText()).replace(/^Waiting:\s*/, "");
  expect(toMinutes(first), `waiting value not a duration: ${first}`).not.toBeNull();
});

test("the queue opens on the oldest end, not the newest", async ({ editPage }) => {
  const editor = await graphqlAs("e2e_edit");
  await submitStudioCreateEdit(editor, uniq("AgingOrderA"));
  await submitStudioCreateEdit(editor, uniq("AgingOrderB"));
  await editor.dispose();

  await editPage.goto("/edits");

  const values = await editPage
    .getByTestId("edit-waiting")
    .allInnerTexts();
  const minutes = values
    .slice(0, 5)
    .map((v) => toMinutes(v.replace(/^Waiting:\s*/, "")))
    .filter((n): n is number => n !== null);

  // A page of one card has no order to check, and PASSING would be a statement
  // about the fixtures rather than about the sort.
  expect(minutes.length).toBeGreaterThan(1);

  // Non-increasing waiting time = oldest first. DESC (the API's own default, and
  // what every other list on this instance uses) fails on the first pair.
  for (let i = 1; i < minutes.length; i += 1) {
    expect(
      minutes[i],
      `card ${i} waited longer than card ${i - 1}: ${minutes.join(", ")} minutes`,
    ).toBeLessThanOrEqual(minutes[i - 1]);
  }
});
