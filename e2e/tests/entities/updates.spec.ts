// Tier 3 — entity update + delete via the edit lifecycle.
// Studio is the simplest entity to drive end-to-end; performer/tag follow the
// same shape with slightly different forms.

import { test, expect } from "../../support/fixtures";
import {
  adminApi,
  createPerformer,
  createStudio,
  createTag,
  createTagCategory,
  gql,
  uniq,
} from "../../support/helpers/seed";
import { approveEdit } from "../../support/helpers/workflow";

test("studio rename: submit MODIFY edit, approve, name changes", async ({
  editPage,
  moderatePage,
}) => {
  const admin = await adminApi();
  const original = await createStudio(admin);
  await admin.dispose();

  const renamed = uniq("StudioRenamed");

  await editPage.goto(`/studios/${original.id}/edit`);
  // Form is pre-filled — clear and overwrite the name.
  const nameInput = editPage.locator('input[name="name"]').first();
  await nameInput.fill(renamed);
  await editPage.getByRole("tab", { name: "Confirm" }).click();
  await editPage.locator('textarea[name="note"]').fill("rename via e2e");
  await editPage.getByRole("button", { name: "Submit Edit" }).click();

  await editPage.waitForURL(/\/edits\/[0-9a-f-]+/i, { timeout: 15_000 });
  const editId = editPage.url().split("/").pop()!;

  await approveEdit(moderatePage, editId);

  await moderatePage.goto(`/studios/${original.id}`);
  await expect(moderatePage.getByText(renamed).first()).toBeVisible({
    timeout: 15_000,
  });
});

test("studio delete: submit DESTROY edit, approve, studio marked deleted", async ({
  editPage,
  moderatePage,
}) => {
  const admin = await adminApi();
  const toDelete = await createStudio(admin);
  await admin.dispose();

  await editPage.goto(`/studios/${toDelete.id}/delete`);
  await editPage.locator('textarea[name="note"]').fill("delete via e2e");
  await editPage.getByRole("button", { name: "Submit Edit" }).click();

  await editPage.waitForURL(/\/edits\/[0-9a-f-]+/i, { timeout: 15_000 });
  const editId = editPage.url().split("/").pop()!;

  await approveEdit(moderatePage, editId);

  const admin2 = await adminApi();
  // A DESTROY edit marks the row deleted and the API stops resolving it. That behaviour is asserted
  // in Go -- deleted_entity_lookup_integration_test.go's TestFindStudioDoesNotReturnDeletedStudio --
  // and this test's job is that the UI flow REACHES it. The shape of the probe matters: a lookup that
  // returns null is also what a typo in the id or a broken query returns, so the live entity beside it
  // is the control that makes the null mean "deleted" rather than "the query is wrong".
  const data = await gql<{ findStudio: { id: string } | null }>(
    admin2,
    `query($id: ID!) { findStudio(id: $id) { id } }`,
    { id: toDelete.id },
  );
  expect(data.findStudio, "the deleted studio still resolves").toBeNull();

  const live = await createStudio(admin2);
  const liveData = await gql<{ findStudio: { id: string } | null }>(
    admin2,
    `query($id: ID!) { findStudio(id: $id) { id } }`,
    { id: live.id },
  );
  await admin2.dispose();
  expect(
    liveData.findStudio?.id,
    "a studio that was not deleted does not resolve either, so the null above proves nothing",
  ).toBe(live.id);
});

test("performer rename: submit MODIFY edit, approve, name changes", async ({
  editPage,
  moderatePage,
}) => {
  const admin = await adminApi();
  const original = await createPerformer(admin);
  await admin.dispose();

  const renamed = uniq("PerformerRenamed");

  await editPage.goto(`/performers/${original.id}/edit`);
  await editPage.locator('input[name="name"]').first().fill(renamed);
  await editPage.getByRole("tab", { name: "Confirm" }).click();
  await editPage.locator('textarea[name="note"]').fill("rename via e2e");
  await editPage.getByRole("button", { name: "Submit Edit" }).click();

  await editPage.waitForURL(/\/edits\/[0-9a-f-]+/i, { timeout: 15_000 });
  const editId = editPage.url().split("/").pop()!;

  await approveEdit(moderatePage, editId);

  await moderatePage.goto(`/performers/${original.id}`);
  await expect(moderatePage.getByText(renamed).first()).toBeVisible({
    timeout: 15_000,
  });
});

test("performer delete: submit DESTROY edit, approve, performer marked deleted", async ({
  editPage,
  moderatePage,
}) => {
  const admin = await adminApi();
  const toDelete = await createPerformer(admin);
  await admin.dispose();

  await editPage.goto(`/performers/${toDelete.id}/delete`);
  await editPage.locator('textarea[name="note"]').fill("delete via e2e");
  await editPage.getByRole("button", { name: "Submit Edit" }).click();

  await editPage.waitForURL(/\/edits\/[0-9a-f-]+/i, { timeout: 15_000 });
  const editId = editPage.url().split("/").pop()!;

  await approveEdit(moderatePage, editId);

  const admin2 = await adminApi();
  // A DESTROY edit marks the row deleted and the API stops resolving it. That behaviour is asserted
  // in Go -- deleted_entity_lookup_integration_test.go's TestFindStudioDoesNotReturnDeletedStudio --
  // and this test's job is that the UI flow REACHES it. The shape of the probe matters: a lookup that
  // returns null is also what a typo in the id or a broken query returns, so the live entity beside it
  // is the control that makes the null mean "deleted" rather than "the query is wrong".
  const data = await gql<{ findPerformer: { id: string } | null }>(
    admin2,
    `query($id: ID!) { findPerformer(id: $id) { id } }`,
    { id: toDelete.id },
  );
  expect(data.findPerformer, "the deleted performer still resolves").toBeNull();

  const live = await createPerformer(admin2);
  const liveData = await gql<{ findPerformer: { id: string } | null }>(
    admin2,
    `query($id: ID!) { findPerformer(id: $id) { id } }`,
    { id: live.id },
  );
  await admin2.dispose();
  expect(
    liveData.findPerformer?.id,
    "a performer that was not deleted does not resolve either, so the null above proves nothing",
  ).toBe(live.id);
});

test("tag rename: submit MODIFY edit, approve, name changes", async ({
  editPage,
  moderatePage,
}) => {
  const admin = await adminApi();
  const category = await createTagCategory(admin);
  const original = await createTag(admin, { categoryId: category.id });
  await admin.dispose();

  const renamed = uniq("TagRenamed");

  await editPage.goto(`/tags/${original.id}/edit`);
  // Tag form is single-page (no tabs).
  await editPage.locator('input[name="name"]').first().fill(renamed);
  await editPage.locator('textarea[name="note"]').fill("rename via e2e");
  await editPage.getByRole("button", { name: "Submit Edit" }).click();

  await editPage.waitForURL(/\/edits\/[0-9a-f-]+/i, { timeout: 15_000 });
  const editId = editPage.url().split("/").pop()!;

  await approveEdit(moderatePage, editId);

  await moderatePage.goto(`/tags/${original.id}`);
  await expect(moderatePage.getByText(renamed).first()).toBeVisible({
    timeout: 15_000,
  });
});

test("tag delete: submit DESTROY edit, approve, tag marked deleted", async ({
  editPage,
  moderatePage,
}) => {
  const admin = await adminApi();
  const category = await createTagCategory(admin);
  const toDelete = await createTag(admin, { categoryId: category.id });
  await admin.dispose();

  await editPage.goto(`/tags/${toDelete.id}/delete`);
  await editPage.locator('textarea[name="note"]').fill("delete via e2e");
  await editPage.getByRole("button", { name: "Submit Edit" }).click();

  await editPage.waitForURL(/\/edits\/[0-9a-f-]+/i, { timeout: 15_000 });
  const editId = editPage.url().split("/").pop()!;

  await approveEdit(moderatePage, editId);

  const admin2 = await adminApi();
  // A DESTROY edit marks the row deleted and the API stops resolving it. That behaviour is asserted in
  // Go -- deleted_entity_lookup_integration_test.go -- and this test's job is that the UI flow REACHES
  // it. The shape of the probe matters: a lookup that returns null is also what a typo in the id or a
  // broken query returns, so the live tag beside it is the control that makes the null mean "deleted"
  // rather than "the query is wrong".
  const data = await gql<{ findTag: { id: string } | null }>(
    admin2,
    `query($id: ID!) { findTag(id: $id) { id } }`,
    { id: toDelete.id },
  );
  expect(data.findTag, "the deleted tag still resolves").toBeNull();

  const liveCategory = await createTagCategory(admin2);
  const live = await createTag(admin2, { categoryId: liveCategory.id });
  const liveData = await gql<{ findTag: { id: string } | null }>(
    admin2,
    `query($id: ID!) { findTag(id: $id) { id } }`,
    { id: live.id },
  );
  await admin2.dispose();
  expect(
    liveData.findTag?.id,
    "a tag that was not deleted does not resolve either, so the null above proves nothing",
  ).toBe(live.id);
});

test("scene rename: submit MODIFY edit, approve, title changes", async ({
  editPage,
  moderatePage,
}) => {
  const admin = await adminApi();
  const { createScene, createStudio } = await import(
    "../../support/helpers/seed"
  );
  const studio = await createStudio(admin);
  const original = await createScene(admin, { studioId: studio.id });
  await admin.dispose();

  const renamed = uniq("SceneRenamed");

  await editPage.goto(`/scenes/${original.id}/edit`);
  // Scene form's title input — pre-filled with the existing title.
  await editPage.getByPlaceholder("Title").fill(renamed);
  await editPage.getByRole("tab", { name: "Confirm" }).click();
  await editPage.locator('textarea[name="note"]').fill("rename via e2e");
  await editPage.getByRole("button", { name: "Submit Edit" }).click();

  await editPage.waitForURL(/\/edits\/[0-9a-f-]+/i, { timeout: 15_000 });
  const editId = editPage.url().split("/").pop()!;

  await approveEdit(moderatePage, editId);

  await moderatePage.goto(`/scenes/${original.id}`);
  await expect(moderatePage.getByText(renamed).first()).toBeVisible({
    timeout: 15_000,
  });
});

test("scene delete: submit DESTROY edit, approve, scene marked deleted", async ({
  editPage,
  moderatePage,
}) => {
  const admin = await adminApi();
  const { createScene, createStudio } = await import(
    "../../support/helpers/seed"
  );
  const studio = await createStudio(admin);
  const toDelete = await createScene(admin, { studioId: studio.id });
  await admin.dispose();

  await editPage.goto(`/scenes/${toDelete.id}/delete`);
  await editPage.locator('textarea[name="note"]').fill("delete via e2e");
  await editPage.getByRole("button", { name: "Submit Edit" }).click();

  await editPage.waitForURL(/\/edits\/[0-9a-f-]+/i, { timeout: 15_000 });
  const editId = editPage.url().split("/").pop()!;

  await approveEdit(moderatePage, editId);

  const admin2 = await adminApi();
  const data = await gql<{ findScene: { deleted: boolean } | null }>(
    admin2,
    `query($id: ID!) { findScene(id: $id) { deleted } }`,
    { id: toDelete.id },
  );
  await admin2.dispose();
  expect(data.findScene?.deleted).toBe(true);
});
