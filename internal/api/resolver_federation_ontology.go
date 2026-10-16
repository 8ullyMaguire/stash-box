package api

import (
	"context"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/federation"
)

// The ontology's federation half (phase 12, slice 12a).
//
// Two fields and no mutation, and the absence is the design. There is nothing here that writes a
// remote ontology into a local one: F2's rule (foreign evidence is evidence and never a vote) applies
// to an ontology exactly as it applies to a candidate, and merging two tag graphs automatically is
// how a taxonomy ends up with edges nobody can explain. The declaration and the compatibility rule are
// what an operator -- or the transport, when it exists -- asks BEFORE deciding anything.
//
// Note what these two fields do NOT do: they do not cache. `ontologyDeclaration` reads the version and
// its document on every call, because an instance that changed its ontology a minute ago must not
// report the one it booted with. The database is the authority and the round trip is two queries.

// OntologyDeclaration is what this instance says about the ontology it speaks.
func (r *queryResolver) OntologyDeclaration(ctx context.Context) (*models.OntologyDeclaration, error) {
	decl, err := federation.LocalDeclaration(ctx, r.services.TagOntology())
	if err != nil {
		return nil, err
	}
	return &decl, nil
}

// OntologyCompatibility decides what may cross to a peer declaring these core versions.
//
// The peer's side is a SET, which is what a declaration carries. An earlier version of this field took
// a single number, and the integration test is what showed it could not answer: a peer at "core 2"
// alone is refused the core 1 fields (it dropped the older document) and allowed the v2-only one, while
// a peer at "core 1" is the other way round -- so no single number could ever produce "everything
// crosses", and the field's most useful answer was unreachable. The rule underneath always took two
// sets; only the surface narrowed it.
func (r *queryResolver) OntologyCompatibility(ctx context.Context, coreVersions []int) (*models.OntologyCompatibility, error) {
	// The instance's own versions come FROM its own declaration rather than from a constant here: a
	// second list would be a second place to update when core 3 arrives, and the failure it produces
	// is a compatibility answer describing an instance that does not exist.
	decl, err := federation.LocalDeclaration(ctx, r.services.TagOntology())
	if err != nil {
		return nil, err
	}

	// A pointer because gqlgen binds this type as one (the build says which it wants, and this is that
	// line: `want OntologyCompatibility(ctx, int) (*models.OntologyCompatibility, error)`). A value
	// return here is a compile error naming both signatures, which is the version of this mistake that
	// costs nothing.
	compat := federation.Compatibility(decl.CoreVersions, coreVersions)
	return &compat, nil
}
