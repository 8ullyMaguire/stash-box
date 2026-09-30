package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stashapp/stash-box/internal/config"
)

// Regression test for a bug that destroyed a live deployment's config file.
//
// config.InitializeDefaults() ends in viper.WriteConfig(). Calling it BEFORE
// ReadInConfig() makes viper write its defaults over the real file: the DSN,
// the JWT key and the session key are all replaced with defaults. Nothing
// crashes, the process starts, and the service then runs against the WRONG
// database with regenerated session keys -- while the real values are gone from
// disk.
//
// This happened for real: the first version of sdbimport ordered the two calls
// the other way round and overwrote the thinkcentre config on its first run.
//
// The test asserts the ORDER, not the outcome, because asserting the outcome
// would pass for a binary that simply never writes config -- and the ordering
// is the thing that has to stay fixed if a future edit adds defaults back.

func TestReadInConfigPrecedesInitializeDefaults(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("reading main.go: %v", err)
	}
	body := string(src)

	// Find the CALL sites, not the prose. The comment above the call deliberately
	// names config.InitializeDefaults() to explain why it must not be called
	// here, and matching that mention would make this test assert on its own
	// documentation.
	read := indexOfCall(body, "viper.ReadInConfig()")
	defaults := indexOfCall(body, "config.InitializeDefaults()")
	if read < 0 || defaults < 0 {
		t.Skip("main.go no longer calls both; the ordering guard no longer applies")
	}
	if read > defaults {
		t.Errorf("viper.ReadInConfig() (offset %d) must come BEFORE "+
			"config.InitializeDefaults() (offset %d): InitializeDefaults calls "+
			"viper.WriteConfig() and will overwrite the config with defaults",
			read, defaults)
	}
}

// indexOfCall finds a call to fn, skipping occurrences inside a line comment.
func indexOfCall(src, fn string) int {
	for i := 0; i+len(fn) <= len(src); i++ {
		if src[i:i+len(fn)] != fn {
			continue
		}
		// Reject a match whose line begins with //.
		lineStart := strings.LastIndexByte(src[:i], '\n') + 1
		if strings.HasPrefix(strings.TrimSpace(src[lineStart:i]), "//") {
			continue
		}
		return i
	}
	return -1
}

// Behavioural version: writing defaults over an existing config must be
// detectable. Confirms the hazard is real rather than theoretical, so the
// ordering test above is guarding something.
func TestInitializeDefaultsWritesConfigFile(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yml")
	original := "database: user:secretpw@db.example:5432/mydb?sslmode=disable\n"
	if err := os.WriteFile(cfg, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	v := viper.New()
	v.SetConfigFile(cfg)
	if err := v.ReadInConfig(); err != nil {
		t.Fatal(err)
	}

	// This is the call sdbimport used to make first, and the reason the order
	// matters. InitializeDefaults operates on the package-level viper, which
	// has no config file loaded at this point, so its WriteConfig() targets
	// whatever SetConfigFile last named -- here, the real file.
	viper.Reset()
	viper.SetConfigFile(cfg)
	if err := config.InitializeDefaults(); err != nil {
		t.Fatal(err)
	}

	after, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after), "secretpw") {
		t.Errorf("InitializeDefaults() on an UNREAD viper preserved the DSN; "+
			"expected it to be overwritten with defaults, which is why the call "+
			"order in main.go matters. Config now: %q", string(after))
	} else {
		t.Logf("confirmed: defaults overwrote the DSN. Config now: %q", string(after))
	}
}
