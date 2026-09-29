package fingerprint

import (
	"slices"
	"testing"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
)

// loadOshashLinks, not buildMember.
//
// Issue #1177 ("Fingerprint cluster view hash list population error") led here,
// but the defect is not the one the report describes -- see the note on the
// dedup test below. What the investigation turned up is a link that is silently
// DROPPED, which is a different and more serious version of the same symptom
// area: a phash that co-submitted an oshash with another phash does not get
// that oshash in its linked list at all.
//
// The bug is the seen guard in loadOshashLinks:
//
//	for _, row := range rows {
//	    if _, seen := out.hashesByID[row.OshashFingerprintID]; seen {
//	        continue
//	    }
//	    out.hashesByID[row.OshashFingerprintID] = row.OshashHash
//	    out.allIDs = append(out.allIDs, row.OshashFingerprintID)
//	    out.byPhash[row.PhashFingerprintID] = append(out.byPhash[row.PhashFingerprintID], row.OshashFingerprintID)
//	}
//
// The guard is keyed on the OSHASH id alone, but hashesByID and allIDs are
// per-oshash (correctly -- an oshash has one hash and needs one submission
// fetch), whereas byPhash is a per-(phash, oshash) index. So the first phash to
// claim an oshash wins, and every later phash linked to the same oshash is
// dropped by that same `continue`, which skips the byPhash append too.
//
// This is not hypothetical. Against the integration database:
//
//   SELECT oshash_fp, count(*), array_agg(phash_fp) ...
//   18 | 2 | {18,19}
//   19 | 2 | {18,19}
//   21 | 2 | {21,22}
//   24 | 2 | {24,25}
//
// One oshash linked to two phashes is the NORMAL case, not an edge case: the
// link is "these two fingerprints were co-submitted on one scene by one user
// within 60 seconds", and a scene commonly has several phashes.
//
// The consequence is not cosmetic. LinkedFingerprints drives the move and delete
// mutation rows (buildMoveSources in the frontend), and an oshash missing from
// the list is left behind on the source scene when the phash is moved, and is
// not deleted with it.

// A single oshash linked to two phashes must appear in BOTH phashes' lists.
// The `seen` guard keeps it in only the first.
func TestLoadOshashLinksKeepsOshashForEveryLinkedPhash(t *testing.T) {
	const (
		phash1 = 1
		phash2 = 2
		oshash = 3
	)

	// The shape LoadLinkedOshashSubmissions returns for one oshash shared by two
	// phashes, which the query's SELECT DISTINCT does NOT collapse: DISTINCT
	// covers (oshash_fp, phash_fp, oshash_hash) and the two rows differ in
	// phash_fp.
	rows := []queries.LoadLinkedOshashSubmissionsRow{
		{OshashFingerprintID: oshash, PhashFingerprintID: phash1, OshashHash: 0xBEEF},
		{OshashFingerprintID: oshash, PhashFingerprintID: phash2, OshashHash: 0xBEEF},
	}

	links := buildOshashLinks(rows)

	for _, phash := range []int{phash1, phash2} {
		if !slices.Contains(links.byPhash[phash], oshash) {
			t.Errorf("phash %d is missing oshash %d: an oshash linked to several "+
				"phashes must be kept for each of them (#1177)", phash, oshash)
		}
	}

	// The per-oshash data must still be recorded exactly once, or the hash
	// lookup and the submission fetch would be duplicated.
	if got := len(links.hashesByID); got != 1 {
		t.Errorf("hashesByID has %d entries, want 1: the hash and submission "+
			"lookups are per-oshash and must not be repeated", got)
	}
	if got := links.hashesByID[oshash]; got != 0xBEEF {
		t.Errorf("hashesByID[%d] = %#x, want 0xBEEF", oshash, got)
	}
	if got := len(links.allIDs); got != 1 {
		t.Errorf("allIDs has %d entries, want 1: an oshash is fetched once", got)
	}
}

// An oshash linked to several phashes, alongside unrelated links, so the fix
// cannot be "keep only the first row" or "keep one phash's list".
func TestLoadOshashLinksKeepsSharedOshashAlongsideOthers(t *testing.T) {
	const (
		phash1 = 1
		phash2 = 2
		phash3 = 3
		shared = 10
		only1  = 11
		only2  = 12
	)

	rows := []queries.LoadLinkedOshashSubmissionsRow{
		{OshashFingerprintID: shared, PhashFingerprintID: phash1, OshashHash: 0xAAA},
		{OshashFingerprintID: only1, PhashFingerprintID: phash1, OshashHash: 0xBBB},
		{OshashFingerprintID: shared, PhashFingerprintID: phash2, OshashHash: 0xAAA},
		{OshashFingerprintID: only2, PhashFingerprintID: phash2, OshashHash: 0xCCC},
		{OshashFingerprintID: shared, PhashFingerprintID: phash3, OshashHash: 0xAAA},
	}

	links := buildOshashLinks(rows)

	want := map[int][]int{
		phash1: {shared, only1},
		phash2: {shared, only2},
		phash3: {shared},
	}
	for phash, wantIDs := range want {
		got := links.byPhash[phash]
		if len(got) != len(wantIDs) {
			t.Errorf("phash %d: byPhash = %v, want %v (#1177)", phash, got, wantIDs)
			continue
		}
		for _, id := range wantIDs {
			if !slices.Contains(got, id) {
				t.Errorf("phash %d: byPhash = %v, missing oshash %d (#1177)", phash, got, id)
			}
		}
	}

	// three distinct oshashes => three hash lookups, one submission fetch each.
	if got := len(links.hashesByID); got != 3 {
		t.Errorf("hashesByID has %d entries, want 3", got)
	}
	if got := len(links.allIDs); got != 3 {
		t.Errorf("allIDs has %d entries, want 3", got)
	}
}

// The counter-test: the dedup the `seen` guard was there to provide must survive.
// Repeating the SAME (phash, oshash) pair -- which SELECT DISTINCT does collapse,
// so the query cannot produce it -- must not double-list. Without this, the
// obvious "just delete the guard" fix passes both tests above while reintroducing
// a repeat, and the change would be a trade rather than a fix.
func TestLoadOshashLinksDoesNotRepeatSamePair(t *testing.T) {
	const (
		phash  = 1
		oshash = 5
	)

	rows := []queries.LoadLinkedOshashSubmissionsRow{
		{OshashFingerprintID: oshash, PhashFingerprintID: phash, OshashHash: 0xDDD},
		{OshashFingerprintID: oshash, PhashFingerprintID: phash, OshashHash: 0xDDD},
	}

	links := buildOshashLinks(rows)

	if got := len(links.byPhash[phash]); got != 1 {
		t.Errorf("byPhash[phash] has %d entries, want 1: the same (phash, oshash) "+
			"pair must not be listed twice (#1177)", got)
	}
	if got := len(links.allIDs); got != 1 {
		t.Errorf("allIDs has %d entries, want 1", got)
	}
}

// buildMember must then attach the shared oshash to every phash that links it,
// on every scene it appears on. This is the downstream effect the fix restores:
// buildMember reads oshashByPhash[id], so a dropped link means a missing
// linked_fingerprint, which means the oshash is not moved or deleted with its
// phash.
func TestBuildMemberAttachesSharedOshashToEachPhash(t *testing.T) {
	const (
		phash1    = 1
		phash2    = 2
		oshashID  = 3
		oshashHsh = models.FingerprintHash(0xAAA)
	)
	sceneA := uuid.FromStringOrNil("019e7850-00e3-719a-b3b0-7dba03d43d43")

	hashByID := map[int]models.FingerprintHash{
		phash1:   models.FingerprintHash(0x1111),
		phash2:   models.FingerprintHash(0x2222),
		oshashID: oshashHsh,
	}
	// Both phashes link the same oshash -- what buildOshashLinks now produces.
	oshashByPhash := map[int][]int{phash1: {oshashID}, phash2: {oshashID}}
	subsByMember := map[int][]queries.LoadClusterSubmissionsRow{
		phash1:   {{FingerprintID: phash1, SceneID: sceneA, Submissions: 1}},
		phash2:   {{FingerprintID: phash2, SceneID: sceneA, Submissions: 1}},
		oshashID: {{FingerprintID: oshashID, SceneID: sceneA, Submissions: 1}},
	}

	for _, phash := range []int{phash1, phash2} {
		member := buildMember(phash, hashByID, subsByMember, oshashByPhash)
		found := false
		for _, ss := range member.SceneSubmissions {
			for _, o := range ss.LinkedFingerprints {
				if o.Hash == oshashHsh {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("phash %d does not list oshash %x; a shared oshash must be "+
				"attached to every phash that links it (#1177)", phash, oshashHsh)
		}
	}
}
