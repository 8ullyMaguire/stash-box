//go:build integration

package api_test

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
)

// The instance metrics over GraphQL (phase 16, slice 2; spec section 16.2).
//
// THE ASSERTION IS RECOMPUTATION, NOT A TABLE OF EXPECTED NUMBERS. A test that asserted `scenes_total == 12`
// against a fixture would pass for a figure that counted the wrong table, counted nothing, or had the number
// written into it -- and this package shares one database, so the absolute totals carry whatever the other
// tests left behind. So each figure is put beside a SELECT written HERE, over the table the figure claims to
// describe, and the two must agree.
//
// FOUR ARMS, each of which a plausible wrong implementation fails:
//
//  1. ONE SNAPSHOT. Every figure comes from a single statement, so the relations BETWEEN the figures hold by
//     construction: nobody is active who does not exist, no scene has sources that is not a scene, no
//     checked source is missing from the sources. Asserted here, because the cheap implementation -- one
//     query per metric -- reads a moving table and turns each of those into a race.
//  2. EVERY FIGURE CARRIES ITS DEFINITION. A bare number is not a measurement: two readers of "scenes_total"
//     who disagree about deleted scenes are reading two figures, and nothing in the response would say which
//     one they got. This is the property that makes the surface usable at all, so it is asserted for every
//     metric rather than spot-checked.
//  3. ADMIN. The figures describe the instance's own operation, so a member without the role is refused --
//     and the refusal is asserted to be a REFUSAL rather than an empty answer, because a query that silently
//     returned zero figures to everybody would satisfy "the non-admin got no figures".
//  4. THE WINDOW IS A FILTER, NOT A WORD. Two rows are planted OUTSIDE the window: a source checked 60 days
//     ago and an account created 60 days ago. The totals must move by both and the windowed figures by
//     neither. An implementation that dropped the window would move all four; one that totalled the wrong
//     thing would move neither.

type instanceMetricOutput struct {
	Key        string `json:"key"`
	Unit       string `json:"unit"`
	Definition string `json:"definition"`
	Value      int64  `json:"value"`
}

type instanceMetricsOutput struct {
	InstanceMetrics struct {
		// The Time scalar arrives as an RFC3339 string, as it does everywhere else in this API.
		GeneratedAt string                 `json:"generated_at"`
		Since       string                 `json:"since"`
		WindowDays  int                    `json:"window_days"`
		Metrics     []instanceMetricOutput `json:"metrics"`
	} `json:"instanceMetrics"`
}

func (c *graphqlClient) instanceMetrics() (*instanceMetricsOutput, error) {
	var out instanceMetricsOutput
	err := c.Post(`query { instanceMetrics { generated_at since window_days metrics { key unit definition value } } }`, &out)
	return &out, err
}

// figures indexes a reading by key, so an assertion can name the figure it is about.
func (o *instanceMetricsOutput) figures(t *testing.T) map[string]instanceMetricOutput {
	t.Helper()
	byKey := map[string]instanceMetricOutput{}
	for _, m := range o.InstanceMetrics.Metrics {
		_, duplicate := byKey[m.Key]
		require.False(t, duplicate, "the key %q was served twice, so two figures share one name", m.Key)
		byKey[m.Key] = m
	}
	return byKey
}

// metricExpectations is the test's own arithmetic: for every key, the SQL that should produce it.
//
// Written out one by one rather than derived, because a derived expectation would be the service's own logic
// again -- and the failure this file exists for is precisely a figure that disagrees with the table it names.
var metricExpectations = map[string]string{
	"members_total": `SELECT COUNT(*) FROM users u
		WHERE u.name <> '[deleted user]'
		  AND NOT EXISTS (SELECT 1 FROM user_roles r WHERE r.user_id = u.id AND r.role = 'BOT')`,
	"members_active_window": `SELECT COUNT(*) FROM users u
		WHERE u.name <> '[deleted user]'
		  AND NOT EXISTS (SELECT 1 FROM user_roles r WHERE r.user_id = u.id AND r.role = 'BOT')
		  AND u.last_api_call >= now() - interval '30 days'`,
	"members_created_window": `SELECT COUNT(*) FROM users u
		WHERE u.name <> '[deleted user]'
		  AND NOT EXISTS (SELECT 1 FROM user_roles r WHERE r.user_id = u.id AND r.role = 'BOT')
		  AND u.created_at >= now() - interval '30 days'`,
	"members_invited": `SELECT COUNT(*) FROM users u
		WHERE u.name <> '[deleted user]'
		  AND NOT EXISTS (SELECT 1 FROM user_roles r WHERE r.user_id = u.id AND r.role = 'BOT')
		  AND u.invited_by IS NOT NULL`,
	"scenes_total":            `SELECT COUNT(*) FROM scenes s WHERE NOT s.deleted`,
	"scenes_created_window":   `SELECT COUNT(*) FROM scenes s WHERE NOT s.deleted AND s.created_at >= now() - interval '30 days'`,
	"scenes_with_fingerprint": `SELECT COUNT(*) FROM scenes s WHERE NOT s.deleted AND EXISTS (SELECT 1 FROM scene_fingerprints f WHERE f.scene_id = s.id)`,
	"scenes_with_sources": `SELECT COUNT(*) FROM scenes s WHERE NOT s.deleted
		AND EXISTS (SELECT 1 FROM content_sources cs WHERE cs.entity_type = 'scene' AND cs.entity_id = s.id)`,
	"fingerprints_total":                `SELECT COUNT(*) FROM scene_fingerprints`,
	"tags_total":                        `SELECT COUNT(*) FROM tags`,
	"tag_implications_total":            `SELECT COUNT(*) FROM tag_implications`,
	"sources_total":                     `SELECT COUNT(*) FROM content_sources`,
	"sources_checked_window":            `SELECT COUNT(*) FROM content_sources cs WHERE cs.last_checked >= now() - interval '30 days'`,
	"sources_unknown":                   `SELECT COUNT(*) FROM content_sources cs WHERE cs.status = 'unknown'`,
	"edits_pending":                     `SELECT COUNT(*) FROM edits e WHERE e.status = 'PENDING'`,
	"edits_decided_window":              `SELECT COUNT(*) FROM edits e WHERE e.closed_at IS NOT NULL AND e.closed_at >= now() - interval '30 days'`,
	"identification_candidates_pending": `SELECT COUNT(*) FROM identification_candidates`,
	"invites_issued":                    `SELECT COUNT(*) FROM invite_keys`,
	"invites_unused":                    `SELECT COUNT(*) FROM invite_keys k WHERE k.uses = 0`,
}

func TestInstanceMetricsServesEveryFigureWithItsDefinition(t *testing.T) {
	out, err := asAdmin(t).client.instanceMetrics()
	require.NoError(t, err, "an admin reading the instance metrics")

	assert.Equal(t, 30, out.InstanceMetrics.WindowDays,
		"the response must name the window its windowed figures are bound to")
	since, err := time.Parse(time.RFC3339, out.InstanceMetrics.Since)
	require.NoError(t, err, "since must be an RFC3339 stamp; a reader checks the figures against it")
	assert.WithinDuration(t, time.Now().UTC().AddDate(0, 0, -30), since, 5*time.Minute,
		"`since` is the start of the window the windowed figures are bound to")

	generatedAt, err := time.Parse(time.RFC3339, out.InstanceMetrics.GeneratedAt)
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now().UTC(), generatedAt, 5*time.Minute,
		"a snapshot whose age is unknown cannot be trusted about a moving instance")

	byKey := out.figures(t)
	assert.Len(t, byKey, len(metricExpectations),
		"every figure this instance publishes must be served, and each exactly once")

	for key, m := range byKey {
		assert.NotEmpty(t, m.Definition, "%s was served without the definition that decides what it counts", key)
		assert.True(t, len(m.Definition) > 30, "%s's definition is too short to decide anything: %q", key, m.Definition)
		assert.Equal(t, "count", m.Unit, "%s must say what its number IS", key)
		assert.GreaterOrEqual(t, m.Value, int64(0), "%s is a count of rows and cannot be negative", key)
	}

	// ARM 1: the relations that hold because all of these are one snapshot. Each is asserted against its
	// denominator rather than against zero, so that a figure stuck at 0 cannot satisfy them.
	assert.LessOrEqual(t, byKey["members_active_window"].Value, byKey["members_total"].Value,
		"more accounts were active in the window than exist")
	assert.LessOrEqual(t, byKey["members_created_window"].Value, byKey["members_total"].Value,
		"more accounts were created in the window than exist")
	assert.LessOrEqual(t, byKey["scenes_with_sources"].Value, byKey["scenes_total"].Value,
		"more scenes have sources than there are scenes")
	assert.LessOrEqual(t, byKey["scenes_with_fingerprint"].Value, byKey["scenes_total"].Value,
		"more scenes have fingerprints than there are scenes")
	assert.LessOrEqual(t, byKey["sources_checked_window"].Value, byKey["sources_total"].Value,
		"more sources were checked in the window than exist")
	assert.LessOrEqual(t, byKey["sources_unknown"].Value, byKey["sources_total"].Value,
		"more sources are unknown than exist")
	assert.LessOrEqual(t, byKey["invites_unused"].Value, byKey["invites_issued"].Value,
		"more invite keys are unused than were issued")
	assert.GreaterOrEqual(t, byKey["members_total"].Value, int64(1),
		"the admin making this request is an account; a member count of zero means the figure excludes everybody")
}

func TestEveryFigureAgreesWithTheCountWrittenHere(t *testing.T) {
	out, err := asAdmin(t).client.instanceMetrics()
	require.NoError(t, err)

	for key, m := range out.figures(t) {
		sql, ok := metricExpectations[key]
		require.True(t, ok,
			"the service publishes %q and this test has no count for it: a figure with no independent "+
				"recomputation is a number nobody has checked", key)

		assert.Equal(t, sqlCount(t, sql), m.Value,
			"%s does not agree with the table it claims to describe (%s)", key, sql)
	}
}

func TestInstanceMetricsIsRefusedToAMemberWithoutTheRole(t *testing.T) {
	_, err := asRead(t).client.instanceMetrics()
	require.Error(t, err, "a member without the admin role read the instance metrics")
	assert.Contains(t, err.Error(), "not authorized",
		"the refusal must be a refusal: a query answering with an empty figure list would satisfy a test that "+
			"only checked for no error, and would tell a member nothing about why")
}

func TestTheWindowIsAFilterRatherThanAWord(t *testing.T) {
	before, err := asAdmin(t).client.instanceMetrics()
	require.NoError(t, err)
	wasFigures := before.figures(t)

	aged := time.Now().UTC().AddDate(0, 0, -60)

	// A source the sweep last looked at OUTSIDE the window, and an account created OUTSIDE it. Both are
	// planted directly because the subject is the AGE of the row, and no verb in this application backdates a
	// check or an account.
	sourceURL := "https://outside-the-window-" + uuid.Must(uuid.NewV4()).String() + "/watch/1"
	_, err = dbtest.DB().Exec(t.Context(),
		`INSERT INTO content_sources (entity_type, entity_id, url, status, last_checked)
		 VALUES ('scene', $1, $2, 'live', $3)`,
		uuid.Must(uuid.NewV4()), sourceURL, aged)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = dbtest.DB().Exec(context.Background(), `DELETE FROM content_sources WHERE url = $1`, sourceURL)
	})

	oldUserID := uuid.Must(uuid.NewV4())
	oldUserName := "aged-outside-window-" + oldUserID.String()[:8]
	_, err = dbtest.DB().Exec(t.Context(),
		`INSERT INTO users (id, name, password_hash, email, api_key, last_api_call, created_at, updated_at)
		 VALUES ($1, $2, 'x', $3, $4, $5, $5, $5)`,
		oldUserID, oldUserName, oldUserName+"@example.invalid", oldUserName+"-key", aged)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = dbtest.DB().Exec(context.Background(), `DELETE FROM users WHERE id = $1`, oldUserID)
	})

	after, err := asAdmin(t).client.instanceMetrics()
	require.NoError(t, err)
	nowFigures := after.figures(t)

	delta := func(key string) int64 { return nowFigures[key].Value - wasFigures[key].Value }

	// The totals move by exactly the two rows planted.
	assert.Equal(t, int64(1), delta("sources_total"),
		"a source address was recorded and the source total did not move by one")
	assert.Equal(t, int64(1), delta("members_total"),
		"an account was created and the member total did not move by one")

	// And the windowed figures do not move at all, which is the whole point of planting them 60 days out.
	assert.Equal(t, int64(0), delta("sources_checked_window"),
		"a source last checked 60 days ago was counted as checked in the window: the window is a word, not a filter")
	assert.Equal(t, int64(0), delta("members_created_window"),
		"an account created 60 days ago was counted as created in the window")
	assert.Equal(t, int64(0), delta("members_active_window"),
		"an account whose last call was 60 days ago was counted as active in the window")

	// The status is not 'unknown', so the coverage figure -- which is a lifetime figure, not a windowed one --
	// must not move either. This is what stops 'unknown' from being read as 'not yet counted'.
	assert.Equal(t, int64(0), delta("sources_unknown"),
		"a source carrying the status 'live' was counted among the sources no check has ever looked at")

	assert.Equal(t, int64(0), delta("members_invited"),
		"an account created outside any invite was counted as having arrived through one")
}

// TestAPendingEditIsPendingRatherThanDecided plants the row that separates the two edit figures.
//
// "edits_pending" counts a STATUS and "edits_decided_window" counts a TIMESTAMP, and on a database with no
// edits at all they are both zero -- a pair of figures that cannot be told apart. The fixture is the smallest
// row this schema allows (operation, target_type, status and created_at are the only non-nullable columns an
// edit needs), and it is removed by VALUE when the test ends, because this package shares one database and a
// leftover edit would move a neighbour's count.
func TestAPendingEditIsPendingRatherThanDecided(t *testing.T) {
	before, err := asAdmin(t).client.instanceMetrics()
	require.NoError(t, err)
	wasFigures := before.figures(t)

	editID := uuid.Must(uuid.NewV4())
	_, err = dbtest.DB().Exec(t.Context(),
		`INSERT INTO edits (id, operation, target_type, status, created_at) VALUES ($1, 'CREATE', 'SCENE', 'PENDING', now())`,
		editID)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = dbtest.DB().Exec(context.Background(), `DELETE FROM edits WHERE id = $1`, editID)
	})

	after, err := asAdmin(t).client.instanceMetrics()
	require.NoError(t, err)
	nowFigures := after.figures(t)

	delta := func(key string) int64 { return nowFigures[key].Value - wasFigures[key].Value }

	assert.Equal(t, int64(1), delta("edits_pending"),
		"a pending edit was created and the pending figure did not move by one")
	assert.Equal(t, int64(0), delta("edits_decided_window"),
		"a pending edit is not a decided one: the figure that counts closed_at counted a row that is still open")
}

// TestTheCatalogueFiguresCountScenesAndFingerprintsSeparately plants the row that tells the catalogue figures
// apart, and it exists because the mutation ledger said so.
//
// THE LEDGER ROW "the fingerprint figure counts scenes instead of fingerprints" SURVIVED the first run of this
// suite: on the database the api package tests against, the two tables held the SAME count (nothing at all), so
// swapping one for the other changed no value and every assertion here was satisfied by a figure that counted
// the wrong table. That is the class of defect a recomputation test cannot see when its fixture has nothing in
// it, and the answer is a row: one scene carrying one fingerprint, after which the two figures differ by
// measure rather than by luck.
func TestTheCatalogueFiguresCountScenesAndFingerprintsSeparately(t *testing.T) {
	before, err := asAdmin(t).client.instanceMetrics()
	require.NoError(t, err)
	wasFigures := before.figures(t)

	sceneID := uuid.Must(uuid.NewV4())
	var owner uuid.UUID
	var fingerprintID int64
	require.NoError(t, dbtest.DB().QueryRow(t.Context(), `SELECT id FROM users LIMIT 1`).Scan(&owner))

	_, err = dbtest.DB().Exec(t.Context(),
		`INSERT INTO scenes (id, title, details, code, date, created_at, updated_at)
		 VALUES ($1, $2, 'A description of the record.', $3, DATE '2026-10-16', now(), now())`,
		sceneID, "metrics fixture "+sceneID.String()[:8], "MTR-"+sceneID.String()[:8])
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = dbtest.DB().Exec(context.Background(), `DELETE FROM scene_fingerprints WHERE scene_id = $1`, sceneID)
		_, _ = dbtest.DB().Exec(context.Background(), `DELETE FROM scenes WHERE id = $1`, sceneID)
	})

	// The hash is a BIGINT in this schema -- a perceptual hash, not a hex digest -- so the fixture uses a
	// random number rather than a UUID string. Measured, after the first attempt was refused with
	// "invalid input syntax for type bigint".
	hash := rand.Int63()
	require.NoError(t, dbtest.DB().QueryRow(t.Context(),
		`INSERT INTO fingerprints (algorithm, hash) VALUES ('md5', $1) RETURNING id`, hash).Scan(&fingerprintID))
	t.Cleanup(func() {
		_, _ = dbtest.DB().Exec(context.Background(), `DELETE FROM fingerprints WHERE id = $1`, fingerprintID)
	})

	_, err = dbtest.DB().Exec(t.Context(),
		`INSERT INTO scene_fingerprints (fingerprint_id, scene_id, user_id, duration) VALUES ($1, $2, $3, 60)`,
		fingerprintID, sceneID, owner)
	require.NoError(t, err)

	// A SECOND scene, with no fingerprint at all. This is the row that makes the two catalogue figures
	// separable: with one scene and one fingerprint the two counts are 1 and 1, and a figure that counted the
	// catalogue would satisfy every assertion written for the fingerprint figure -- which is exactly how this
	// test's first two versions let the mutation survive.
	bareSceneID := uuid.Must(uuid.NewV4())
	_, err = dbtest.DB().Exec(t.Context(),
		`INSERT INTO scenes (id, title, details, code, date, created_at, updated_at)
		 VALUES ($1, $2, 'A description of the record.', $3, DATE '2026-10-16', now(), now())`,
		bareSceneID, "metrics fixture bare "+bareSceneID.String()[:8], "MTRB-"+bareSceneID.String()[:8])
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = dbtest.DB().Exec(context.Background(), `DELETE FROM scenes WHERE id = $1`, bareSceneID)
	})

	after, err := asAdmin(t).client.instanceMetrics()
	require.NoError(t, err)
	nowFigures := after.figures(t)

	delta := func(key string) int64 { return nowFigures[key].Value - wasFigures[key].Value }

	assert.Equal(t, int64(2), delta("scenes_total"),
		"two scenes were created and the catalogue total did not move by two")
	assert.Equal(t, int64(2), delta("scenes_created_window"),
		"two scenes created just now are not inside the window")
	// The fingerprint figure moves by ONE while the catalogue moved by two: the two counts differ by measure,
	// which is the only way a test can tell a figure that counts fingerprints from one that counts scenes.
	assert.Equal(t, int64(1), delta("fingerprints_total"),
		"a fingerprint was recorded and the fingerprint figure did not move by one -- a figure that counts the "+
			"catalogue instead of the fingerprints is a figure that will read as large and mean nothing")
	assert.Equal(t, int64(1), delta("scenes_with_fingerprint"),
		"a scene was given a fingerprint and the coverage figure did not move")

	// The two catalogue figures are still different questions: this scene has a fingerprint and no source.
	assert.Equal(t, int64(0), delta("scenes_with_sources"),
		"a fingerprint is not a source address: the aggregation coverage must not move for one")
}

// TestTheTwoInviteFiguresMeasureDifferentThings plants an UNUSED invite key, which is the fixture that makes
// the pair meaningful rather than decorative.
//
// Two figures over one table, differing by a predicate, is exactly the shape that survives every test which
// only ever reads them: on a database where every key has been used (this instance's own live database is
// one), "invites_unused" and "invites_issued" are 0 and 21, and a mutation swapping the predicate to
// `uses > 0` produces 21 and 21 -- which no assertion on the values alone would notice, because both are
// legitimate-looking numbers. Planting an unused key separates them by measure.
func TestTheTwoInviteFiguresMeasureDifferentThings(t *testing.T) {
	before, err := asAdmin(t).client.instanceMetrics()
	require.NoError(t, err)
	wasFigures := before.figures(t)

	keyID := uuid.Must(uuid.NewV4())
	var generator uuid.UUID
	require.NoError(t, dbtest.DB().QueryRow(t.Context(), `SELECT id FROM users LIMIT 1`).Scan(&generator))

	_, err = dbtest.DB().Exec(t.Context(),
		`INSERT INTO invite_keys (id, generated_by, generated_at, uses) VALUES ($1, $2, now(), 0)`,
		keyID, generator)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = dbtest.DB().Exec(context.Background(), `DELETE FROM invite_keys WHERE id = $1`, keyID)
	})

	after, err := asAdmin(t).client.instanceMetrics()
	require.NoError(t, err)
	nowFigures := after.figures(t)

	delta := func(key string) int64 { return nowFigures[key].Value - wasFigures[key].Value }

	assert.Equal(t, int64(1), delta("invites_issued"),
		"an invite key was generated and the issued figure did not move by one")
	assert.Equal(t, int64(1), delta("invites_unused"),
		"an UNUSED invite key was generated and the unused figure did not move by one: a figure that counts "+
			"the same rows as invites_issued is not measuring anything")

	// And the same key does not move the member figures: generating a key is not somebody arriving.
	assert.Equal(t, int64(0), delta("members_total"), "generating an invite key created an account")
	assert.Equal(t, int64(0), delta("members_invited"), "generating an invite key was counted as an arrival")
}
