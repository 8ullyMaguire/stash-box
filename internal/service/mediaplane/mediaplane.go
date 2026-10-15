// Package mediaplane holds the media plane's one posture switch (plan phase 10).
//
// WHAT IS GATED. Hosting, the variant ladder, transcoding, the players, PWA offline caching and the
// CDN/edge -- every behaviour that serves BYTES to a reader rather than a claim about a work. This
// package is deliberately tiny and deliberately empty of capability: it exists so that the switch
// has ONE reader, so the refusal has ONE message, and so the first slice that actually serves
// something has somewhere to ask. A capability that arrives without asking here is the bug this
// package is written to make visible.
//
// WHY A PROGRAMME THAT REFUSED THIS SHIPS IT GATED INSTEAD. The media plane was struck on
// 2026-10-15 and restored the same day by owner instruction: "include it in the spec again but
// disable it by default in config". The refusal it replaced was right about the RISK and wrong
// about the remedy -- an instance that serves third-party material is a host, and a host is the
// category of site that is DMCA'd out of existence, so the danger was never that the code could
// exist. It was that the DEFAULT made every fork a host without its operator ever deciding. Hence
// a specified, planned, gated capability that every shipped config leaves off (config.ContentDelivery
// defaults false), and a switch whose message names itself so an operator who turned it on by
// accident can find the key from the log line alone.
//
// WHAT DOES NOT MOVE WITH THE SWITCH, named so a reader does not assume the switch is the whole
// guardrail:
//
//   - scripts/check-no-content-plane.py still fails the build for the third-party patterns (an
//     embed of somebody else's player, a proxy of somebody else's stream, byte-range serving of a
//     file this instance does not own), whatever this key says. Turning the switch on does not
//     license the tree to grow an iframe.
//   - content_item_media (migration 117) is not gated and needs no switch: a row there is a URL an
//     editor recorded and a kind, never bytes, exactly like content_sources. Reading it is
//     curation; serving what it points at is the media plane.
//   - The takedown verb (phase 14 slice 1) applies to the media plane the moment one exists: a
//     block is consulted at the address level, so served bytes would be refused by the same
//     content_source_block rows that refuse a fetch.
package mediaplane

import (
	"errors"

	"github.com/stashapp/stash-box/internal/config"
)

// ErrDeliveryDisabled is what a caller returns when this instance is not configured to serve
// content.
//
// The message names the key on purpose. The two readers it has are an operator, who needs to know
// which line of their configuration to change, and a member, who should be told that the instance
// does not deliver rather than that their request was malformed -- the difference between a
// server's posture and a bad request.
var ErrDeliveryDisabled = errors.New(
	"this instance does not serve content (content_delivery is off)")

// DeliveryEnabled reports whether this instance may serve content.
func DeliveryEnabled() bool {
	return config.GetContentDelivery()
}

// RequireDelivery returns ErrDeliveryDisabled unless the operator has enabled content delivery.
//
// It returns an error rather than a bool so it can be the FIRST statement of a function that serves
// bytes: an unchecked bool is invisible to the compiler, an unchecked error is not, and the guard
// then reads as the precondition it is. Mirrors content.RequireFetching, deliberately: the two
// switches are this programme's two postures, and a reader who has met one has met both.
func RequireDelivery() error {
	if !DeliveryEnabled() {
		return ErrDeliveryDisabled
	}
	return nil
}

// PostureLine reports how this process should describe its media-plane posture in the startup log.
//
// The pair is returned rather than logged here so that the line is assertable in a test without a
// logger and so that the process has exactly one place that decides the wording. Both variants name
// the key, because the log is where an operator finds out which posture they are in -- the same
// shape as the cron's content_fetch_sources line.
func PostureLine() string {
	if DeliveryEnabled() {
		return "content_delivery is ON: this instance may serve content -- hosting, the variant " +
			"ladder, transcoding and the players (plan phase 10). Serving third-party material " +
			"makes this instance a host; the operator has accepted that in writing."
	}
	return "content_delivery is off: this instance serves no bytes -- it holds claims about works " +
		"and the addresses of pages about them, and nothing it can hand a reader is a copy of a " +
		"work. See docs/spec/feature-content-aggregator.md, second respec (2026-10-15)."
}
