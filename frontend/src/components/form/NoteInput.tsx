import cx from "classnames";
import { type ChangeEvent, type FC, useState } from "react";
import { Form, Tab, Tabs } from "react-bootstrap";
import type { UseFormRegister } from "react-hook-form";
import EditComment from "src/components/editCard/EditComment";
import { useCurrentUser } from "src/hooks";

interface IProps {
  onChange?: (text: string) => void;
  className?: string;
  register?: UseFormRegister<{ note: string }>;
  hasError?: boolean;
  initialValue?: string;
  /**
   * The textarea's accessible name, since the field has no label of its own.
   *
   * This component is rendered inside a tab whose ONLY label is whatever the caller puts beside it --
   * and on the delete forms there is nothing beside it at all -- so the name has to live on the
   * control. Every caller that shows a visible label passes that label's text here, which is what
   * keeps WCAG 2.5.3 (label in name) true: a reader told to "click Edit Note" is talking about an
   * element whose name CONTAINS those words, not one named something else.
   */
  label?: string;
}

const NoteInput: FC<IProps> = ({
  onChange,
  className,
  register,
  hasError = false,
  initialValue = "",
  label = "Note",
}) => {
  const { user } = useCurrentUser();
  const [comment, setComment] = useState(initialValue);

  const handleChange = (e: ChangeEvent<HTMLTextAreaElement>) => {
    setComment(e.currentTarget.value);
    onChange?.(e.currentTarget.value);
  };

  const textareaProps = register ? register("note") : { name: "note" };
  const now = new Date().toISOString();

  return (
    <div className={cx("NoteInput", { "is-invalid": hasError })}>
      <Tabs id="add-comment">
        <Tab eventKey="write" title="Write" className="NoteInput-tab">
          <Form.Control
            as="textarea"
            className={className}
            aria-label={label}
            onInput={handleChange}
            rows={5}
            defaultValue={initialValue}
            {...textareaProps}
          />
        </Tab>
        <Tab eventKey="preview" title="Preview" unmountOnExit mountOnEnter>
          <EditComment
            id={`${user?.id}-${now}`}
            comment={comment}
            date={now}
            user={user}
            preview
          />
        </Tab>
      </Tabs>
    </div>
  );
};

export default NoteInput;
