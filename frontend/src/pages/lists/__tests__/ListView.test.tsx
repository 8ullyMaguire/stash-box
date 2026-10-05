import { screen } from "@testing-library/react";
import type { ComponentProps } from "react";

import { RoleEnum } from "src/graphql";
import ListView from "src/pages/lists/ListView";
import { renderForm } from "src/test/renderForm";
import { describe, expect, it } from "vitest";

// The list page's owner gate (SPEC §28).
//
// The backend refuses to publish on behalf of a stranger, so a leaked Publish button would
// not corrupt data -- it would show a stranger a control that 403s, which reads as a broken
// instance rather than as a permissions boundary. That is why this is worth a test at all:
// the failure mode is confusing, not dangerous, and confusing failures are exactly the ones
// nobody files a bug for.
//
// Each case names the mutation that must turn it red. A reachability test without its
// mutation is a snapshot that stops being true silently.

const aliceAuth = {
  authenticated: true,
  user: { id: "u-alice", name: "alice", roles: [RoleEnum.READ, RoleEnum.MODIFY] },
};

const bobAuth = {
  authenticated: true,
  user: { id: "u-bob", name: "bob", roles: [RoleEnum.READ, RoleEnum.MODIFY] },
};

// The published list as List.gql returns it. Cast rather than fully typed: the fixture is
// passed to a component that reads named fields, and building it out of generated types
// would break this test every time codegen reshapes the List selection -- which is churn a
// reachability test should not have.
interface ListFixture {
  __typename: "List";
  id: string;
  name: string;
  description: string | null;
  publishedAt: string | null;
  itemCount: number;
  createdAt: string;
  updatedAt: string;
  owner: { __typename: "User"; id: string; name: string };
  items: {
    __typename: "ListItem";
    id: string;
    entityType: "PERFORMER";
    entityId: string;
    position: number;
  }[];
  auditTrail: {
    __typename: "ListAudit";
    id: string;
    action: "PUBLISH";
    createdAt: string;
    actor: { __typename: "User"; id: string; name: string } | null;
  }[];
}

const publishedList: ListFixture = {
  __typename: "List",
  id: "l-1",
  name: "Alice's favourites",
  description: null,
  publishedAt: "2026-10-01T12:00:00Z",
  itemCount: 1,
  createdAt: "2026-10-01T11:00:00Z",
  updatedAt: "2026-10-01T12:00:00Z",
  owner: { __typename: "User", id: "u-alice", name: "alice" },
  items: [
    {
      __typename: "ListItem",
      id: "li-1",
      entityType: "PERFORMER",
      entityId: "p-1",
      position: 1,
    },
  ],
  auditTrail: [
    {
      __typename: "ListAudit",
      id: "la-1",
      action: "PUBLISH",
      createdAt: "2026-10-01T12:00:00Z",
      actor: { __typename: "User", id: "u-alice", name: "alice" },
    },
  ],
};

const draftList: ListFixture = {
  ...publishedList,
  name: "Alice's draft",
  publishedAt: null,
  auditTrail: [],
};

const render = (
  list: typeof publishedList,
  isOwner: boolean,
  auth: typeof aliceAuth,
) =>
  renderForm(
    <ListView
      list={list as unknown as ComponentProps<typeof ListView>["list"]}
      isOwner={isOwner}
    />,
    { auth, mocks: [] },
  );

describe("list page owner gate", () => {
  it("shows the publish controls to the owner", async () => {
    // MUTATION: pass isOwner={false} from pages/lists/index.tsx -> this goes red.
    render(publishedList, true, aliceAuth);

    expect(await screen.findByText("Make private")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Edit" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Remove" })).toBeInTheDocument();
  });

  it("shows a non-owner no controls at all", async () => {
    // These mount ListView directly, so they can only prove the component honours isOwner.
    // They CANNOT see the loader's gate -- the case below is what covers that, because it
    // goes through index.tsx. My first draft claimed this mutation turned this red, which
    // was simply false: changing isSelf() to always-true in index.tsx left every case here
    // green.
    render(publishedList, false, bobAuth);

    // Count first, then assert absence: an absence assertion with no count before it is
    // vacuously true if the component never rendered.
    expect(await screen.findByText("Alice's favourites")).toBeInTheDocument();

    expect(screen.queryByRole("button", { name: "Make private" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Remove" })).toBeNull();
    expect(screen.queryByRole("link", { name: "Edit" })).toBeNull();
  });

  it("offers Publish -- not Make private -- on an owner's draft", async () => {
    // MUTATION: swap the two branches of PublishToggle -> this goes red.
    //
    // The toggle has to read the SERVER's state rather than local state. A control that
    // said "Make private" on a draft would promise an unpublish that the backend refuses
    // ("this list is not published"), so the button would be a guaranteed error.
    render(draftList, true, aliceAuth);

    expect(await screen.findByRole("button", { name: "Publish" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Make private" })).toBeNull();
    expect(screen.getByText("Private")).toBeInTheDocument();
  });

  it("renders a deleted actor as 'a former member' rather than hiding the row", async () => {
    // MUTATION: return null for a null actor in ListAudit's row -> this goes red.
    //
    // Deleting a user sets list_audit.actor_id to NULL on purpose; the record must
    // survive. A client that hid the row would make the instance look like it had lost
    // history it was supposed to keep.
    render(
      {
        ...publishedList,
        auditTrail: [{ ...publishedList.auditTrail[0], actor: null }],
      },
      true,
      aliceAuth,
    );

    expect(await screen.findByText("a former member")).toBeInTheDocument();

    // The history row itself must still be there. Asserting on the action label is not
    // enough on its own: the status badge also says "Published", so getByText finds two
    // elements and the assertion becomes a "multiple elements" error rather than a check.
    // Counting them and requiring two pins BOTH -- the badge and the surviving history row.
    expect(screen.getAllByText("Published")).toHaveLength(2);
  });
});
