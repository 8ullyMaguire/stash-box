package federation

import (
	"fmt"
	"sort"
	"strings"
)

// THE MAPPING (phase 12, slice 12e): local tag names to CORE tag names.
//
// The wire never carries a local tag name. A local name is this instance's operator's word for something
// -- it is stable here and meaningless there, and sending it invites a peer to guess. What crosses is a
// CORE name: one of an agreed, small vocabulary that both instances know they share. This file is what
// turns the first into the second, and it is deliberately strict about the ways it can fail, because
// every one of them is a silent mis-mapping if it is not a refusal:
//
//   - a mapping that points at a name nobody agreed on ("my `big-breasts` means `chest-large`", where
//     `chest-large` is not a core name) would cross the boundary as if it were agreed, and a peer would
//     file the tag under a name whose meaning it believes it knows;
//   - a mapping that points at ANOTHER local tag is a chain, and following it means guessing which end
//     the operator meant;
//   - a tag with no mapping at all has no core name, and dropping it silently loses the fact that
//     something was left unsaid -- which is indistinguishable, at the far end, from "this instance has no
//     such tag".
//
// So the resolutions carry their refusal as a sentence, and there is no code path that invents a core
// name. The vocabulary itself is small and listed here rather than fetched: a governed document would
// publish it, and until one does, a list in the source is the honest place for it -- it is checkable, it
// is reviewable, and it cannot silently change under a running instance.

// CoreTagVocabularyV1 is the agreed core tag names, core ontology version 1.
//
// These are DESCRIPTIVE attributes of a scene or a performer -- the kind of thing two instances can agree
// about because the name states the whole rule. Nothing a person's identity rests on belongs here, and
// nothing whose meaning depends on this instance's own curation does either.
var CoreTagVocabularyV1 = []string{
	"eye-color",
	"gender",
	"hair-color",
	"height",
	"measurements",
	"piercings",
	"scene-type",
	"studio",
	"tattoos",
}

// CoreTagVocabulary returns the agreed names for a core version, ascending by version. Only v1 is
// published today; v2's additions are the phase's remaining work and are refused BY ABSENCE rather than
// guessed at, which is why this returns a list per version instead of one union.
func CoreTagVocabulary(coreVersion int) []string {
	switch coreVersion {
	case CoreOntologyV1:
		out := make([]string, len(CoreTagVocabularyV1))
		copy(out, CoreTagVocabularyV1)
		return out
	default:
		return nil
	}
}

// IsCoreTag says whether a name is in the agreed vocabulary.
func IsCoreTag(name string) bool {
	for _, t := range CoreTagVocabularyV1 {
		if t == name {
			return true
		}
	}
	return false
}

// TagResolution is what one local tag name resolves to, and why it did not when it did not.
//
// Exactly one of Core and Refusal is set. A struct with both fields and a rule about them is not a
// prettier error: the refusal is DATA that an operator reads on a page and a peer reads over the wire
// (GET /federation/tags), so it has to survive the trip as a value rather than as an err returned up a
// call stack that ends in a log line.
type TagResolution struct {
	// Local is the name as this instance spells it.
	Local string

	// Core is the agreed name it crosses as, or "" when it does not cross.
	Core string

	// Refusal says why it does not cross, naming the tag and the reason. Never a generic message: an
	// operator reading "unmapped" with no name learns nothing about which of their tags is unsaid.
	Refusal string
}

// Resolved reports whether this tag has a core name.
func (r TagResolution) Resolved() bool { return r.Core != "" }

// ResolveLocalTag resolves ONE local tag name against the operator's declared mappings.
//
// A tag that is ALREADY a core name resolves to itself, with no mapping needed: an instance that named
// its tags with the agreed vocabulary has nothing to declare, and demanding a mapping for
// `eye-color -> eye-color` would be asking an operator to write down what is already true.
func ResolveLocalTag(local string, mappings map[string]string) TagResolution {
	name := strings.TrimSpace(local)
	if name == "" {
		return TagResolution{Local: local, Refusal: "an empty tag name has nothing to resolve"}
	}

	if IsCoreTag(name) {
		return TagResolution{Local: name, Core: name}
	}

	core, declared := mappings[name]
	if !declared {
		// The refusal names the tag. "no mapping declared" alone would leave an operator to diff their
		// configuration against their tag list to find out which one this is about.
		return TagResolution{
			Local:   name,
			Refusal: fmt.Sprintf("no mapping is declared for %q, so this instance will not guess which core tag it means", name),
		}
	}

	target := strings.TrimSpace(core)
	switch {
	case target == "":
		return TagResolution{
			Local:   name,
			Refusal: fmt.Sprintf("%q maps to an empty name, which is not a core tag", name),
		}
	case target == name:
		// A self-mapping of a name that is NOT a core name: the operator has said "this means itself",
		// which is true here and meaningless on the wire. Refused rather than passed through, because
		// passing it through is precisely the leak of a local name this file exists to prevent.
		return TagResolution{
			Local:   name,
			Refusal: fmt.Sprintf("%q maps to itself, but %q is not a core v1 tag: a local name cannot cross as itself", name, name),
		}
	case IsCoreTag(target):
		return TagResolution{Local: name, Core: target}
	default:
		// Pointed at another LOCAL tag: a chain, and following it is guessing which end was meant.
		if _, isLocal := mappings[target]; isLocal {
			return TagResolution{
				Local:   name,
				Refusal: fmt.Sprintf("%q maps to %q, which is itself a local tag with a mapping: a chain is a guess about which end was meant", name, target),
			}
		}
		return TagResolution{
			Local:   name,
			Refusal: fmt.Sprintf("%q maps to %q, which is not a core v1 tag: an unagreed name would cross the boundary as if it were agreed", name, target),
		}
	}
}

// ResolveLocalTags resolves a set of names, ascending by local name so that two instances exchanging the
// same table produce the same document. Duplicates are resolved once: the answer is a property of the
// name, and a caller passing the same tag twice should not get two refusals for it.
func ResolveLocalTags(names []string, mappings map[string]string) []TagResolution {
	seen := map[string]bool{}
	out := make([]TagResolution, 0, len(names))
	for _, n := range names {
		key := strings.TrimSpace(n)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, ResolveLocalTag(n, mappings))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Local < out[j].Local })
	return out
}
