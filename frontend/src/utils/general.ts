export const isUUID = (term: string): boolean =>
  /^[a-f\d]{8}-[a-f\d]{4}-[a-f\d]{4}-[a-f\d]{4}-[a-f\d]{12}$/i.test(term);

/**
 * Pick a form default from an edit's proposed value, falling back to the
 * entity's current value.
 *
 * `??` cannot be used here. In an edit, `null` is a deliberate deletion and
 * `undefined` means "this edit does not touch the field", so collapsing the
 * two with `??` (or `||`) resurrects a value the contributor asked to remove
 * whenever the edit is re-submitted. Only `undefined` falls through.
 *
 * `T` is the type of a NON-EMPTY current value, so the proposed side admits
 * `null` — a deletion is the whole reason this helper exists. A caller whose
 * field is genuinely non-nullable passes a `T` that excludes null and gets a
 * compile error rather than a silent coercion.
 *
 * See issue #879.
 */
export const proposedOrCurrent = <T>(
  proposed: T | null | undefined,
  current: T | undefined,
): T | null | undefined => (proposed !== undefined ? proposed : current);
