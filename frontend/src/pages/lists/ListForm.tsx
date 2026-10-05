import { yupResolver } from "@hookform/resolvers/yup";
import cx from "classnames";
import type { FC } from "react";
import { Button, Form } from "react-bootstrap";
import { useForm } from "react-hook-form";
import * as yup from "yup";

// The 200-character limit is duplicated from the Go service's normaliseName. Two sources for
// a bound is a bound that will drift -- but the client copy exists only to fail fast, and the
// server's is the one enforced, so a user who slips past this form still gets the server's
// error. What is NOT duplicated is the trimming rule: this form does not trim, because "the
// user typed a trailing space" is not something to silently fix. The server trims and the
// user sees what was stored.
const schema = yup.object({
  name: yup
    .string()
    .max(200, "Name must be 200 characters or fewer")
    .required("Name is required"),
  description: yup
    .string()
    .max(5000, "Description must be 5000 characters or fewer")
    .nullable()
    .optional(),
});

type ListFormData = yup.Asserts<typeof schema>;

interface Props {
  id?: string;
  list?: {
    id: string;
    name: string;
    description?: string | null;
  };
  callback: (data: { name: string; description: string | null }) => void;
  saving?: boolean;
}

const ListForm: FC<Props> = ({ list, callback, saving = false }) => {
  const {
    register,
    handleSubmit,
    formState: { errors },
  } = useForm({
    resolver: yupResolver(schema),
  });

  const onSubmit = (data: ListFormData) => {
    // An empty description becomes null, not "". The backend distinguishes the two and
    // serialises null, so sending "" would store a blank description that reads as present.
    callback({
      name: data.name,
      description: data.description || null,
    });
  };

  return (
    <Form className="ListForm col-6" onSubmit={handleSubmit(onSubmit)}>
      <Form.Group controlId="name" className="mb-3">
        <Form.Label>Name</Form.Label>
        <Form.Control
          type="text"
          className={cx({ "is-invalid": errors.name })}
          placeholder="Favourites"
          {...register("name")}
          defaultValue={list?.name ?? ""}
        />
        <div className="invalid-feedback">{errors?.name?.message}</div>
      </Form.Group>

      <Form.Group controlId="description" className="mb-3">
        <Form.Label>Description</Form.Label>
        <Form.Control
          as="textarea"
          rows={4}
          className={cx({ "is-invalid": errors.description })}
          placeholder="Optional. What is this list for?"
          {...register("description")}
          defaultValue={list?.description ?? ""}
        />
        <div className="invalid-feedback">{errors?.description?.message}</div>
      </Form.Group>

      <Button type="submit" variant="primary" disabled={saving}>
        {list?.id ? "Update" : "Create"}
      </Button>
    </Form>
  );
};

export default ListForm;
