//go:build integration

package imagetype_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/models"
)

// The untagged tests in this package cover withGroupPreference and withPreference
// as pure functions. VocabularyFor is the only caller of either, it is what
// internal/dataloader/loaders.go:156 resolves per image to rank it, and it had NO
// test at all -- so deleting either application line from it left this suite green.
// Verified by mutation: inverting the typePosition comparator is KILLED, but
// removing `vocabulary = vocabulary.withGroupPreference(preferredGroups)` SURVIVES.
//
// What needs a database and cannot be checked without one: that the rows in
// user_image_type_preferences and user_image_type_group_preferences are the ones
// applied, that a user with no rows gets the instance ordering, and that the two
// preference kinds compose.
//
// Needs its own TestMain: dbtest.initPostgres drops every table before the tests
// run, so the database cannot be shared with another package in the same binary.

func TestMain(m *testing.M) {
	dbtest.TestWithDatabase(m, nil)
}

// Keys pinned against migration 94's seed, NOT read back from the database --
// reading them back would let the fixture agree with whatever the DB contains,
// which is the round-trip that proves nothing. First guess at these was wrong:
// `FACE` and `POSE` do not exist; the keys are CROP_FACE and SHOT_PORTRAIT, and
// the five groups are SHOT, CROP, VIEW, POSTURE, DRESS.
var (
	shotPortrait = models.ImageTypeEnum("SHOT_PORTRAIT")
	shotDetail   = models.ImageTypeEnum("SHOT_DETAIL")
	cropFace     = models.ImageTypeEnum("CROP_FACE")
	cropWide     = models.ImageTypeEnum("CROP_WIDE")
)

func TestVocabularyForWithNoPreferencesIsInstanceOrdering(t *testing.T) {
	ctx := context.Background()
	user := createUser(t, ctx)
	svc := dbtest.Factory().ImageType()

	got, err := svc.VocabularyFor(ctx, user)
	require.NoError(t, err)
	require.NotNil(t, got)

	// Instance() returns the receiver when there is no parent, so this is NOT a
	// nil check -- it is "this vocabulary IS the instance ordering".
	require.Same(t, got, got.Instance(),
		"a vocabulary carrying no preference must be its own instance vocabulary")

	// A second user with rows is the reference the unadjusted one must match.
	adjusted := createUser(t, ctx)
	require.NoError(t, svc.SetPreferences(ctx, adjusted,
		[]models.ImageTypeEnum{shotDetail}, nil))
	_, err = svc.VocabularyFor(ctx, adjusted)
	require.NoError(t, err)

	plain, err := svc.VocabularyFor(ctx, user)
	require.NoError(t, err)
	require.Equal(t, plain.Rank([]models.ImageTypeEnum{shotPortrait, shotDetail}),
		got.Rank([]models.ImageTypeEnum{shotPortrait, shotDetail}),
		"a user with no preferences must rank exactly as the instance does")
}

func TestVocabularyForAppliesStoredTypePreference(t *testing.T) {
	ctx := context.Background()
	user := createUser(t, ctx)
	svc := dbtest.Factory().ImageType()

	// Rank is a TUPLE, one entry per group, so reordering WITHIN a group is what
	// moves it: CROP_FACE outranks CROP_WIDE in the instance ordering, so listing
	// CROP_WIDE first must swap them. Reversing across groups would be a no-op,
	// because group order comes from groupPosition and preferences do not move it.
	require.NoError(t, svc.SetPreferences(ctx, user,
		[]models.ImageTypeEnum{cropWide, cropFace}, nil))

	baseline, err := svc.VocabularyFor(ctx, createUser(t, ctx))
	require.NoError(t, err)

	got, err := svc.VocabularyFor(ctx, user)
	require.NoError(t, err)

	// The known half: a preference exists, so Instance() must be a DIFFERENT object.
	require.NotSame(t, got, got.Instance(),
		"a vocabulary carrying a preference must record the instance ordering it departs from")

	// Rank collapses a type list to ONE tuple position per GROUP, taking the best
	// (minimum) position in that group -- so ranking both CROP types together can
	// never distinguish them. Each type must be ranked ALONE to see its own slot,
	// and the CROP group is tuple index 1 (SHOT, CROP, VIEW, POSTURE, DRESS).
	const cropGroup = 1

	instanceWide := baseline.Rank([]models.ImageTypeEnum{cropWide})[cropGroup]
	instanceFace := baseline.Rank([]models.ImageTypeEnum{cropFace})[cropGroup]
	require.Less(t, instanceFace, instanceWide,
		"fixture assumes CROP_FACE outranks CROP_WIDE in the instance ordering")

	adjustedWide := got.Rank([]models.ImageTypeEnum{cropWide})[cropGroup]
	adjustedFace := got.Rank([]models.ImageTypeEnum{cropFace})[cropGroup]

	// The decisive direction check, which is what "not equal" cannot establish:
	// listing CROP_WIDE first must put it ahead of CROP_FACE, i.e. the order of the
	// two instance positions must be INVERTED.
	require.Less(t, adjustedWide, adjustedFace,
		"listing CROP_WIDE first must swap it ahead of CROP_FACE")
	// The unlisted type is pushed DOWN behind the listed one: it keeps its own
	// instance position as the group's floor while the listed type takes the front.
	// It does not gain a number above its instance position, so the check is that
	// the two are now INVERTED and CROP_WIDE -- the one actually listed -- is ahead.
	// Measured, not assumed: instance is CROP_FACE=0, CROP_WIDE=7; after listing
	// CROP_WIDE first it is CROP_WIDE=0, CROP_FACE=1. So the preferred types are
	// RENUMBERED from 0 in listed order, and everything unlisted keeps its instance
	// position -- CROP_FACE is pushed to 1, which is the slot CROP_WIDE vacated.
	// Asserting the exact numbers, not just their order, is what makes this a
	// statement about the algorithm rather than about "they differ".
	require.Equal(t, 7, instanceWide)
	require.Equal(t, 0, instanceFace)
	require.Equal(t, 0, adjustedWide, "the listed type takes slot 0")
	require.Equal(t, 1, adjustedFace, "the unlisted type keeps its instance position")

	require.Less(t, got.Rank([]models.ImageTypeEnum{cropWide})[cropGroup],
		baseline.Rank([]models.ImageTypeEnum{cropWide})[cropGroup],
		"the listed type must move ahead of its instance position")

	stored, err := svc.Preferences(ctx, user)
	require.NoError(t, err)
	require.Equal(t, []models.ImageTypeEnum{cropWide, cropFace}, stored,
		"what VocabularyFor applied must be what round-trips through the database")
}

func TestVocabularyForRejectsUnknownPreferenceKeyAtTheDatabase(t *testing.T) {
	ctx := context.Background()
	user := createUser(t, ctx)
	svc := dbtest.Factory().ImageType()

	// The row a stale client would write after an admin retires a type. Migration
	// 94 puts a FOREIGN KEY on user_image_type_preferences.type_key, so this is
	// rejected by the database rather than filtered in Go. Asserting the rejection
	// is the point: it is the guarantee that withPreference's `if !known { continue }`
	// is unreachable through this path, and the FK is what makes it so.
	unknown := models.ImageTypeEnum("NO_SUCH_TYPE_KEY")
	err := svc.SetPreferences(ctx, user, []models.ImageTypeEnum{unknown, cropFace}, nil)
	require.Error(t, err, "an unknown type key must be rejected, not stored")
	require.Contains(t, err.Error(), "user_image_type_preferences",
		"the rejection must come from the FK on the preferences table, not from Go")

	// And the vocabulary is still usable afterwards -- a rejected write must not
	// have left a half-written preference behind.
	got, err := svc.VocabularyFor(ctx, user)
	require.NoError(t, err)
	require.NotNil(t, got)
}

func TestVocabularyForAppliesStoredGroupPreference(t *testing.T) {
	ctx := context.Background()
	user := createUser(t, ctx)
	svc := dbtest.Factory().ImageType()

	// Groups are exclusive: at most one type per group may be preferred, which is
	// why the GROUP preference exists -- to reorder the groups themselves. Instance
	// order is SHOT, CROP, VIEW, POSTURE, DRESS.
	instanceOrder, err := svc.VocabularyFor(ctx, createUser(t, ctx))
	require.NoError(t, err)
	require.Equal(t, 0, int(instanceOrder.Rank([]models.ImageTypeEnum{shotPortrait})[0]),
		"fixture assumes SHOT leads the instance ordering")

	// Move DRESS (last) to the front, and give it a type so the tuple is not all
	// Unranked. DRESS_NON_NUDE is the first DRESS type in the seed.
	require.NoError(t, svc.SetPreferences(ctx, user,
		[]models.ImageTypeEnum{models.ImageTypeEnum("DRESS_NON_NUDE")},
		[]models.ImageTypeGroupEnum{"DRESS", "SHOT"}))

	got, err := svc.VocabularyFor(ctx, user)
	require.NoError(t, err)

	dressRank := got.Rank([]models.ImageTypeEnum{models.ImageTypeEnum("DRESS_NON_NUDE")})
	shotRank := got.Rank([]models.ImageTypeEnum{shotPortrait})
	require.Less(t, dressRank[0], shotRank[0],
		"DRESS was listed before SHOT, so a DRESS type must now lead every SHOT type")
}

// --- helpers -----------------------------------------------------------------

func createUser(t *testing.T, ctx context.Context) uuid.UUID {
	t.Helper()
	name := fmt.Sprintf("imagetype-test-%s", uuid.Must(uuid.NewV4()).String()[:8])
	user, err := dbtest.Factory().User().Create(ctx, models.UserCreateInput{
		Name:     name,
		Password: "password" + name,
		Email:    name + "@example.com",
	})
	require.NoError(t, err, "creating the user the preferences hang off")
	require.NotNil(t, user)
	return user.ID
}
