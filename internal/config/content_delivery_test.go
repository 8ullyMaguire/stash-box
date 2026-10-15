package config

import "testing"

// content_delivery is a legal posture and not a cost/behaviour trade-off, so its SHIPPED state is
// the assertion that matters: `var C = &config{...}` is the value an instance runs with when no
// config file names the key, which makes "off unless an operator says otherwise" a property of the
// default rather than of a comment (plan phase 10, second respec 2026-10-15).
//
// This is the test that would have caught the shape the media plane was struck for: a capability
// whose default made every fork a host without its operator deciding anything.
func TestContentDeliveryIsOffInTheShippedDefault(t *testing.T) {
	if C.ContentDelivery {
		t.Fatal("content_delivery defaults to true: an instance that was never told to serve " +
			"content would serve it, which is the exact default the gated restoration exists to " +
			"keep off")
	}
}

// The accessor reads its OWN field. Not a tautology in this file: the neighbouring
// conn_max_lifetime helper spent a while reading max_idle_conns, and a switch that read a different
// bool would be a switch that does not control what its name says.
func TestContentDeliveryIsReadFromItsOwnField(t *testing.T) {
	previous := C.ContentDelivery
	t.Cleanup(func() { C.ContentDelivery = previous })

	C.ContentDelivery = true
	if !GetContentDelivery() {
		t.Error("GetContentDelivery() = false with the field set: the accessor reads something else")
	}

	C.ContentDelivery = false
	if GetContentDelivery() {
		t.Error("GetContentDelivery() = true with the field clear")
	}
}

// The two switches are SEPARATE keys, and this is asserted rather than assumed because the whole
// point of the pair is that an operator can fetch metadata without serving anything: fetching
// somebody's page to read a title is a scraper question, serving a work is a host question, and a
// single switch would force each instance to answer both at once.
func TestTheTwoSwitchesAreIndependent(t *testing.T) {
	previousDelivery, previousFetch := C.ContentDelivery, C.ContentFetchSources
	t.Cleanup(func() {
		C.ContentDelivery, C.ContentFetchSources = previousDelivery, previousFetch
	})

	C.ContentDelivery, C.ContentFetchSources = false, true
	if GetContentDelivery() {
		t.Error("content_delivery reads content_fetch_sources: an instance configured to FETCH " +
			"would be serving content it was never told to serve")
	}

	C.ContentDelivery, C.ContentFetchSources = true, false
	if GetContentFetchSources() {
		t.Error("content_fetch_sources reads content_delivery")
	}
}
