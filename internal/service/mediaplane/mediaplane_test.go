package mediaplane

import (
	"errors"
	"strings"
	"testing"

	"github.com/stashapp/stash-box/internal/config"
)

// The switch's SHIPPED state is the assertion that matters, and it is asserted here as well as in
// internal/config for one reason: this package is where the posture is READ, so a default that
// flipped would have to get past a test sitting next to the reader. `var C = &config{...}` is what an
// instance runs with when no config file names the key.
func TestDeliveryIsOffInTheShippedDefault(t *testing.T) {
	if DeliveryEnabled() {
		t.Fatal("content_delivery defaults to true: an instance whose operator never read a config " +
			"file would start serving content, which is the specific outcome the gated restoration " +
			"exists to prevent (plan phase 10, second respec 2026-10-15)")
	}
	if err := RequireDelivery(); !errors.Is(err, ErrDeliveryDisabled) {
		t.Fatalf("RequireDelivery() = %v with the switch off, want ErrDeliveryDisabled", err)
	}
}

// The gate follows the config rather than a constant. Both directions, because a gate that always
// refuses passes the test above and would make the capability unbuildable -- a refusal that cannot
// be lifted is not a switch.
func TestTheGateFollowsTheSwitchBothWays(t *testing.T) {
	previous := config.C.ContentDelivery
	t.Cleanup(func() { config.C.ContentDelivery = previous })

	config.C.ContentDelivery = true
	if !DeliveryEnabled() {
		t.Error("DeliveryEnabled() = false with content_delivery set: the gate reads something else")
	}
	if err := RequireDelivery(); err != nil {
		t.Errorf("RequireDelivery() = %v with the switch on, want nil", err)
	}

	config.C.ContentDelivery = false
	if DeliveryEnabled() {
		t.Error("DeliveryEnabled() = true with content_delivery clear")
	}
	if err := RequireDelivery(); !errors.Is(err, ErrDeliveryDisabled) {
		t.Errorf("RequireDelivery() = %v with the switch off, want ErrDeliveryDisabled", err)
	}
}

// BOTH posture lines name the KEY, and the two lines DIFFER. This is the test that stops the thing
// this package warns about: a switch written into a config file that no line of output ever
// mentions. Asserting they differ is what makes it a posture rather than a constant, and asserting
// the key name is what makes the log usable to an operator who has to find the line to change.
func TestBothPostureLinesNameTheKeyAndDiffer(t *testing.T) {
	previous := config.C.ContentDelivery
	t.Cleanup(func() { config.C.ContentDelivery = previous })

	config.C.ContentDelivery = false
	off := PostureLine()
	config.C.ContentDelivery = true
	on := PostureLine()

	if off == on {
		t.Fatal("PostureLine() returns the same text in both postures: the startup log cannot " +
			"distinguish an instance that serves content from one that does not")
	}
	for state, line := range map[string]string{"off": off, "on": on} {
		if !strings.Contains(line, "content_delivery") {
			t.Errorf("the %s posture line does not name the key: %q -- an operator reading the log "+
				"cannot find the setting that produced it", state, line)
		}
	}
	if !strings.Contains(off, "serves no bytes") {
		t.Errorf("the off line does not state what the posture MEANS: %q", off)
	}
	if !strings.Contains(on, "host") {
		t.Errorf("the on line does not state the consequence of the posture: %q -- the line is "+
			"the last place an operator is told what turning this on makes the instance", on)
	}
}
