// biome-ignore-all lint/correctness/noNestedComponentDefinitions: Necessary for react-select

import { uniq } from "lodash-es";
import type { FC } from "react";
import { Form } from "react-bootstrap";
import Select, { type OnChangeValue } from "react-select";

interface MultiSelectProps {
  values: IOptionType[];
  onChange: (values: string[]) => void;
  placeholder?: string;
  plural?: string;
  selected?: string[];
  /** Override when two of these share a placeholder on one page. */
  inputId?: string;
}

interface IOptionType {
  label: string;
  value: string;
  subValues: string[] | null;
}

const CheckboxSelect: FC<MultiSelectProps> = ({
  values,
  onChange,
  placeholder = "Select...",
  plural = "values",
  selected = [],
  inputId,
}) => {
  // react-select names its input `react-select-N-input` when it is given no id, and that input has no label
  // pointing at it -- axe: `label` [critical], "Form elements must have labels" (measured on the performer
  // page's studio filter). The placeholder already says what the control filters by, so the label uses it, and
  // it is derived HERE rather than required from every caller so that all seven usages are fixed at once.
  const selectInputId =
    inputId ??
    `checkbox-select-${placeholder.replace(/[^a-z0-9]+/gi, "-").toLowerCase()}`;
  const handleChange = (vals: OnChangeValue<IOptionType, true>) => {
    onChange(uniq(vals.flatMap((v) => [v.value, ...(v.subValues ?? [])])));
  };

  const formatLabel = (
    option: IOptionType,
    meta: { context: "menu" | "value" },
  ) => {
    if (meta.context === "menu")
      return option.subValues === null ? (
        <div className="d-flex ms-3">
          <Form.Check
            className="me-2"
            checked={selected.includes(option.value)}
          />
          {option.label}
        </div>
      ) : (
        <div className="d-flex">
          <Form.Check
            className="me-2"
            checked={selected.includes(option.value)}
          />
          <span className="text-muted">{option.label}</span>
        </div>
      );
    return `${
      selected.length === 0 ? "All" : selected.length
    } ${plural} selected`;
  };

  const selectedOptions = values.filter((val) => selected.includes(val.value));

  return (
    <>
      <label htmlFor={selectInputId} className="visually-hidden">
        {placeholder}
      </label>
      <Select
        inputId={selectInputId}
        value={selectedOptions}
        isMulti
        classNamePrefix="react-select"
        className="react-select CheckboxSelect"
        options={values}
        onChange={handleChange}
        formatOptionLabel={formatLabel}
        hideSelectedOptions={false}
        closeMenuOnSelect={false}
        placeholder={placeholder}
        noOptionsMessage={() => null}
        styles={{
          option: (base) => ({
            ...base,
            backgroundColor: "transparent",
          }),
        }}
        components={{
          DropdownIndicator: () => null,
          IndicatorSeparator: () => null,
          MultiValue: (e) =>
            e.data.value === selected[0] ? (
              <span className="text-secondary">
                {selected.length} {plural} selected
              </span>
            ) : null,
        }}
      />
    </>
  );
};

export default CheckboxSelect;
