package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/stashapp/stash-box/internal/config"
	"github.com/stashapp/stash-box/internal/service"
	"github.com/stashapp/stash-box/internal/service/federation"
)

// The federation identify endpoint (phase 12, slice 12b): the resource `client.go` has been POSTing
// Questions to since the mesh was built, finally served by the instance that sends them.
//
// WHY THIS IS A ROUTE AND NOT A GRAPHQL FIELD. A peer is a different implementation with no account
// here, and GraphQL is this instance's session-authenticated surface for people. A peer that had to
// hold an account to say "here is the ontology I speak" would be a peer holding a credential it can
// lose, and the answer is public posture -- a version number and a digest -- not user data.
//
// WHAT IT PUTS ON THE WIRE, and what it checks before it does:
//
//   - the protocol version HEADER, because a peer on an older build must be recognisable as older
//     rather than as broken (ProtocolVersion's own comment);
//   - the question's shape (federation.Question.Validate, the same F1 check the outbound path runs, so
//     an inbound question is held to the rule this instance holds its own questions to);
//   - this instance's OWN name, from config's `instance_id`, refusing by name when it is unset -- an
//     anonymous answer cannot be attributed, and the peer registry is keyed by instance id;
//   - the asking peer's own claim, if it declared one (federation.AnswerQuestion).
//
// The refusals are HTTP statuses rather than a 200 with an empty body, because a peer that cannot tell
// "this instance has not built that" from "this instance has nothing" will record an empty answer as
// evidence. 503 is "this instance is not configured to answer" (fixable by an operator), 422 is "your
// request cannot be answered as written" (fixable by the peer), 400 is "that is not a question".

// maxQuestionBytes bounds an inbound body. A peer that streams forever would otherwise consume memory
// until the process dies, and this endpoint faces hostile input by construction -- anyone who can reach
// the port can POST to it.
const maxQuestionBytes = 1 << 20

// FederationIdentify answers a peer's Question.
func FederationIdentify(fac service.Factory) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Stash-Federation-Version"); got != strconv.Itoa(federation.ProtocolVersion) {
			// Named rather than 400-flat: a peer three versions behind and a peer sending nothing are
			// different problems, and the header's absence is the common one during rollout.
			refuse(w, http.StatusBadRequest, "unsupported federation protocol version: "+
				describeVersion(got))
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, maxQuestionBytes))
		if err != nil {
			refuse(w, http.StatusBadRequest, "unreadable request body")
			return
		}

		var q federation.Question
		if err := json.Unmarshal(body, &q); err != nil {
			refuse(w, http.StatusBadRequest, "that is not a question: "+err.Error())
			return
		}

		// The F1 content check, on the way IN. The outbound half runs it before every broadcast; running
		// it here too means a peer cannot make this instance the one that stored a path or a URL.
		if err := q.Validate(); err != nil {
			refuse(w, http.StatusUnprocessableEntity, err.Error())
			return
		}

		answer, err := federation.AnswerQuestion(r.Context(), fac.TagOntology(), config.GetInstanceID(), q)
		if err != nil {
			switch {
			case errors.Is(err, federation.ErrNoInstanceID):
				// 503, not 422: nothing about the peer's request is wrong. The instance is not set up to
				// answer, and the message names the key so an operator can fix it without reading code.
				refuse(w, http.StatusServiceUnavailable, err.Error()+" (set `instance_id` in the config)")
			case errors.Is(err, federation.ErrIdentifyUnserved),
				errors.Is(err, federation.ErrClaimRejected):
				refuse(w, http.StatusUnprocessableEntity, err.Error())
			default:
				refuse(w, http.StatusInternalServerError, "could not answer: "+err.Error())
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(answer); err != nil {
			// The status is already written, so this cannot become an error response. It is logged by the
			// recoverer if it panics; a truncated body is visible to the peer as a JSON error.
			return
		}
	}
}

func describeVersion(got string) string {
	if got == "" {
		return "none sent"
	}
	return strconv.Quote(got)
}

// refuse writes a status and a JSON body.
//
// The body is JSON rather than plain text because the caller is a program, and a peer's diagnostic
// ("peer X returned 422") is only useful if the message travels with it -- the same reason the answer
// itself is JSON.
func refuse(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
