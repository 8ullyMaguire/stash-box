import { type FC, useState } from "react";
import { Col, Row } from "react-bootstrap";
import type { OperationEnum } from "src/graphql";

export interface MergeSource {
  id: string;
  name: string;
}

interface Props {
  /** Ids currently on the edit. Names are filled in as they load. */
  sources: MergeSource[];
  /** Called with the new full list whenever it changes. */
  onChange: (sources: MergeSource[]) => void;
  /** Renders the "add a source" control. */
  children: (excludeIds: string[]) => React.ReactNode;
  /** Id of the merge target, so it can never be selected as a source. */
  targetId?: string | null;
  testId?: string;
  label?: string;
}

// A merge edit's source list, shared by the per-entity update forms.
//
// #703: updating a merge edit used to open the plain entity form, which carried
// the merge sources on submit but offered no way to see, add or drop them, so the
// only recourse was cancelling the edit and filing a new one.
export const MergeSourceEditor: FC<Props> = ({
  sources,
  onChange,
  children,
  targetId,
  testId = "merge-source-list",
  label = "Merge sources",
}) => {
  const [internal, setInternal] = useState<MergeSource[] | null>(null);
  const current = internal ?? sources;

  const update = (next: MergeSource[]) => {
    setInternal(next);
    onChange(next);
  };

  const excludeIds = [
    ...(targetId ? [targetId] : []),
    ...current.map((s) => s.id),
  ];

  return (
    <Row className="g-0">
      <Col xs={6}>
        <label className="form-label" htmlFor={`${testId}-select`}>
          {label}
        </label>
        {children(excludeIds)}
        {current.length > 0 && (
          <ul className="list-group mt-2" data-testid={testId}>
            {current.map((s) => (
              <li
                key={s.id}
                className="list-group-item d-flex justify-content-between align-items-center"
              >
                <span>{s.name || <em>loading…</em>}</span>
                <button
                  type="button"
                  className="btn btn-sm btn-outline-danger"
                  onClick={() => update(current.filter((x) => x.id !== s.id))}
                  aria-label={`Remove merge source ${s.name || s.id}`}
                >
                  Remove
                </button>
              </li>
            ))}
          </ul>
        )}
      </Col>
    </Row>
  );
};

// True when the edit is a merge and therefore needs the source editor.
export const isMergeEdit = (operation: string | OperationEnum): boolean =>
  String(operation) === "MERGE";
