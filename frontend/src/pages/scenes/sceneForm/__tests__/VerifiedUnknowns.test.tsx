import { fireEvent, screen } from "@testing-library/react";
import { useState } from "react";
import type { FieldVerificationReason } from "src/graphql";
import { renderForm } from "src/test/renderForm";
import { describe, expect, it } from "vitest";
import {
  renderVerifiedUnknowns,
  type VerifiedUnknownClaim,
} from "../VerifiedUnknowns";

/**
 * The verified-unknown control (SPEC §7.24.1).
 *
 * The rules here are about the control's SHAPE, and each one is a decision in the section rather than
 * a rendering detail: the reason is CHOSEN FROM THE REGISTRY and never typed, every option states
 * whether it closes the gap, and a claim cannot be added with no field. A free-text reason, or a
 * dropdown that hid the consequence, would both pass a test that only checked "a claim can be added".
 */

const REASONS: FieldVerificationReason[] = [
  {
    __typename: "FieldVerificationReason",
    code: "not_publicly_knowable",
    description: "The value is not publicly available",
    suppresses_gap: true,
  },
  {
    __typename: "FieldVerificationReason",
    code: "not_yet_looked",
    description: "The value is knowable but has not been researched yet",
    suppresses_gap: false,
  },
];

/** A controlled wrapper, because the control is controlled: the claim list lives in the form. */
const Harness = ({
  reasons = REASONS,
}: {
  reasons?: FieldVerificationReason[];
}) => {
  const [claims, setClaims] = useState<VerifiedUnknownClaim[]>([]);
  const [field, setField] = useState("");
  const [reasonCode, setReasonCode] = useState("");
  return (
    <div>
      {renderVerifiedUnknowns({
        reasons,
        value: claims,
        onChange: setClaims,
        field,
        onFieldChange: setField,
        reasonCode,
        onReasonChange: setReasonCode,
      })}
      {/* An always-rendered sibling, so "no claims yet" is about the rule and not about a tree
          that rendered nothing at all. */}
      <span data-testid="control" />
    </div>
  );
};

const addClaim = (field: string, reason?: string) => {
  fireEvent.change(screen.getByLabelText("Field name"), {
    target: { value: field },
  });
  if (reason) {
    fireEvent.change(screen.getByLabelText("Reason"), {
      target: { value: reason },
    });
  }
  fireEvent.click(
    screen.getByRole("button", { name: "Add unanswerable field" }),
  );
};

describe("renderVerifiedUnknowns", () => {
  it("offers only registry reasons, and states each one's consequence", () => {
    renderForm(<Harness />);

    const options = screen.getAllByRole("option");
    expect(options).toHaveLength(2);

    // The distinction the section exists for: one closes the gap and one leaves it actionable.
    expect(options[0]).toHaveTextContent(/closes the gap/);
    expect(options[1]).toHaveTextContent(/leaves the gap open/);
  });

  it("has no free-text reason field", () => {
    renderForm(<Harness />);

    // Subject first: the control rendered, so the absence below is about the rule.
    expect(screen.getByTestId("control")).toBeInTheDocument();
    // A textarea or a text input for the reason would let a curator collapse "not knowable" and
    // "not looked at" into one phrase, which is the failure the codes exist to prevent.
    expect(
      screen.queryByLabelText(/reason/i, { selector: "input[type=text]" }),
    ).toBeNull();
    expect(screen.getByLabelText("Reason").tagName).toBe("SELECT");
  });

  it("adds a claim with the chosen reason code and clears the field", () => {
    renderForm(<Harness />);

    addClaim("date", "not_yet_looked");

    expect(screen.getByText("date")).toBeInTheDocument();
    // The claim carries the CODE, not the description: the code is what the server validates and
    // what the missing list is keyed by.
    expect(screen.getByText(REASONS[1].description)).toBeInTheDocument();
    expect(screen.getByLabelText("Field name")).toHaveValue("");
  });

  it("refuses to add a claim with no field", () => {
    renderForm(<Harness />);

    fireEvent.click(
      screen.getByRole("button", { name: "Add unanswerable field" }),
    );

    // Count the subject first: no claim rows, on a control that is definitely rendered.
    expect(screen.getByTestId("control")).toBeInTheDocument();
    expect(
      screen.queryAllByRole("button", { name: /^Remove the/ }),
    ).toHaveLength(0);
  });

  it("removes exactly the claim that was removed", () => {
    renderForm(<Harness />);

    addClaim("date");
    addClaim("details");
    expect(screen.getAllByRole("button", { name: /^Remove the/ })).toHaveLength(
      2,
    );

    // By NAME, not by index: "the first Remove button" is an assertion about DOM order, while the
    // rule under test is that removing a claim removes exactly it.
    fireEvent.click(
      screen.getByRole("button", { name: "Remove the date claim" }),
    );

    expect(screen.getAllByRole("button", { name: /^Remove the/ })).toHaveLength(
      1,
    );
    expect(screen.queryByText("date")).toBeNull();
    expect(screen.getByText("details")).toBeInTheDocument();
  });

  it("is inert when the registry is empty, rather than adding a reasonless claim", () => {
    renderForm(<Harness reasons={[]} />);

    addClaim("date");

    expect(screen.getByTestId("control")).toBeInTheDocument();
    expect(
      screen.queryAllByRole("button", { name: /^Remove the/ }),
    ).toHaveLength(0);
  });
});
