package ingest

import (
	"fmt"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
)

func TestDecide(t *testing.T) {
	sceneA := uuid.Must(uuid.NewV7())
	sceneB := uuid.Must(uuid.NewV7())

	cases := []struct {
		name  string
		known Known
		want  Decision
	}{
		{
			"nothing known: create",
			Known{},
			DecisionCreate,
		},
		{
			"the URL is already claimed: attach",
			Known{EntityIDs: []uuid.UUID{sceneA}},
			DecisionAttach,
		},
		{
			"the fingerprint matches: merge",
			Known{FingerprintMatch: &sceneB},
			DecisionMerge,
		},
		{
			// A person typed "this URL is a source for scene A". A machine thinks the same
			// content looks like scene B. The person wins, and merging into B would overrule
			// them silently -- which is why this rule sits above the fingerprint match.
			"a named target beats a fingerprint match",
			Known{EntityID: &sceneA, FingerprintMatch: &sceneB},
			DecisionAttach,
		},
		{
			// The precedence that matters. Both are known, and an existing claim is the
			// answer: a fingerprint lookup could only move the source to a second entity,
			// which is a second decision about a question that is already settled.
			"both known: an existing claim beats a fingerprint match",
			Known{EntityIDs: []uuid.UUID{sceneA}, FingerprintMatch: &sceneB},
			DecisionAttach,
		},
		{
			"denied, though the URL is claimed: reject",
			Known{Denied: true, DenyReason: "host on the deny list", EntityIDs: []uuid.UUID{sceneA}},
			DecisionReject,
		},
		{
			"denied, though the fingerprint matches: reject",
			Known{Denied: true, DenyReason: "DMCA", FingerprintMatch: &sceneB},
			DecisionReject,
		},
		{
			"denied with nothing else known: reject",
			Known{Denied: true, DenyReason: "host on the deny list"},
			DecisionReject,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, Decide(c.known))
		})
	}
}

func TestKnownTarget(t *testing.T) {
	sceneA := uuid.Must(uuid.NewV7())
	sceneB := uuid.Must(uuid.NewV7())

	cases := []struct {
		name  string
		known Known
		want  *uuid.UUID
	}{
		{
			"a submission that named a scene lands on that scene, even when the URL is claimed elsewhere",
			Known{EntityID: &sceneB, EntityIDs: []uuid.UUID{sceneA}},
			&sceneB,
		},
		{
			"a bare URL lands on the entity that claims it",
			Known{EntityIDs: []uuid.UUID{sceneA, sceneB}},
			&sceneA,
		},
		{
			"an unclaimed URL lands on the fingerprint match",
			Known{FingerprintMatch: &sceneB},
			&sceneB,
		},
		{
			"a denial has no target, and a target would be an instruction to fetch it",
			Known{Denied: true, EntityID: &sceneB, FingerprintMatch: &sceneA},
			nil,
		},
		{
			"nothing known: no target",
			Known{},
			nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.known.Target()
			if c.want == nil {
				assert.Nil(t, got)
				return
			}
			assert.Equal(t, *c.want, *got)
		})
	}
}

func TestBackoffSeconds(t *testing.T) {
	cases := []struct {
		attempts int
		want     int
	}{
		{0, 60},   // never claimed: the claim itself is attempt 1
		{1, 60},   // first failure
		{2, 120},  // doubling
		{3, 240},  //
		{4, 480},  //
		{7, 3840}, //
		{9, 15360},
		{20, 21600}, // clamped to six hours
		{100, 21600},
	}

	for _, c := range cases {
		t.Run(fmt.Sprintf("%d_attempts", c.attempts), func(t *testing.T) {
			got := BackoffSeconds(c.attempts)

			// A ceiling is the point of the clamp: an unbounded exponential makes retrying
			// a site that came back after a day a wait of years, which is the same as
			// giving up while looking like patience.
			assert.LessOrEqual(t, got, 6*60*60,
				"backoff must be clamped; an unclamped doubling reaches years")
			assert.Equal(t, c.want, got)
		})
	}
}
