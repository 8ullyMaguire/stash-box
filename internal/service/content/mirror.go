package content

import (
	"context"
	"fmt"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
)

const (
	// MirrorDistance is the PHASH Hamming distance within which two scenes count as the same content
	// for the purpose of handing a reader over.
	//
	// TIGHT on purpose. The failure mode of a loose distance is sending somebody to a DIFFERENT scene,
	// which is worse than offering no mirror at all: a mirror is a claim about content, and a claim
	// that is wrong once is worth less than silence. (The identification board takes its distance from
	// the client, where being generous costs a moderator a glance; here it would cost a reader.)
	MirrorDistance = 4

	// MirrorCandidates bounds how many source rows one lookup considers.
	MirrorCandidates = 25
)

// Mirror finds a live URL for the same content on ANOTHER scene, when this scene has no usable source
// of its own.
//
// The candidate scenes come from the caller, because "the same content" is the fingerprint cluster's
// question (internal/service/fingerprint) and this service should not have to know how a cluster is
// computed. The RULE lives here:
//
//   - A live source of its own means there is nothing to hand over: return nil.
//   - A canonical source under DMCA or in the archive means NO mirror is offered. That is a decision
//     the instance has already made, not a hosting problem, and routing around it through a cluster
//     would be circumventing a takedown by accident. (This is the one branch where the answer is
//     deliberately nil rather than "the best remaining option".)
//   - Otherwise the first live source among the candidates is the mirror, chosen canonical-first and
//     then by fewest failures.
//
// It returns nil,nil -- not an error -- when there is no mirror: "this instance knows of nowhere else
// to send you" is an answer, and the absence of one is not a failure.
func (s *Service) Mirror(ctx context.Context, sceneID uuid.UUID, candidateSceneIDs []uuid.UUID) (*models.ContentSource, error) {
	own, err := s.queries.FindSceneSources(ctx, sceneID)
	if err != nil {
		return nil, fmt.Errorf("reading the scene's own sources: %w", err)
	}

	for _, source := range own {
		if source.Status == StatusLive {
			return nil, nil
		}
		if source.Status == StatusDMCA || source.Status == StatusArchived {
			return nil, nil
		}
	}

	if len(candidateSceneIDs) == 0 {
		return nil, nil
	}

	rows, err := s.queries.FindLiveSourcesBySceneIDs(ctx, queries.FindLiveSourcesBySceneIDsParams{
		SceneIds: candidateSceneIDs,
		Lim:      MirrorCandidates,
	})
	if err != nil {
		return nil, fmt.Errorf("reading the candidate scenes' sources: %w", err)
	}

	for _, row := range rows {
		if row.EntityID == sceneID {
			// Defensive: this scene reached here because none of its own sources is live, so it
			// should have no live row at all. If it somehow does, handing it back as a "mirror" of
			// itself would read as content somewhere else.
			continue
		}
		source := ContentSourceToModel(row)
		return &source, nil
	}

	return nil, nil
}
