// Tier 4 — a record's own findings, and the action a finding leads to (SPEC §7.24.4, plan phase 3a).
//
// The board answers "what needs attention on this instance"; this panel answers "what is wrong with
// the record I am looking at", from the same validation pass. The part worth a live journey is not
// that the panel renders -- it is that the panel's action leads somewhere that WORKS: a merge page
// arrived at from a finding has to carry the other half of the finding with it, or the curator lands
// on a form that knows nothing about what they were looking at.

import type { APIRequestContext } from "@playwright/test";
import { expect, test } from "../support/fixtures";
import {
  adminApi,
  createPerformer,
  createSite,
  gql,
  uniq,
} from "../support/helpers/seed";

// The pair, created once per test: the e2e database accumulates every previous run's fixtures, so
// names and URLs are unique and the assertions are about THIS pair rather than about the first page
// of a growing work list.
async function duplicatePair(api: APIRequestContext) {
  const site = await createSite(api);
  const sharedUrl = `https://record-findings.test/${uniq("url")}`;
  const first = await createPerformer(api, {
    name: uniq("Findings One"),
    urls: [{ url: sharedUrl, site_id: site.id }],
  });
  const second = await createPerformer(api, {
    name: uniq("Findings Two"),
    urls: [{ url: sharedUrl, site_id: site.id }],
  });
  return { first, second };
}

test("a flagged record says what is wrong and hands the curator the merge it needs", async ({
  adminPage,
}) => {
  const api = await adminApi();
  const { first, second } = await duplicatePair(api);

  // The pair is confirmed over the API first: the panel is downstream of the detector, and a journey
  // that asserts the panel without the finding existing would be testing the fixture.
  const apiFindings = await gql<{
    lintFindingsFor: {
      slug: string;
      candidates: { entityId: string; entityName: string }[];
    }[];
  }>(
    api,
    `query($entityType: String!, $entityId: ID!) {
       lintFindingsFor(entityType: $entityType, entityId: $entityId) {
         slug
         candidates { entityId entityName }
       }
     }`,
    { entityType: "performer", entityId: first.id },
  );
  const dup = apiFindings.lintFindingsFor.find(
    (r) => r.slug === "duplicate_performer_url",
  );
  expect(dup).toBeDefined();
  expect(dup?.candidates.map((c) => c.entityId).sort()).toEqual(
    [first.id, second.id].sort(),
  );
  await api.dispose();

  await adminPage.goto(`/performers/${first.id}`);

  // SUBJECT FIRST: the panel is asserted before anything is said about what it does not have.
  await expect(adminPage.getByText(/flags this record/)).toBeVisible({
    timeout: 15_000,
  });
  // The finding names the OTHER record, which is the whole point: a panel that said "this record is
  // a duplicate" without saying of what would leave the curator with nowhere to go. `.first()`
  // because the name legitimately appears twice -- once as the candidate row and once inside the
  // merge link below -- and a strict locator would fail on the panel being MORE useful than expected.
  await expect(adminPage.getByText(second.name).first()).toBeVisible();
  // It quotes the shared address, from the detector, rather than the panel inventing a sentence.
  await expect(adminPage.getByText(/record-findings\.test/).first()).toBeVisible();
  // Reading only (SPEC §7.24.4): the panel points at the ordinary flows and holds no control of its
  // own. Asserted after the subject above, so this is a statement about a panel that really rendered.
  await expect(adminPage.getByRole("button", { name: /apply|resolve|dismiss/i })).toHaveCount(0);

  // THE ACTION. Lint never merges (SPEC §7.24.4) -- it points at the ordinary consensus flow, and
  // this click is the claim that the flow arrives preloaded with both halves of the finding.
  await adminPage.getByRole("link", { name: new RegExp(`Merge ${second.name}`) }).click();
  await expect(adminPage).toHaveURL(
    new RegExp(`/performers/${first.id}/merge\\?source=${second.id}$`),
  );

  // The source is NAMED on the merge page, which is what proves it was hydrated rather than seeded
  // as a bare id: a select holding an id it cannot name draws an empty row.
  // THE SELECT ITSELF CARRIES THE SOURCE, not just the page state behind it: the two are different
  // claims and only the first is one a curator can see. Asserted on the field's own content rather
  // than on a visible <span>, because the tag's text node sits inside a control that Playwright
  // reports as hidden while the value IS the field's value -- measured: the plain text locator found
  // the span and called it hidden, which is a fact about react-select's markup rather than about the
  // prefill. The visible proof follows immediately: Continue only exists once a source is selected.
  await expect(adminPage.locator(".PerformerSelect-list").first()).toContainText(
    second.name,
  );
  // And the form is armed: Continue only exists once a source is selected, so its presence is the
  // claim that the prefilled source became the real selection rather than a decoration.
  await adminPage.getByRole("button", { name: "Continue" }).click();

  // The merge form itself, with both halves: this record as the target and the finding's other half
  // as the source. Nothing is submitted -- the journey ends at the door of the ordinary flow.
  await expect(adminPage.getByText("Merge Target")).toBeVisible();
  // BOTH HALVES OF THE FINDING ARE ON THE FORM -- the target it was opened from and the source the
  // finding named. Asserted on the form's content rather than on a card's markup: a merge form that
  // lost the finding's other half would show the target alone, and that is the difference between a
  // candidate action and a link that merely looks like one.
  await expect(adminPage.locator(".PerformerMerge").first()).toContainText(first.name);
  await expect(adminPage.locator(".PerformerMerge").first()).toContainText(second.name);
});

test("a clean record says nothing, and neither does a record shown to a reader", async ({
  adminPage,
  readPage,
}) => {
  const api = await adminApi();
  const { second } = await duplicatePair(api);
  const clean = await createPerformer(api, { name: uniq("Findings Clean") });
  await api.dispose();

  // THE FLAGGED RECORD FIRST, IN THIS RUN. "This record shows no panel" is worth nothing unless the
  // panel exists for somebody, and the flagged record below is the control for the clean one: same
  // page, same role, one of them flagged.
  await adminPage.goto(`/performers/${second.id}`);
  await expect(adminPage.getByText(/flags this record/)).toBeVisible({
    timeout: 15_000,
  });

  await adminPage.goto(`/performers/${clean.id}`);
  // The record itself rendered -- the absence is about the panel, not about the page.
  await expect(
    adminPage.getByRole("heading", { name: clean.name }),
  ).toBeVisible({ timeout: 15_000 });
  await expect(adminPage.getByText(/flags this record/)).toHaveCount(0);
  await expect(adminPage.getByText(/detectors flag/)).toHaveCount(0);

  // A READER IS NOT SERVED THE FINDINGS AT ALL: `lintFindingsFor` is gated VOTE at the schema, so the
  // panel is not theirs to see -- and it must not render a quiet "nothing is wrong here" to somebody
  // who was never asked. The control again is the curator above, on the same record.
  await readPage.goto(`/performers/${second.id}`);
  await expect(
    readPage.getByRole("heading", { name: second.name }),
  ).toBeVisible({ timeout: 15_000 });
  await expect(readPage.getByText(/flags this record/)).toHaveCount(0);
  await expect(readPage.getByText(/could not be run/)).toHaveCount(0);
});
