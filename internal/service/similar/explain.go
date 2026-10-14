package similar

import (
	"fmt"
	"sort"

	"github.com/gofrs/uuid"
)

// ReasonKind is what kind of thing two scenes have in common.
//
// A closed set, because the point of a reason is that a reader can check it. Three of the
// six are about tags and they are NOT interchangeable: the same tag on both scenes is a
// fact, a candidate carrying a KIND of the subject's tag is a claim in one direction, and a
// candidate carrying something the subject's tag implies is a claim in the other. Flattening
// them into "shared tag" would make the count disagree with the ontology -- which is the
// bug the tag explorer already had once (a facet count that did not match what clicking it
// returned).
type ReasonKind string

const (
	// ReasonSameTag: both scenes carry this tag.
	ReasonSameTag ReasonKind = "SAME_TAG"
	// ReasonNarrowerTag: the candidate carries a tag that is a kind of one of the
	// subject's tags (`cowgirl_position` for `woman_on_top`). A specific instance of a
	// claim the subject makes: strong, but a claim in one direction only, so it weighs
	// half of the same tag.
	ReasonNarrowerTag ReasonKind = "NARROWER_TAG"
	// ReasonBroaderTag: the candidate carries a tag one of the subject's tags implies
	// (the subject is tagged `cowgirl_position`, the candidate `woman_on_top`). A family
	// relative rather than an instance: the weakest tag evidence there is.
	ReasonBroaderTag ReasonKind = "BROADER_TAG"
	// ReasonSharedPerformer: the same person appears in both scenes.
	ReasonSharedPerformer ReasonKind = "SHARED_PERFORMER"
	// ReasonSameStudio: both scenes belong to the same studio. Weak on its own -- a
	// studio is a publisher, not a subject -- but it is what a reader would guess first,
	// so it is named rather than folded into a score.
	ReasonSameStudio ReasonKind = "SAME_STUDIO"
	// ReasonSimilarDuration: the two run for about the same length of time.
	ReasonSimilarDuration ReasonKind = "SIMILAR_DURATION"
)

// Weights, in one place, with why each number is what it is.
//
// These are a NAMED CONVENTION rather than a measurement: they say that the same tag on
// both scenes is a better reason than the same studio, and that knowing a scene contains
// the same person is the strongest single fact the archive records. Everything is exposed
// as evidence next to the score, so a reader who disagrees with the weights can still see
// the facts that produced the order.
const (
	// weightSameTag: the fact itself.
	weightSameTag = 3.0
	// weightNarrowerTag: the candidate is a specific instance of a subject claim.
	weightNarrowerTag = 1.5
	// weightBroaderTag: the candidate makes a more general claim than the subject.
	weightBroaderTag = 1.0
	// weightSharedPerformer: the highest evidence number in the whole set. A person in
	// common is a stronger statement than a tag in common: tags are typed by whoever
	// catalogued the scene, while a performer is who was actually there.
	weightSharedPerformer = 5.0
	// weightSameStudio: weak, and deliberately so. Studios publish hundreds of scenes.
	weightSameStudio = 2.0
	// weightSimilarDuration: the ceiling of the duration term, on the same scale as a
	// studio. A scene that runs the same length is not similar on its own -- it is a
	// tiebreak, and it must never outweigh a shared performer or even a shared tag.
	weightSimilarDuration = 2.0
	// durationScaleSeconds is the half-width of the duration term, in SECONDS (the unit
	// `scenes.duration` is in, and the unit the query language's `duration:60..600`
	// filter uses). 1800 seconds is half an hour. An hour-long difference scores nothing,
	// and a scene whose duration is unknown contributes nothing either: an unknown
	// duration is not evidence of a similar one.
	durationScaleSeconds = 1800
)

// Reason is one thing two scenes have in common, named.
type Reason struct {
	Kind ReasonKind
	// Label names the thing itself, in the words a reader would use: a tag name, a
	// performer's name, a studio's name. Never a number.
	Label string
	// Via names the subject's item that makes a TAG reason true, for the two ontology
	// kinds: the subject's tag that the candidate's tag is a kind of, or the subject's tag
	// that implies the candidate's. Empty for an exact tag match and for the non-tag kinds,
	// where there is nothing in between.
	Via string
	// Weight is this reason's contribution to the score, so that the score can be
	// reconstructed from the reasons a client already has.
	Weight float64
}

// evidence is everything the queries observed about one candidate, before scoring.
type evidence struct {
	// tags is the set of tag evidence rows, deduplicated to one explanation per tag.
	tags map[uuid.UUID]tagEvidence
	// performers is the set of shared performers, keyed by id.
	performers map[uuid.UUID]string
	// sameStudio is true when both scenes have the same non-null studio.
	sameStudio bool
	// studioName labels the studio reason.
	studioName string
	// durationDelta is the absolute difference in seconds, nil when either scene's
	// duration is unknown. A pointer rather than a zero because zero is a real delta:
	// two scenes of exactly the same length are the best case, not the absent one.
	durationDelta *int
}

type tagEvidence struct {
	tagID     uuid.UUID
	tagName   string
	rootName  string
	direction string
	depth     int32
}

// score turns evidence into reasons and a number in [0, ~1].
//
// THE DENOMINATOR IS WHAT THE SUBJECT OFFERS, not what the two scenes share: the score is
// "what fraction of everything this scene is, the candidate also is". The alternative --
// dividing by the shared count's own total, or by nothing at all -- lets a scene with forty
// tags outrank a genuine match against a scene with three, which is the same failure the
// performer similarity score documents: someone in 400 scenes shares something with
// everybody, and that is noise dressed as a recommendation.
//
// capacity counts only the axes where the SUBJECT has something to offer. A scene with no
// duration offers nothing for the duration term, so the term cannot be earned against it
// and it does not inflate the denominator; a scene with no tags anywhere in the subject
// leaves the tag axes at zero rather than dividing by a guess.
//
// The value can exceed 1.0 in one case, and that is not clamped: a candidate can carry many
// tags that are kinds of a single tag the subject has, so a narrow subject can be
// "exceeded" by a rich candidate. Clamping would collapse all of those onto 1.0 and lose
// the distinction between them; the field is for ordering, and the evidence beside it is
// what a client shows.
func score(ev evidence, subjectTags, subjectPerformers int, subjectHasStudio, subjectHasDuration bool) ([]Reason, float64) {
	reasons := make([]Reason, 0, len(ev.tags)+len(ev.performers)+2)

	for _, te := range sortedTagEvidence(ev.tags) {
		switch {
		case te.depth == 0:
			reasons = append(reasons, Reason{
				Kind:   ReasonSameTag,
				Label:  te.tagName,
				Weight: weightSameTag,
			})
		case te.direction == "down":
			reasons = append(reasons, Reason{
				Kind:   ReasonNarrowerTag,
				Label:  te.tagName,
				Via:    te.rootName,
				Weight: weightNarrowerTag,
			})
		default:
			reasons = append(reasons, Reason{
				Kind:   ReasonBroaderTag,
				Label:  te.tagName,
				Via:    te.rootName,
				Weight: weightBroaderTag,
			})
		}
	}

	for _, name := range sortedPerformerNames(ev.performers) {
		reasons = append(reasons, Reason{
			Kind:   ReasonSharedPerformer,
			Label:  name,
			Weight: weightSharedPerformer,
		})
	}

	if ev.sameStudio {
		reasons = append(reasons, Reason{
			Kind:   ReasonSameStudio,
			Label:  ev.studioName,
			Weight: weightSameStudio,
		})
	}

	if ev.durationDelta != nil {
		// Linear decay to zero at the half-width: two scenes of exactly the same length
		// earn the whole term, and the term is zero at half an hour apart or more. A
		// linear falloff rather than a step because a 5-minute difference and a 4-minute
		// one should not score the same, and a zero-order step would make that true.
		proximity := 1.0 - float64(*ev.durationDelta)/float64(durationScaleSeconds)
		if proximity < 0 {
			proximity = 0
		}
		if proximity > 0 {
			reasons = append(reasons, Reason{
				Kind:   ReasonSimilarDuration,
				Label:  fmt.Sprintf("%d seconds apart", *ev.durationDelta),
				Weight: weightSimilarDuration * proximity,
			})
		}
	}

	earned := 0.0
	for _, r := range reasons {
		earned += r.Weight
	}

	capacity := weightSameTag*float64(subjectTags) + weightSharedPerformer*float64(subjectPerformers)
	if subjectHasStudio {
		capacity += weightSameStudio
	}
	if subjectHasDuration {
		capacity += weightSimilarDuration
	}
	if capacity == 0 {
		// Unreachable through FindSimilarScenes -- the candidate predicate requires a
		// shared axis, and a scene with no tags, no performers, no studio and no duration
		// has no axis to share -- but a division by zero here would be a panic in a read
		// path, so it is decided rather than assumed away.
		return reasons, 0
	}

	return reasons, earned / capacity
}

// sortedTagEvidence orders the tag reasons deterministically: strongest kind first, then by
// name. The query already orders its rows, but a map iteration is what the service actually
// walks, and an unordered reason list is a client diffing two identical responses.
func sortedTagEvidence(tags map[uuid.UUID]tagEvidence) []tagEvidence {
	out := make([]tagEvidence, 0, len(tags))
	for _, te := range tags {
		out = append(out, te)
	}
	sort.Slice(out, func(i, j int) bool {
		ri, rj := tagRank(out[i]), tagRank(out[j])
		if ri != rj {
			return ri < rj
		}
		return out[i].tagName < out[j].tagName
	})
	return out
}

// tagRank orders the three tag kinds by strength: exact, then an instance, then a relative.
func tagRank(te tagEvidence) int {
	switch {
	case te.depth == 0:
		return 0
	case te.direction == "down":
		return 1
	default:
		return 2
	}
}

func sortedPerformerNames(performers map[uuid.UUID]string) []string {
	out := make([]string, 0, len(performers))
	for _, name := range performers {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
