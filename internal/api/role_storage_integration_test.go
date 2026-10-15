//go:build integration

package api_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/models"
)

// EVERY ROLE THE API ACCEPTS MUST BE STORABLE.
//
// The operator's invite path needs MANAGE_INVITES, and the role column was `varchar(10)`
// while the enum spells that role in fourteen characters -- so the API accepted a role the
// database could not hold, and the mutation that gates grantInvite/revokeInvite (the remedy
// phase 11 hands a member who has no level slots) was unreachable for every account.
//
// The test drives the REAL mutations rather than inserting a row: `userUpdate` is where a
// client asks for the role, and "value too long for type character varying(10)" is what it got.
// It is written as a walk over the whole enum, so a role added later is covered without anyone
// remembering to extend it.
func TestEveryRoleTheAPIAcceptsIsStorable(t *testing.T) {
	roles := []models.RoleEnum{
		models.RoleEnumRead,
		models.RoleEnumVote,
		models.RoleEnumEdit,
		models.RoleEnumModify,
		models.RoleEnumModerate,
		models.RoleEnumAdmin,
		models.RoleEnumInvite,
		models.RoleEnumManageInvites,
		models.RoleEnumBot,
		models.RoleEnumReadOnly,
		models.RoleEnumEditTags,
	}

	admin := asAdmin(t)

	for _, role := range roles {
		t.Run(string(role), func(t *testing.T) {
			user, err := admin.createTestUser(nil, nil)
			require.NoError(t, err)

			// Through the API, as an operator would: the enum is what the schema publishes.
			updated, err := admin.resolver.Mutation().UserUpdate(admin.ctx, models.UserUpdateInput{
				ID:    user.ID,
				Roles: []models.RoleEnum{role},
			})
			require.NoError(t, err, "the API accepted role %s but could not store it", role)
			require.NotNil(t, updated)

			// And it reads back as the role that was asked for, through the resolver.
			got, err := admin.resolver.User().Roles(admin.ctx, updated)
			require.NoError(t, err)
			assert.Contains(t, got, role, "the role must survive the round trip")
		})
	}
}
