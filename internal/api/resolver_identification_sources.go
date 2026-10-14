package api

import (
	"context"
	"fmt"

	"github.com/stashapp/stash-box/internal/config"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/identification"
)

// Third-party identification source resolvers (SPEC §8, plan step 7a).
//
// These render links. Nothing here makes a request, and there is no HTTP client
// in this file by construction: the point of shipping F8's "deep links first"
// half before any provider is that the instance's own traffic reveals nothing
// about which images its users are trying to identify. A reader's browser does
// the lookup, which is also what makes the F10 gate meaningful -- a disabled
// face service is one this server never offers and never calls.

// identificationSources builds the catalogue this instance offers: the built-in
// sources with the operator's config entries applied.
//
// Built per call rather than cached, because it is a handful of literals plus the
// config slice and a cache would be a second answer to "what is configured" --
// the failure mode being a config change that appears to do nothing until a
// restart. Same reasoning as the similar-vector factory's, inverted: there, the
// store was expensive and had to be shared; here it is free and must be fresh.
func identificationSources() []identification.Source {
	configured := config.C.IdentificationSources
	overrides := make([]identification.SourceOverride, 0, len(configured))
	for _, c := range configured {
		overrides = append(overrides, identification.SourceOverride{
			Name:     c.Name,
			Kind:     identification.SourceKind(c.Kind),
			Home:     c.Home,
			Template: c.Template,
			Enabled:  c.Enabled,
		})
	}
	return identification.ApplyOverrides(identification.DefaultSources(), overrides)
}

// identificationSourceKindEnum maps the service's kind onto the schema enum.
//
// Named and separate rather than a conversion, for the reason this repository
// keeps relearning: the two spellings differ (`reverse_media` against
// `REVERSE_MEDIA`) and a silent mismatch shows up as an enum value that does not
// exist. The default branch is an error naming the value, because an unknown kind
// here is a config typo and a source whose kind nobody recognises cannot be gated
// by F10.
func identificationSourceKindEnum(k identification.SourceKind) (models.IdentificationSourceKind, error) {
	switch k {
	case identification.KindReverseMedia:
		return models.IdentificationSourceKindReverseMedia, nil
	case identification.KindRemoteFace:
		return models.IdentificationSourceKindRemoteFace, nil
	case identification.KindCommunity:
		return models.IdentificationSourceKindCommunity, nil
	}
	return "", fmt.Errorf("unknown identification source kind %q", string(k))
}

// IdentificationSources resolves the instance's source catalogue, on and off.
func (r *queryResolver) IdentificationSources(_ context.Context) ([]models.IdentificationSource, error) {
	sources := identificationSources()
	out := make([]models.IdentificationSource, 0, len(sources))
	for _, s := range sources {
		kind, err := identificationSourceKindEnum(s.Kind)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.Name, err)
		}
		out = append(out, models.IdentificationSource{
			Name:    s.Name,
			Kind:    kind,
			Home:    s.Home,
			Enabled: s.Enabled,
		})
	}
	return out, nil
}

type identificationQueryResolver struct{ *Resolver }

// SourceLinks renders the offered sources for one image address.
func (r *identificationQueryResolver) SourceLinks(
	_ context.Context,
	_ *models.IdentificationQuery,
	imageURL string,
) ([]models.IdentificationSourceLink, error) {
	links, err := identification.Links(identificationSources(), imageURL)
	if err != nil {
		return nil, err
	}

	out := make([]models.IdentificationSourceLink, 0, len(links))
	for _, l := range links {
		kind, err := identificationSourceKindEnum(l.Kind)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", l.Source, err)
		}
		out = append(out, models.IdentificationSourceLink{
			Source:    l.Source,
			Kind:      kind,
			URL:       l.URL,
			Prefilled: l.Prefilled,
		})
	}
	return out, nil
}
