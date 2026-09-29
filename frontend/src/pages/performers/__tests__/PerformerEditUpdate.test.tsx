import { screen, waitFor } from "@testing-library/react";
import type { EditUpdateQuery } from "src/graphql";
import { renderForm } from "src/test/renderForm";
import { describe, expect, it } from "vitest";

import { PerformerEditUpdate } from "../PerformerEditUpdate";

type Edit = NonNullable<EditUpdateQuery["findEdit"]>;

const baseEdit = {
  __typename: "Edit",
  id: "e-1",
  target_type: "PERFORMER",
  operation: "MERGE",
  status: "PENDING",
  applied: false,
  closed: null,
  created: "2026-01-01T00:00:00Z",
  updated: null,
  updatable: true,
  update_count: 0,
  vote_count: 0,
  options: null,
  user: null,
  merge_sources: [
    { __typename: "Performer", id: "src-1" },
    { __typename: "Performer", id: "src-2" },
  ],
  target: {
    __typename: "Performer",
    id: "p-1",
    name: "Target",
    aliases: [],
    deleted: false,
  },
  details: {
    __typename: "PerformerEdit",
    name: "Target",
    aliases: [],
    urls: [],
    tattoos: [],
    piercings: [],
    images: [],
  },
} as unknown as Edit;

const editWith = (overrides: Partial<Edit>) =>
  ({ ...baseEdit, ...overrides }) as Edit;

const sourceItems = () =>
  screen.getByTestId("merge-source-list").querySelectorAll("li");

// #703 applies to every entity type: the update form has to let the merge
// sources be seen and changed, not just round-tripped blind.
describe("PerformerEditUpdate merge sources (#703)", () => {
  it("lists the existing merge sources on a merge edit", async () => {
    renderForm(<PerformerEditUpdate edit={editWith({})} />);

    await screen.findByTestId("merge-source-list");
    expect(sourceItems()).toHaveLength(2);
  });

  it("lets a merge source be removed", async () => {
    renderForm(<PerformerEditUpdate edit={editWith({})} />);

    await screen.findByTestId("merge-source-list");
    expect(sourceItems()).toHaveLength(2);

    const removeButtons = screen.getAllByRole("button", {
      name: /Remove merge source/,
    });
    await removeButtons[0].click();

    await waitFor(() => expect(sourceItems()).toHaveLength(1));
  });

  it("does not render the source selector on a non-merge edit", async () => {
    renderForm(
      <PerformerEditUpdate
        edit={editWith({ operation: "MODIFY" } as Partial<Edit>)}
      />,
    );

    await waitFor(() =>
      expect(screen.queryByTestId("merge-source-list")).not.toBeInTheDocument(),
    );
  });
});
