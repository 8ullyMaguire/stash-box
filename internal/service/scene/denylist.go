package scene

import (
	"context"
	"fmt"

	"github.com/gofrs/uuid"
)

// deniedSceneIDs returns the subset of the given ids that the instance denies, as a set.
//
// SLICE 5g: THE FOUR DIRECT PATHS. The four ENUMERATING paths have been gated since slice 5f, and the
// gap this closes had a different character from the tier gap -- a tier removes a thing from a VIEW
// (which is why `findScene(id)` was left ungated on purpose), while migration 87's own wording for this
// denylist is "entities whose CONTENT ACCESS is denied". Access, not visibility.
//
// A DENIED SCENE READS AS NOT FOUND. Not an error, and not a distinguishable "denied": to a reader,
// "this instance refuses to serve it" and "there is no such scene" have to be the same answer, or the
// response itself confirms the content exists. That is also why the ranking of the two instruments
// matters -- a tier is a preference of the VIEWER and a denylist is a decision of the INSTANCE.
//
// FAIL CLOSED: if the denylist cannot be read, the read fails rather than proceeding. A gate that
// serves content when its own check errored is not a gate.
//
// One query for the whole batch, the same `DeniedSceneIDs` the feed uses (slice 5f): the four paths
// differ in shape, not in what they must ask.
func (s *Scene) deniedSceneIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]struct{}, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	denied, err := s.queries.DeniedSceneIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("reading the per-entity content denylist: %w", err)
	}
	if len(denied) == 0 {
		return nil, nil
	}

	set := make(map[uuid.UUID]struct{}, len(denied))
	for _, id := range denied {
		set[id] = struct{}{}
	}
	return set, nil
}
