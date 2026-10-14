//go:build integration

package api_test

import (
	"encoding/json"
	"net/url"
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/config"
	"github.com/stashapp/stash-box/internal/service/identification"
)

// The third-party source links through GraphQL (SPEC §8, plan step 7a).
//
// What only this layer can catch: the field being bound to its struct field so
// the caller's image address is parsed and then discarded (the arguments never
// reach the query, so every link renders without the address), a role gate that
// leaks, and F10's default being enforced somewhere other than where a reader can
// see it. All three compile.

type identSourceLinksResponse struct {
	IdentificationQuery struct {
		ID          string
		SourceLinks []struct {
			Source    string
			Kind      string
			URL       string
			Prefilled bool
		}
	}
}

type identSourcesResponse struct {
	IdentificationSources []struct {
		Name    string
		Kind    string
		Home    string
		Enabled bool
	}
}

// The image address is nastier than it needs to be on purpose: a second query
// parameter is what a naive concatenation loses, and the loss is silent (the
// engine simply does not know the image).
const sourceLinkImage = "https://cdn.example.com:8443/cover.jpg?token=abc&size=full"

func sourceLinksFor(t *testing.T, admin *testRunner, queryID, imageURL string) identSourceLinksResponse {
	t.Helper()
	var resp identSourceLinksResponse
	admin.client.MustPost(`
		query Links($id: ID!, $img: String!) {
			identificationQuery(id: $id) {
				id
				sourceLinks(imageUrl: $img) { source kind url prefilled }
			}
		}
	`, &resp, client.Var("id", queryID), client.Var("img", imageURL))
	return resp
}

func TestIdentificationSourceLinks(t *testing.T) {
	admin := asAdmin(t)
	query := postQuery(t, identification.TargetScene, "source links case", uuid.NullUUID{})

	t.Run("the address the caller passed is the address the source receives", func(t *testing.T) {
		resp := sourceLinksFor(t, admin, query.ID.String(), sourceLinkImage)
		links := resp.IdentificationQuery.SourceLinks
		require.NotEmpty(t, links, "the no-network half needs no configuration, so it is on by default")

		// Count the subject before asserting any absence: a list that came back
		// empty would make every "is not there" assertion below vacuously true.
		var saucenao *struct {
			Source    string
			Kind      string
			URL       string
			Prefilled bool
		}
		for i := range links {
			if links[i].Source == "SauceNAO" {
				saucenao = &links[i]
			}
		}
		require.NotNil(t, saucenao, "SauceNAO is in the spec's catalogue: %v", links)

		// Read the link the way the ENGINE will. The raw string would pass for a
		// link that merely looks different; parsing it fails unless the whole
		// address, second parameter included, arrived.
		u, err := url.Parse(saucenao.URL)
		require.NoError(t, err)
		assert.Equal(t, "saucenao.com", u.Host)
		assert.Equal(t, sourceLinkImage, u.Query().Get("url"))
		assert.True(t, saucenao.Prefilled)
	})

	t.Run("no face service is offered by default (F10)", func(t *testing.T) {
		var resp identSourcesResponse
		admin.client.MustPost(`
			query { identificationSources { name kind home enabled } }
		`, &resp)

		sources := resp.IdentificationSources
		require.NotEmpty(t, sources)

		var faceSources, enabledFaces int
		for _, s := range sources {
			if s.Kind != "REMOTE_FACE" {
				continue
			}
			faceSources++
			if s.Enabled {
				enabledFaces++
				t.Logf("enabled face source: %s", s.Name)
			}
		}
		// The subject first: "no face source is enabled" is only a claim about
		// the gate if face sources are in the list at all. Otherwise a catalogue
		// that simply has none would pass this and mean nothing.
		assert.NotZero(t, faceSources,
			"face services are OFFERABLE and off -- absent would be a different bug (or a different feature)")
		assert.Zero(t, enabledFaces,
			"a biometric query is the operator's decision, not a default (F10)")

		links := sourceLinksFor(t, admin, query.ID.String(), sourceLinkImage).IdentificationQuery.SourceLinks
		require.NotEmpty(t, links)
		for _, l := range links {
			assert.NotEqual(t, "REMOTE_FACE", l.Kind, "%s: a disabled source must not be rendered", l.Source)
		}
	})

	t.Run("the operator's config is read per call, and opens the gate", func(t *testing.T) {
		// The resolver builds the catalogue from config on every call rather than
		// caching it, so a config change does not need a restart to be seen. This
		// is the test of that property as much as of the gate: if the catalogue
		// were cached, this case would still show the previous answer.
		enabled := true
		prev := config.C.IdentificationSources
		config.C.IdentificationSources = []config.IdentificationSourceConfig{
			{Name: "FaceCheck.ID", Enabled: &enabled},
		}
		t.Cleanup(func() { config.C.IdentificationSources = prev })

		links := sourceLinksFor(t, admin, query.ID.String(), sourceLinkImage).IdentificationQuery.SourceLinks
		var face *struct {
			Source    string
			Kind      string
			URL       string
			Prefilled bool
		}
		for i := range links {
			if links[i].Source == "FaceCheck.ID" {
				face = &links[i]
			}
		}
		require.NotNil(t, face, "an operator who enabled it must see it: %v", links)
		assert.Equal(t, "REMOTE_FACE", face.Kind)
		// FaceCheck.ID takes an upload, so the reader arrives at its front door.
		assert.Equal(t, "https://facecheck.id", face.URL)
		assert.False(t, face.Prefilled)
	})

	t.Run("no image address is an empty list, not an error", func(t *testing.T) {
		resp := sourceLinksFor(t, admin, query.ID.String(), "")
		assert.Empty(t, resp.IdentificationQuery.SourceLinks,
			"there is nothing to look up yet, which is a state and not a fault")
	})
}

// TestIdentificationSourceLinksShapeIsRenderable is the shape check: every field
// the schema declares non-null must come back non-zero, because a resolver
// returning the zero value compiles and passes every service test.
func TestIdentificationSourceLinksShapeIsRenderable(t *testing.T) {
	admin := asAdmin(t)
	query := postQuery(t, identification.TargetScene, "source links shape case", uuid.NullUUID{})

	var raw identSourceLinksResponse
	admin.client.MustPost(`
		query Links($id: ID!, $img: String!) {
			identificationQuery(id: $id) {
				id
				sourceLinks(imageUrl: $img) { source kind url prefilled }
			}
		}
	`, &raw, client.Var("id", query.ID.String()), client.Var("img", sourceLinkImage))

	require.NotEmpty(t, raw.IdentificationQuery.SourceLinks)
	for _, l := range raw.IdentificationQuery.SourceLinks {
		assert.NotEmpty(t, l.Source)
		assert.Contains(t, []string{"REVERSE_MEDIA", "REMOTE_FACE", "COMMUNITY"}, l.Kind)
		u, err := url.Parse(l.URL)
		require.NoError(t, err, "%s", l.Source)
		assert.Contains(t, []string{"http", "https"}, u.Scheme, "%s", l.Source)
		assert.NotEmpty(t, u.Host, "%s", l.Source)
	}

	// And the response is valid JSON end to end, so nothing above was read out of
	// a fragment.
	encoded, err := json.Marshal(raw.IdentificationQuery.SourceLinks)
	require.NoError(t, err)
	assert.NotEmpty(t, encoded)
}
