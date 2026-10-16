// biome-ignore-all lint/correctness/noNestedComponentDefinitions: react-select

import type { Lens } from "@hookform/lenses";
import type { ChangeEvent, FC } from "react";
import { Button, Col, Form, InputGroup, Row } from "react-bootstrap";
import { useFieldArray } from "react-hook-form";
import { components } from "react-select";
import Creatable from "react-select/creatable";

export type BodyModItem = {
  location: string;
  description?: string | null | undefined;
};

interface BodyModificationProps {
  name: string;
  lens: Lens<BodyModItem[]>;
  locationPlaceholder: string;
  descriptionPlaceholder: string;
  formatLabel: (text: string) => string;
}

const CLASSNAME = "BodyModification";

const BodyModification: FC<BodyModificationProps> = ({
  name,
  locationPlaceholder,
  descriptionPlaceholder,
  lens,
  formatLabel,
}) => {
  const interop = lens.interop();
  const {
    fields: modifications,
    append,
    remove,
    update,
  } = useFieldArray({
    control: interop.control,
    name: interop.name,
    keyName: "key",
  });

  const isNewLocationValid = (inputValue: string): boolean =>
    !!inputValue &&
    !modifications.find(({ location }) => inputValue === location);

  const handleNewLocation = (inputValue: string) => {
    append({ location: inputValue });
  };

  const modificationList = modifications.map((mod, index) => (
    <Row key={mod.location} className="mb-1">
      <InputGroup className="col">
        <InputGroup.Text className="fw-bold">Location</InputGroup.Text>
        <Form.Control defaultValue={mod.location} readOnly />
        <Form.Control
          defaultValue={mod.description ?? ""}
          placeholder={descriptionPlaceholder}
          onInput={(e: ChangeEvent<HTMLInputElement>) =>
            update(index, {
              location: mod.location,
              description: e.currentTarget.value,
            })
          }
        />
        <Button variant="danger" onClick={() => remove(index)}>
          Remove
        </Button>
      </InputGroup>
    </Row>
  ));

  return (
    <>
      <Row className={CLASSNAME}>
        <Col className="mb-3">
          {/* The id is what associates this label with the picker below. Without it the label is a
              sibling rather than a name, and react-select names its input `react-select-N-input` --
              which is how the performer form's audit reported an unlabelled form element. `name` is
              the modification's kind ("tattoos", "piercings"), so the id is unique among the pickers
              this component renders. */}
          <Form.Label
            htmlFor={`body-modification-${name}`}
            className="text-capitalize"
          >
            {name}
          </Form.Label>
          <Creatable
            classNamePrefix="react-select"
            inputId={`body-modification-${name}`}
            value={null}
            name={name}
            placeholder={locationPlaceholder}
            isValidNewOption={isNewLocationValid}
            onCreateOption={handleNewLocation}
            formatCreateLabel={formatLabel}
            components={{
              DropdownIndicator: () => null,
              Menu: (data) =>
                data.options.length > 0 ? <components.Menu {...data} /> : null,
            }}
          />
        </Col>
      </Row>
      {modificationList}
    </>
  );
};

export default BodyModification;
