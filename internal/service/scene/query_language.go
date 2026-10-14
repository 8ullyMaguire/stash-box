package scene

import (
	"context"
	"errors"
	"strings"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/search"
)

// nameResolver is the ONE place a search-language name becomes an id, and therefore the one
// place where "what counts as a tag name" is decided. The language itself never touches a
// database (internal/service/search), which is what keeps its rules testable with no fixtures.
type nameResolver struct {
	queries *queries.Queries
}

func (r nameResolver) TagID(ctx context.Context, name string) (uuid.UUID, bool, error) {
	// The live lookup first: name or alias, in one query, for the case that needs no history.
	tag, err := r.queries.FindTagByNameOrAlias(ctx, name)
	switch {
	case err == nil:
		return tag.ID, true, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return uuid.Nil, false, err
	}

	// Nothing live carries that name. A MERGE is the case that lands here: it soft-deletes the
	// source tag and writes a redirect to the survivor, without writing the old name as an alias
	// (internal/service/edit/tag.go, mergeInto). So the name is reachable only through the
	// soft-deleted row, and following its redirect is what keeps a query written before a merge --
	// a saved search, or a reader's memory of what the tag was called -- meaning what it meant.
	// Without this the term reports "unknown tag" and the query empties, silently.
	merged, err := r.queries.FindTagByNameOrAliasUnfiltered(ctx, name)
	if err != nil {
		return notFound(err)
	}

	targets, err := r.queries.FindTagWithRedirect(ctx, merged.ID)
	if err != nil {
		return notFound(err)
	}
	// The redirect arm is the one that matters here; the first arm of that query needs the row
	// to be live, and this one is not. No rows means a deleted tag nobody redirected, which is
	// genuinely gone.
	for _, target := range targets {
		return target.ID, true, nil
	}

	return uuid.Nil, false, nil
}

func (r nameResolver) PerformerID(ctx context.Context, name string) (uuid.UUID, bool, error) {
	performer, err := r.queries.FindPerformerByName(ctx, name)
	if err != nil {
		return notFound(err)
	}

	return performer.ID, true, nil
}

func (r nameResolver) StudioID(ctx context.Context, name string) (uuid.UUID, bool, error) {
	studio, err := r.queries.FindStudioByName(ctx, name)
	if err != nil {
		return notFound(err)
	}

	return studio.ID, true, nil
}

// notFound is the third state every lookup has: not an id, not an error, but "no such name".
// Collapsing it into an error would make an unknown tag look like a database fault, and
// collapsing it into uuid.Nil would make it look like a tag that exists.
func notFound(err error) (uuid.UUID, bool, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}

	return uuid.Nil, false, err
}

// LanguageDiagnostics is what a query asked for that the filter could not apply, for a client to
// show beside the result. It re-lowers: the work is a parse and one lookup per named term, and it
// is the SAME code path the query itself took, so a diagnostic cannot drift from the answer it
// explains. Both callers are cheap enough that a shared cache would be the wrong trade.
func (s *Scene) LanguageDiagnostics(ctx context.Context, input models.SceneQueryInput) ([]search.Diagnostic, error) {
	_, diagnostics, _, err := s.lowerQueryLanguage(ctx, input)

	return diagnostics, err
}

// lowerQueryLanguage merges input.Query -- the search language -- into the filter the query
// builder already understands. `noMatch` says the query as written has no answers: a term could
// not be applied, and answering with that term dropped would be a LARGER answer to a smaller
// question, presented as if it were the answer.
//
// A parse failure is a diagnostic and not an error, for the same reason: the client has to be
// able to show which term is wrong and where, and a 500 says none of that.
func (s *Scene) lowerQueryLanguage(ctx context.Context, input models.SceneQueryInput) (models.SceneQueryInput, []search.Diagnostic, bool, error) {
	if input.Query == nil || strings.TrimSpace(*input.Query) == "" {
		return input, nil, false, nil
	}

	expr, err := search.Parse(*input.Query)
	if err != nil {
		var parseErr *search.ParseError
		if errors.As(err, &parseErr) {
			return input, []search.Diagnostic{search.DiagnosticFromParseError(parseErr)}, true, nil
		}

		return input, nil, false, err
	}

	lowered, err := search.Lower(ctx, expr, input, nameResolver{queries: s.queries})
	if err != nil {
		return input, nil, false, err
	}

	filter := lowered.Filter
	// The language has been applied. Leaving it on the filter would apply it a second time to
	// whatever reads this value next -- and the terms are not idempotent: two `text:` merges
	// would double the phrase.
	filter.Query = nil

	return filter, lowered.Diagnostics, lowered.NoMatch, nil
}
