package config

import "testing"

// The content plane's switch is a legal posture and not a cost/behaviour trade-off, so its SHIPPED
// state is the assertion that matters: `var C = &config{...}` is the value an instance runs with when
// no config file names the key (the e2e config does not, and neither does any file in this repository),
// which makes "off unless an operator says otherwise" a property of the default rather than of a
// comment (respec, 2026-10-15).
func TestContentFetchingIsOffInTheShippedDefault(t *testing.T) {
	if C.ContentFetchSources {
		t.Fatal("content_fetch_sources defaults to true: an instance that has not been told to make " +
			"outbound requests to third-party content sources would start making them")
	}
}

// The accessor reads its OWN field. This is not a tautology in this file: the neighbouring
// conn_max_lifetime helper spent a while reading max_idle_conns, and a switch that read a different
// bool would be a switch that does not control what its name says.
func TestContentFetchingIsReadFromItsOwnField(t *testing.T) {
	previous := C.ContentFetchSources
	t.Cleanup(func() { C.ContentFetchSources = previous })

	C.ContentFetchSources = true
	if !GetContentFetchSources() {
		t.Error("GetContentFetchSources() = false with the field set: the accessor reads something else")
	}

	C.ContentFetchSources = false
	if GetContentFetchSources() {
		t.Error("GetContentFetchSources() = true with the field clear")
	}
}
