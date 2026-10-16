// Tier 4 -- the accessibility target, measured by the tool that measured it (spec §16.3, plan phase 16
// slice 4).
//
// THE PLAN'S VERIFICATION SENTENCE for phase 16 is: "keyboard-only navigation reaches every control on each
// key page; the a11y target is recorded with the tool that measured it." This file is both halves of that
// sentence, and it is a SPEC rather than a checklist because a checklist cannot fail.
//
// WHY axe-core AND NOT AN OPINION. "WCAG 2.1 AA" is a standard with 50-odd success criteria, and a
// hand-written assertion would cover the handful somebody thought of. axe-core is the implementation of the
// standard that the browsers' own devtools ship; its rule ids are the vocabulary a reader can check, so a
// failure here names `image-alt` or `color-contrast` rather than "accessibility problem".
//
// WHAT THIS GATE DOES NOT DO, said out loud because a green a11y gate is easy to over-read: axe detects only
// the subset of WCAG that a machine can decide from the DOM. It cannot tell whether an alt text MEANS
// anything, whether a tab order is sensible, or whether a live region announced the right thing. So the two
// checks here are the two that a machine CAN decide -- and the keyboard check is a second, independent
// instrument for the half axe does not cover.
//
// THE PAGES ARE THE FIVE THE PHASE NAMES (home dashboard, search results, content detail, tag page,
// performer page) plus /metrics, which this phase added. Each is visited with data in it: an empty search
// page and a populated one have different DOM, and the populated one is what a reader gets.

import AxeBuilder from "@axe-core/playwright";
import { mkdirSync, writeFileSync } from "node:fs";
import type { Page } from "@playwright/test";
import path from "node:path";

import { expect, test } from "../support/fixtures";
import {
  adminApi,
  createPerformer,
  createScene,
  createStudio,
  createTag,
  randomHex,
} from "../support/helpers/seed";

// WCAG 2.1 level AA is the phase's target: 2.0 A + AA, plus the 2.1 additions.
const WCAG_21_AA = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"];

interface Seeded {
  tag: { id: string; name: string };
  performer: { id: string; name: string };
  scene: { id: string; title: string | null };
}

let seeded: Seeded;

test.beforeAll(async () => {
  const api = await adminApi();
  // Names carry random hex rather than a timestamp: Playwright runs one worker per CPU (six here) and each
  // worker runs this hook, so two workers seeding in the same millisecond collide on the unique index and
  // the whole spec dies in setup -- a flake that looks like a broken page.
  const token = randomHex(8);
  const studio = await createStudio(api, { name: `a11y studio ${token}` });
  seeded = {
    tag: await createTag(api, { name: `a11y tag ${token}` }),
    performer: await createPerformer(api, { name: `a11y performer ${token}` }),
    scene: await createScene(api, { title: `a11y scene ${token}`, studioId: studio.id }),
  };
  await api.dispose();
});

/**
 * The pages the phase names, each with the data that makes it a page rather than a shell.
 *
 * The path is a FUNCTION because Playwright collects test titles before `beforeAll` has seeded anything: a
 * string built at module scope would be built from an undefined scene id, which is a collection-time crash
 * rather than a failing assertion.
 */
const KEY_PAGES: Array<{ name: string; path: () => string }> = [
  { name: "home dashboard", path: () => "/" },
  { name: "search results", path: () => `/search?q=${encodeURIComponent("a11y")}` },
  { name: "content detail", path: () => `/scenes/${seeded.scene.id}` },
  { name: "tag", path: () => `/tags/${seeded.tag.id}` },
  { name: "performer", path: () => `/performers/${seeded.performer.id}` },
  { name: "instance metrics", path: () => "/metrics" },
];

/**
 * The violation list, in the terms a reader can act on.
 *
 * The rule id first, because that is the standard's vocabulary and the string somebody will search for; then
 * the impact, then up to three targets, because one rule can fire on forty nodes and the first three say
 * which pattern is wrong.
 */
function describe(violations: Awaited<ReturnType<AxeBuilder["analyze"]>>["violations"]) {
  return violations
    .map((v) => {
      const nodes = v.nodes
        .slice(0, 3)
        .map((n) => `      ${n.target.join(" ")}\n        ${n.html.slice(0, 200)}`)
        .join("\n");
      return `  ${v.id} [${v.impact}] x${v.nodes.length}: ${v.help}\n${nodes}`;
    })
    .join("\n");
}

/**
 * The report is written per page, by the worker that audited it, into its own file.
 *
 * It was first collected on `globalThis` and flushed once in `afterAll` -- which silently produced `{}`,
 * because Playwright runs six workers and each has its OWN global: the worker that finished last wrote its
 * own (often empty) object over everybody else's. One file per page cannot lose another worker's work, and
 * the audit's own value -- "the tool that measured it" -- is exactly the thing that must not be lost.
 */
async function audit(page: Page, pagePath: string, label: string) {
  await page.goto(pagePath);
  // The SPA answers before it has rendered, so wait for the shell's content rather than for a load event.
  await page.locator("main").first().waitFor({ state: "visible", timeout: 15_000 });
  await page.waitForLoadState("domcontentloaded");

  const results = await new AxeBuilder({ page }).withTags(WCAG_21_AA).analyze();
  const entry = {
    // The counts are recorded even when everything passes: "0 violations" from a run that analysed 0 nodes
    // is the same string as "0 violations" from a run that analysed 300.
    passes: results.passes.length,
    incomplete: results.incomplete.length,
    violations: results.violations.map((v) => ({
      id: v.id,
      impact: v.impact,
      help: v.help,
      nodes: v.nodes.length,
      targets: v.nodes.slice(0, 5).map((n) => n.target.join(" ")),
      // The element's own markup, so a finding can be traced to a component from the report alone.
      html: v.nodes.slice(0, 5).map((n) => n.html.slice(0, 200)),
      // The contrast nodes carry the measured colours and ratio; without them a `color-contrast` finding
      // cannot be fixed from anything but a guess.
      detail: v.nodes.slice(0, 5).map((n) => n.any[0]?.data),
    })),
  };

  if (process.env.A11Y_REPORT === "1") {
    const dir = path.resolve(__dirname, "..", "a11y-report");
    mkdirSync(dir, { recursive: true });
    const slug = label.replace(/[^a-z0-9]+/gi, "-").toLowerCase();
    writeFileSync(path.join(dir, `${slug}.json`), `${JSON.stringify({ path: pagePath, ...entry }, null, 2)}\n`);
  }

  return results.violations;
}

/**
 * Every control a keyboard user can reach, by Tab, from the top of the page.
 *
 * `focusables` is what the DOM SAYS is focusable; `reached` is what the keyboard actually reaches. The
 * difference between the two lists is the finding, and it is the one axe cannot give: an element that is
 * focusable in the DOM but never receives focus is invisible to a keyboard user, and nothing in the markup
 * looks wrong.
 */
async function focusables(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    // A disabled control is not focusable by the BROWSER, so it is not a control a keyboard user was
    // promised; a control that is focusable and never receives focus is. Excluding `:disabled` keeps the
    // check about reachability rather than about how many buttons happen to be disabled on a page.
    const selector =
      'a[href], button:not([disabled]), input:not([type="hidden"]):not([disabled]), ' +
      'select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';
    const describe = (el: Element) => {
      const text = (el.getAttribute("aria-label") || el.textContent || "").trim().slice(0, 40);
      const cls = typeof el.className === "string" && el.className ? `.${el.className.split(" ")[0]}` : "";
      return `${el.tagName.toLowerCase()}[${el.getAttribute("type") || ""}]${cls}#${el.id || "-"}:${text}`;
    };
    return Array.from(document.querySelectorAll(selector))
      .filter((el) => {
        const rect = el.getBoundingClientRect();
        const style = getComputedStyle(el);
        return (
          rect.width > 0 &&
          rect.height > 0 &&
          style.visibility !== "hidden" &&
          style.display !== "none" &&
          // A control inside an aria-hidden subtree is one the browser reports as focusable and a screen
          // reader is told not to mention: not what this check is about.
          !el.closest('[aria-hidden="true"]') &&
          // Tabs are excluded HERE and checked separately, because a tablist is a composite widget: the WAI-ARIA
          // pattern puts ONE tab in the tab order and expects the arrow keys to move between them. The
          // exclusion is not an assumption -- `every tab ... arrow keys` presses them and requires each tab to
          // receive focus, so the widget is verified, just not by Tab.
          !el.closest('[role="tablist"]')
        );
      })
      .map(describe);
  });
}

// Both palettes are audited, and that is not thoroughness for its own sake: the theme is a token layer over
// 33 stylesheets that predate it, so a palette can be consistent in one mode and broken in the other. The
// first run of this gate measured exactly that -- `.text-muted` compiled to the DARK palette's grey in both
// modes (1.53:1 on the light page) and `.card` hard-coded the dark surface while its text followed the theme
// (1.54:1). Neither was visible from the dark palette alone, which is what a developer looks at.
const PALETTES = ["light", "dark"] as const;

for (const target of KEY_PAGES) {
  for (const palette of PALETTES) {
    test(`the ${target.name} page has no WCAG 2.1 AA violations in the ${palette} palette`, async ({
      adminPage,
    }) => {
      // The app resolves "system" against `prefers-color-scheme`, so emulating the media query is how a real
      // user in that mode is reproduced.
      await adminPage.emulateMedia({ colorScheme: palette });
      const violations = await audit(adminPage, target.path(), `${target.name}-${palette}`);

      expect(
        violations,
        `axe (tags: ${WCAG_21_AA.join(", ")}) found ${violations.length} violation(s) on ${target.path()} ` +
          `in the ${palette} palette:\n${describe(violations)}`,
      ).toEqual([]);
    });
  }

  // One keyboard walk per page, not per palette: which controls are reachable does not depend on colour.
  test(`every control on the ${target.name} page is reachable by Tab`, async ({ adminPage }) => {
    const pagePath = target.path();
    await adminPage.goto(pagePath);
    await adminPage.locator("main").first().waitFor({ state: "visible", timeout: 15_000 });

    const expected = await focusables(adminPage);
    // The subject is counted BEFORE its reachability is asserted: "every control is reachable" is vacuously
    // true of a page that rendered no controls, which is exactly what a broken selector produces.
    expect(expected.length, `${pagePath} rendered no focusable controls at all`).toBeGreaterThan(0);

    const reached = new Set<string>();
    await adminPage.evaluate(() => document.body.focus());
    for (let i = 0; i < Math.min(expected.length * 3, 400); i += 1) {
      await adminPage.keyboard.press("Tab");
      const described = await adminPage.evaluate(() => {
        const el = document.activeElement;
        if (!el || el === document.body) return "";
        const rect = el.getBoundingClientRect();
        if (rect.width === 0 || rect.height === 0) return "";
        const text = (el.getAttribute("aria-label") || el.textContent || "").trim().slice(0, 40);
        const cls =
          typeof el.className === "string" && el.className ? `.${el.className.split(" ")[0]}` : "";
        return `${el.tagName.toLowerCase()}[${el.getAttribute("type") || ""}]${cls}#${el.id || "-"}:${text}`;
      });
      if (described) reached.add(described);
      // Stop once the tab order has wrapped around to something already visited and every expectation has
      // been met -- otherwise this loops for the full count on a page that is already complete.
      if (expected.every((name) => reached.has(name))) break;
    }

    const unreachable = expected.filter((name) => !reached.has(name));
    expect(
      unreachable,
      `${unreachable.length} control(s) on ${pagePath} are focusable in the DOM but never receive focus ` +
        `from Tab (the keyboard user cannot use them):\n    ${unreachable.join("\n    ")}`,
    ).toEqual([]);
  });

  // The other half of the keyboard contract. Tab does not reach a tablist's inactive tabs BY DESIGN, so they
  // are excluded above -- and an exclusion without its own test is where a real defect hides. This presses
  // ArrowRight through every tablist on the page and requires each tab to receive focus, which is also the
  // only place the app's tabs are checked as the composite widget the ARIA pattern says they are.
  test(`every tab on the ${target.name} page is reachable with the arrow keys`, async ({ adminPage }) => {
    const pagePath = target.path();
    await adminPage.goto(pagePath);
    await adminPage.locator("main").first().waitFor({ state: "visible", timeout: 15_000 });

    const tablists = await adminPage.evaluate(() =>
      Array.from(document.querySelectorAll('[role="tablist"]')).map((list) => ({
        name: list.getAttribute("aria-label") || list.id || "(unnamed tablist)",
        tabs: Array.from(list.querySelectorAll('[role="tab"]')).map((tab) =>
          (tab.textContent || "").trim(),
        ),
      })),
    );

    // No tablists is a legitimate answer for some pages, and it is stated rather than left implicit.
    if (tablists.length === 0) {
      expect(tablists, `${pagePath} has no tablist, so nothing to walk`).toEqual([]);
      return;
    }

    for (const [index, list] of tablists.entries()) {
      expect(
        list.tabs.length,
        `${pagePath}: tablist "${list.name}" renders no tabs, so reachability here is vacuous`,
      ).toBeGreaterThan(0);

      await adminPage.evaluate((i) => {
        const target = document.querySelectorAll('[role="tablist"]')[i];
        const first = target?.querySelector('[role="tab"]') as HTMLElement | undefined;
        first?.focus();
      }, index);

      const focused: string[] = [];
      for (let step = 0; step < list.tabs.length; step += 1) {
        const text = await adminPage.evaluate(() =>
          (document.activeElement?.textContent || "").trim(),
        );
        if (text) focused.push(text);
        await adminPage.keyboard.press("ArrowRight");
      }

      expect(
        focused,
        `${pagePath}: tablist "${list.name}" -- the arrow keys reached ${focused.length} of ` +
          `${list.tabs.length} tabs (${list.tabs.join(", ")})`,
      ).toEqual(list.tabs);
    }
  });
}


// One test, not one per page: reduced motion is a property of the stylesheet, and the page that renders the
// control is enough to measure it.
test("the instance honours a reader's reduced-motion setting", async ({ adminPage }) => {
  // Measured twice, and the pair is the evidence. A test that only checked the reduced case would pass on a
  // stylesheet where the property is zero for everybody -- "0 seconds" and "no transition at all" are the same
  // string. So the normal case must be measurably non-zero first.
  const read = async (motion: "no-preference" | "reduce") => {
    await adminPage.emulateMedia({ reducedMotion: motion });
    await adminPage.goto("/");
    await adminPage.locator("main").first().waitFor({ state: "visible", timeout: 15_000 });
    return adminPage.evaluate(() => {
      // `.nav-link` is the control bootstrap gives a colour transition to, so it is the one that can show the
      // difference; `.card` has no transition and would report zero in both cases.
      const el = document.querySelector(".nav-link") as HTMLElement | null;
      if (!el) return null;
      const seconds = getComputedStyle(el)
        .transitionDuration.split(",")
        .map((part) => parseFloat(part));
      return { max: Math.max(...seconds), raw: getComputedStyle(el).transitionDuration };
    });
  };

  const normal = await read("no-preference");
  const reduced = await read("reduce");

  expect(normal, "no `.nav-link` on the dashboard, so nothing was measured").not.toBeNull();
  expect(reduced, "no `.nav-link` on the dashboard, so nothing was measured").not.toBeNull();
  expect(
    normal?.max,
    `the normal case has to have a transition to reduce; measured ${normal?.raw}`,
  ).toBeGreaterThan(0.1);
  expect(
    reduced?.max,
    `with prefers-reduced-motion: reduce the transition must be near-zero; measured ${reduced?.raw}`,
  ).toBeLessThanOrEqual(0.001);
});
