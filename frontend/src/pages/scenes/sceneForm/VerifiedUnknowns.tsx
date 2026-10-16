import { type FC, useState } from "react";
import { Button, Form } from "react-bootstrap";
import type { FieldVerificationReason } from "src/graphql";
import { useFieldVerificationReasons } from "src/graphql";

export interface VerifiedUnknownClaim {
  field: string;
  reason_code: string;
  citation_url: string | null;
}

interface ControlProps {
  /** The reason registry, in the order the server returned it. */
  reasons: FieldVerificationReason[];
  value: VerifiedUnknownClaim[];
  onChange: (claims: VerifiedUnknownClaim[]) => void;
  /** The in-progress entry's field, cleared after each Add. */
  field: string;
  onFieldChange: (field: string) => void;
  reasonCode: string;
  onReasonChange: (code: string) => void;
}

/**
 * The verified-unknown control for the scene edit form (SPEC §7.24.1).
 *
 * WHAT IT IS FOR. The gaps in a scene's metadata are not all answerable: a date nobody ever published
 * is not a task anybody can finish, and without a way to say so the field is missing forever, gets
 * bountied forever, and the score never reaches 100%. This is the only way to say it, and it is an
 * edit rather than a moderator action so it earns trust like any other claim.
 *
 * THE REASON IS A DROPDOWN AND NEVER A TEXT BOX, which is the whole design: "not publicly knowable"
 * and "exists but nobody has looked" are DIFFERENT FACTS, and the second must stay actionable. A
 * free-text reason collapses them, and a collapsed reason turns an assertion into a way to clear a
 * completion gap without doing the work.
 *
 * THE CONSEQUENCE IS SHOWN PER OPTION, because it is what the curator is choosing between: one reason
 * closes the gap and the other records a belief while leaving it open. Asking for that choice without
 * stating its effect would be asking for it blind -- and the difference is not cosmetic, since one of
 * them removes the field from the missing list and the other does not.
 *
 * `renderVerifiedUnknowns` is the pure body, split out the way the edit card splits `renderWaiting`:
 * the rules worth testing are about the control's SHAPE, and none of them need a GraphQL provider.
 */
export const renderVerifiedUnknowns = ({
  reasons,
  value,
  onChange,
  field,
  onFieldChange,
  reasonCode,
  onReasonChange,
}: ControlProps) => {
  // The first reason is the default rather than an empty option, so a claim can never be added with
  // no reason at all -- the server refuses an empty code anyway, and a refused edit is a worse way to
  // learn that. The order comes from the server (by code), so this does not move between renders.
  const chosen = reasonCode || reasons[0]?.code || "";

  const add = () => {
    if (!field.trim() || !chosen) return;
    onChange([
      ...value,
      { field: field.trim(), reason_code: chosen, citation_url: null },
    ]);
    onFieldChange("");
  };

  return (
    <Form.Group controlId="verified_unknowns" className="mb-3">
      <Form.Label>Confirmed unanswerable</Form.Label>
      <Form.Text className="d-block text-muted mb-2">
        Mark a field that <strong>cannot</strong> be supplied. This is not for a
        field nobody has got to yet.
      </Form.Text>

      <div className="d-flex gap-2 mb-2">
        <Form.Control
          aria-label="Field name"
          placeholder="field, e.g. date"
          value={field}
          onChange={(e) => onFieldChange(e.target.value)}
        />
        <Form.Control
          as="select"
          aria-label="Reason"
          value={chosen}
          onChange={(e) => onReasonChange(e.target.value)}
        >
          {reasons.map((r) => (
            <option key={r.code} value={r.code}>
              {r.description}
              {r.suppresses_gap
                ? " (closes the gap)"
                : " (leaves the gap open)"}
            </option>
          ))}
        </Form.Control>
        {/* The accessible name is NOT "Add": this form already has an Add button for URLs, and two
            controls with one name are indistinguishable to a screen reader -- and to voice control,
            which is how the collision was found (two existing form tests started failing with
            "Found multiple elements with the role button and name Add"). The visible text stays
            "Add" and is contained in the label, which is what WCAG 2.5.3 (label in name) asks for so
            that saying "click Add" still works. */}
        <Button
          type="button"
          variant="secondary"
          aria-label="Add unanswerable field"
          onClick={add}
        >
          Add
        </Button>
      </div>

      {value.length > 0 && (
        <ul className="list-unstyled mb-0">
          {value.map((claim, index) => (
            <li
              key={`${claim.field}-${claim.reason_code}`}
              className="d-flex align-items-center"
            >
              <code className="me-2">{claim.field}</code>
              <span className="text-muted me-auto">
                {reasons.find((r) => r.code === claim.reason_code)
                  ?.description ?? claim.reason_code}
              </span>
              <Button
                type="button"
                variant="link"
                size="sm"
                aria-label={`Remove the ${claim.field} claim`}
                onClick={() => onChange(value.filter((_, i) => i !== index))}
              >
                Remove
              </Button>
            </li>
          ))}
        </ul>
      )}
    </Form.Group>
  );
};

const VerifiedUnknowns: FC<
  Omit<ControlProps, "reasons" | "reasonCode" | "onReasonChange">
> = (props) => {
  const { data } = useFieldVerificationReasons();
  const [reasonCode, setReasonCode] = useState("");
  return (
    <VerifiedUnknownsWithReasons
      {...props}
      reasonCode={reasonCode}
      onReasonChange={setReasonCode}
      reasons={data?.fieldVerificationReasons ?? []}
    />
  );
};

const VerifiedUnknownsWithReasons: FC<ControlProps> = (props) =>
  renderVerifiedUnknowns(props);

export default VerifiedUnknowns;
