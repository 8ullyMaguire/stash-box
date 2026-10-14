package models

// TagFacet is one tag's presence in a result set: how much of the set carries it, and how much
// of the instance does.
//
// It is a DISPLAY figure and never a filter. `strength` is computed against this instance's
// corpus, so two instances holding different scenes give the same query different numbers --
// fine for "what else is in here", wrong for anything that decides what a reader sees.
type TagFacet struct {
	Tag Tag `json:"tag"`

	// Result scenes that carry this tag OR anything implying it: exactly what filtering by it
	// returns (internal/service/scene/facets.go).
	Count int `json:"count"`

	// Count divided by every scene in the instance that a filter on this tag would return.
	// 1.0 means the whole corpus is in the result set for this tag.
	Strength float64 `json:"strength"`
}

// TagFacets is the facet list for a query, most-used first.
type TagFacets struct {
	Tags []TagFacet `json:"tags"`

	// How many of the result scenes carry at least one tag. Not the sum of the counts above:
	// a scene carrying two tags is in two of them.
	TaggedCount int `json:"tagged_count"`
}
