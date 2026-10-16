// Tier 4 — the data-lint quest board (SPEC §7.24.4, plan phase 3a).
//
// The board is the only front end of a promise: a detector emits a quest and
// NEVER auto-merges, because the merge path is the consensus flow and an
// unattributable merge is worse than the duplicate it would have fixed. So the
// journey asserted here is not just "the page loads with data on it" -- it is
// that the page offers no way to act beyond reading, and that the role gate the
// schema declares is the gate the UI actually applies.

import type { APIRequestContext } from "@playwright/test";
import { expect, test } from "../support/fixtures";
import { adminApi, createSite, gql, uniq } from "../support/helpers/seed";

// A performer created WITH a URL, which is the finding's whole subject: two
// records claiming one address. `createPerformer` in the shared helpers takes a
// name only, so this is the same mutation with the urls argument added rather
// than a second way to create performers.
async function createPerformerWithUrl(
  api: APIRequestContext,
  name: string,
  url: string,
  siteId: string,
): Promise<{ id: string; name: string }> {
  const data = await gql<{ performerCreate: { id: string; name: string } }>(
    api,
    `mutation($input: PerformerCreateInput!) {
       performerCreate(input: $input) { id name }
     }`,
    { input: { name, gender: "FEMALE", urls: [{ url, site_id: siteId }] } },
  );
  return data.performerCreate;
}

test("the lint board shows the duplicate-URL detector, links its findings to records, and offers no way to apply them", async ({
  adminPage,
}) => {
  const api = await adminApi();
  const site = await createSite(api);

  // Unique per run: the e2e database accumulates every previous run's fixtures.
  const sharedUrl = `https://lint-e2e.test/${uniq("url")}`;
  const first = await createPerformerWithUrl(
    api,
    uniq("Lint Board One"),
    sharedUrl,
    site.id,
  );
  const second = await createPerformerWithUrl(
    api,
    uniq("Lint Board Two"),
    sharedUrl,
    site.id,
  );

  // THE PAIR IS ASSERTED OVER THE API, NOT THE PAGE, and the limit is why. The
  // board is a work list that stops at 25 findings per detector by default and
  // SAYS SO when it does; this database carries every run this host has ever done,
  // so a pair created seconds ago is not necessarily in the first 25. Asserting
  // "my two records are on the page" would be a test of the seeding order rather
  // than of the board -- it passes on an empty database and fails on a real one.
  // The page contract is asserted below, on the page.
  const board = await gql<{
    lintQuestBoard: {
      slug: string;
      count: number;
      candidates: { entityId: string; entityName: string; detail: string }[];
    }[];
  }>(
    api,
    `query($limit: Int) {
       lintQuestBoard(limit: $limit) {
         slug count
         candidates { entityId entityName detail }
       }
     }`,
    { limit: 500 },
  );
  const dup = board.lintQuestBoard.find(
    (r) => r.slug === "duplicate_performer_url",
  );
  expect(dup).toBeDefined();
  const found = dup?.candidates.filter((c) =>
    [first.id, second.id].includes(c.entityId),
  );
  expect(found?.map((c) => c.entityName).sort()).toEqual(
    [first.name, second.name].sort(),
  );
  // And the detector's own sentence quotes the shared address rather than
  // describing the class of problem.
  expect(found?.[0].detail).toContain(sharedUrl);
  await api.dispose();

  await adminPage.goto("/curation/lint");

  // The detector is on the board BY NAME -- a board that quietly omitted it would
  // look exactly like a detector that found nothing, and a reader cannot tell the
  // difference from the page alone.
  await expect(
    adminPage.getByRole("heading", {
      name: /Two performers record the same URL/,
    }),
  ).toBeVisible({ timeout: 15_000 });

  // Findings are LINKS to the records they are about: the curator's next click is
  // the ordinary edit on the record's own page, with its ordinary votes.
  const candidateLinks = adminPage.locator(
    'a[href^="/performers/"], a[href^="/scenes/"]',
  );
  await expect(candidateLinks.first()).toBeVisible();
  expect(await candidateLinks.count()).toBeGreaterThan(0);

  // SUBJECT FIRST. "No way to apply it" is a statement about a page that really
  // did render findings, so the subject is counted before the absence is asserted
  // -- an absence assertion on an empty page proves nothing.
  await expect(
    adminPage.getByRole("button", { name: /merge|apply|resolve|fix/i }),
  ).toHaveCount(0);
});

test("the lint board is behind VOTE, so a read-only user is not served it", async ({
  adminPage,
  readPage,
}) => {
  // The positive half first, in the same run: an assertion that the read user does
  // not see the board is only evidence if the board exists for somebody.
  await adminPage.goto("/curation/lint");
  await expect(adminPage.getByText("What needs attention")).toBeVisible({
    timeout: 15_000,
  });

  await readPage.goto("/curation/lint");
  await expect(readPage.getByText("What needs attention")).toHaveCount(0);
});
