package api

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/stashapp/stash-box/internal/config"
	"github.com/stashapp/stash-box/internal/service/federation"
)

// The federation tag table (phase 12, slice 12e): what this instance's LOCAL tag names mean in the
// agreed core vocabulary.
//
// WHY THIS IS A ROUTE. The mapping is the answer to a question a peer would otherwise have to guess at:
// "when you send me `measurements`, what did you mean here?" Without it, the resolution is a rule with no
// caller -- computable, tested, and invisible -- which this programme has now found three times is the
// same as absent. The table is readable by a peer (it is a vocabulary statement, not user data) and by
// this instance's operator (whose declarations are the input, and whose mistakes appear here as
// refusals rather than as quiet omissions).
//
// IT CARRIES THE REFUSALS, and that is most of its value. A mapping pointing at a name nobody agreed on,
// or at another local tag, or at itself, is a declaration that will not cross; an operator reading a list
// of only the SUCCESSFUL mappings would be looking at a table that has quietly dropped the ones they got
// wrong. So every declaration appears, resolved or refused, with the sentence saying which.
//
// The vocabulary is included because a peer otherwise has no way to know which names are agreed -- it
// would have to learn them from the mappings of whoever happened to be reachable.

// federationTagsResponse is the body. It is a struct rather than a map so that the field names are the
// ones a peer's decoder is written against, and a rename is a compile error here rather than a silent
// null on the wire.
type federationTagsResponse struct {
	InstanceID   string                   `json:"InstanceID"`
	CoreVersions []int                    `json:"CoreVersions"`
	Vocabulary   []string                 `json:"Vocabulary"`
	Tags         []federationStructureTag `json:"Tags"`
}

// federationStructureTag is one declaration, resolved or refused. The Refusal field is always present in
// the JSON (never omitted) so a peer's "did this resolve" check cannot pass on a missing field -- the
// same defect the wire vocabulary guard exists to prevent, in a different costume.
type federationStructureTag struct {
	Local   string `json:"Local"`
	Core    string `json:"Core"`
	Refusal string `json:"Refusal"`
}

// FederationTags answers GET /federation/tags.
func FederationTags() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		instance := config.GetInstanceID()
		if instance == "" {
			// The same refusal as the identify endpoint, by name: an unattributable answer is not an
			// answer, and the operator has a fixable configuration problem rather than a bad request.
			refuse(w, http.StatusServiceUnavailable,
				"this instance has no `instance_id` set, so it cannot name itself on the federation wire")
			return
		}

		mappings := config.GetTagMappings()
		names := make([]string, 0, len(mappings))
		for name := range mappings {
			names = append(names, name)
		}
		sort.Strings(names)

		resolutions := federation.ResolveLocalTags(names, mappings)
		tags := make([]federationStructureTag, 0, len(resolutions))
		for _, res := range resolutions {
			tags = append(tags, federationStructureTag{Local: res.Local, Core: res.Core, Refusal: res.Refusal})
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(federationTagsResponse{
			InstanceID:   instance,
			CoreVersions: []int{federation.CoreOntologyV1, federation.CoreOntologyV2},
			Vocabulary:   federation.CoreTagVocabulary(federation.CoreOntologyV1),
			Tags:         tags,
		})
	}
}
