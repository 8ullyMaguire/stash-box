package sdbimport

import "testing"

// The watermark key is derived from the endpoint rather than configured, so that two spellings of the same
// source are one source and two hosts are two. A key that is easy to get subtly wrong is worth testing on its
// own: getting it wrong means marks that are never read again.
func TestSourceKeyComesFromTheEndpointHost(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"https://stashdb.org/graphql", "stashdb.org", false},
		{"https://stashdb.org/graphql/", "stashdb.org", false},
		{"http://localhost:9999/graphql", "localhost:9999", false},
		{"https://stashdb.org", "stashdb.org", false},
		{"https://stashdb.org/graphql?a=1#b", "stashdb.org", false},
		{"", "", true},
		{"   ", "", true},
	}
	for _, c := range cases {
		got, err := SourceOf(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("SourceOf(%q) = %q, want an error: a watermark with no source is shared by every source",
					c.in, got)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("SourceOf(%q) = (%q, %v), want %q", c.in, got, err, c.want)
		}
	}
}

// The entity list is the sync's own contract: an entity that is not in it is refused rather than given a row,
// because a row for `performer` would be read by nothing while the entity walked in full on every run.
func TestTheSyncEntityListIsWhatTheRunnerWalks(t *testing.T) {
	want := []string{"tags", "sites", "studios", "performers", "scenes"}
	if len(SyncEntities) != len(want) {
		t.Fatalf("SyncEntities = %v, want %v", SyncEntities, want)
	}
	for i := range want {
		if SyncEntities[i] != want[i] {
			t.Errorf("SyncEntities[%d] = %q, want %q (the order matters: a scene resolves its performers)",
				i, SyncEntities[i], want[i])
		}
		if !IsSyncEntity(want[i]) {
			t.Errorf("IsSyncEntity(%q) = false for an entity in the list", want[i])
		}
	}
	for _, bad := range []string{"performer", "performers ", "Performers", "", "all"} {
		if IsSyncEntity(bad) {
			t.Errorf("IsSyncEntity(%q) = true: a near-miss entity name must be refused, not given its own row",
				bad)
		}
	}
}
