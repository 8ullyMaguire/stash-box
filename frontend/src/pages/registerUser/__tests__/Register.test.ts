import { describe, expect, it } from "vitest";

import { schema } from "../Register";

const VALID_UUID = "123e4567-e89b-12d3-a456-426614174000";

const validate = (data: Record<string, unknown>, inviteRequired: boolean) =>
  schema.validate(data, { context: { inviteRequired } });

// Issue #956: "registration form still requiring invite key with
// require_invite false".
//
//   When the config flag has require_invite set to false, the registration page
//   hides the invite field but the user still gets a validation message saying
//   "invalid invite key" when clicking "Register"
//
// The field is hidden when invites are not required, so requiring it is
// validating an input the user cannot see or fix. That is the reported bug.
//
// The guard is `.when("$inviteRequired", ...)` on the schema context. The
// reporter is on v0.6.11 and that guard is newer, so the reported behaviour
// does not reproduce on this tree -- which is exactly why these tests exist:
// there was nothing pinning the rule, so a plausible refactor of the schema
// would reintroduce the bug and nothing would go red.

describe("registration invite key validation", () => {
  describe("when require_invite is false (the field is hidden)", () => {
    it("accepts an email with no invite key", async () => {
      // The reported repro: hidden field, empty form, "Invalid invite key".
      await expect(
        validate({ email: "user@example.com" }, false),
      ).resolves.toBeDefined();
    });

    it("accepts an empty invite key", async () => {
      // An empty string is the browser's value for a field the user cleared or
      // never saw. Treating "" as a supplied-but-invalid key would reject it.
      await expect(
        validate({ email: "user@example.com", inviteKey: "" }, false),
      ).resolves.toBeDefined();
    });

    it("still rejects a malformed invite key that WAS supplied", async () => {
      // Not required is not the same as unchecked. If a user pastes garbage it
      // should fail here rather than round-trip to the server.
      await expect(
        validate({ email: "user@example.com", inviteKey: "nope" }, false),
      ).rejects.toThrow("Invalid invite key");
    });

    it("accepts a well-formed invite key", async () => {
      // Optional-but-valid must pass, otherwise a genuine key cannot be used.
      await expect(
        validate({ email: "user@example.com", inviteKey: VALID_UUID }, false),
      ).resolves.toBeDefined();
    });
  });

  describe("when require_invite is true", () => {
    it("rejects a missing invite key", async () => {
      await expect(validate({ email: "user@example.com" }, true)).rejects.toThrow(
        "Invite key is required",
      );
    });

    it("rejects a malformed invite key", async () => {
      await expect(
        validate({ email: "user@example.com", inviteKey: "nope" }, true),
      ).rejects.toThrow("Invalid invite key");
    });

    it("accepts a well-formed invite key", async () => {
      await expect(
        validate({ email: "user@example.com", inviteKey: VALID_UUID }, true),
      ).resolves.toBeDefined();
    });
  });

  it("still rejects a malformed email regardless of the invite setting", async () => {
    // The invite branch must not have swallowed the email rule. The message is
    // yup's own, not the schema's "Email is required": `.email()` is tested
    // before `.required()`, so a malformed address never reaches the required
    // branch. Asserting on the rejection rather than the string keeps this test
    // about the rule, not about yup's message precedence.
    await expect(validate({ email: "not-an-email" }, false)).rejects.toThrow();
    await expect(validate({ email: "not-an-email" }, true)).rejects.toThrow();
  });

  it("still rejects a missing email when invites are not required", async () => {
    await expect(validate({}, false)).rejects.toThrow("Email is required");
  });
});
