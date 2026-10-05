import { screen } from "@testing-library/react";
import type { MockedResponse } from "@apollo/client/testing";

import { ConfigDocument, ListDocument, RoleEnum } from "src/graphql";
import { configMock } from "src/test/graphqlMocks";
import { renderForm } from "src/test/renderForm";
import { describe, expect, it } from "vitest";
import ListRoutes from "../index";

// The loader's owner gate, through index.tsx (SPEC §28).
//
// This file exists because ListView.test.tsx cannot see it. Those tests mount ListView
// directly and therefore only prove the component honours the isOwner PROP -- changing the
// loader's `isSelf(data.list.owner)` to always-true left all four of them green. A component
// test that claims to cover a gate one layer up is a comment, not a check.
//
// It also covers the second thing the loader decides: that `list(id:)` returning null is
// reported as "not found, or you do not have access", which is one message for both cases
// because the backend deliberately refuses to distinguish them.

const listMock = (ownerId: string): MockedResponse => ({
  request: { query: ListDocument, variables: { id: "l-1" } },
  result: {
    data: {
      list: {
        __typename: "List",
        id: "l-1",
        name: "Alice's favourites",
        description: null,
        publishedAt: "2026-10-01T12:00:00Z",
        itemCount: 0,
        createdAt: "2026-10-01T11:00:00Z",
        updatedAt: "2026-10-01T12:00:00Z",
        owner: { __typename: "User", id: ownerId, name: "alice" },
        items: [],
        auditTrail: [],
      },
    },
  },
  maxUsageCount: 5,
});

const aliceAuth = {
  authenticated: true,
  user: {
    id: "u-alice",
    name: "alice",
    roles: [RoleEnum.READ, RoleEnum.MODIFY],
  },
};

const bobAuth = {
  authenticated: true,
  user: {
    id: "u-bob",
    name: "bob",
    roles: [RoleEnum.READ, RoleEnum.MODIFY],
  },
};

describe("list loader owner gate", () => {
  it("gives the owner the publish control", async () => {
    // MUTATION: isOwner={isSelf(data.list.owner)} -> isOwner={true} in index.tsx
    // -> this goes red AND the case below turns red.
    renderForm(<ListRoutes />, {
      route: "/l-1",
      auth: aliceAuth,
      mocks: [configMock, listMock("u-alice")],
    });

    expect(
      await screen.findByRole("button", { name: "Make private" }),
    ).toBeInTheDocument();
  });

  it("gives a non-owner no publish control", async () => {
    // The mutation above turns THIS red too. Both cases are needed: one proves the control
    // appears, the other proves it is not merely always on.
    renderForm(<ListRoutes />, {
      route: "/l-1",
      auth: bobAuth,
      mocks: [configMock, listMock("u-alice")],
    });

    // Count before asserting absence, or the absence check is vacuous.
    expect(await screen.findByText("Alice's favourites")).toBeInTheDocument();

    expect(screen.queryByRole("button", { name: "Make private" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Publish" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Remove" })).toBeNull();
    expect(screen.queryByRole("link", { name: "Edit" })).toBeNull();
  });

  it("reports a null list as one message, never as a permission problem", async () => {
    // MUTATION: change the ErrorMessage text to "You do not have access to this list"
    // -> this goes red. Distinguishing the two would confirm a private list exists, which
    // is the leak the backend refuses to make.
    renderForm(<ListRoutes />, {
      route: "/l-1",
      auth: bobAuth,
      mocks: [
        configMock,
        {
          request: { query: ListDocument, variables: { id: "l-1" } },
          result: { data: { list: null } },
          maxUsageCount: 5,
        },
      ],
    });

    expect(
      await screen.findByText(/not found, or you do not have access/i),
    ).toBeInTheDocument();
    expect(
      screen.queryByText(/you do not have access to this list/i),
    ).toBeNull();
  });
});
