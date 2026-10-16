package cron

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"

	"github.com/stashapp/stash-box/internal/service/federation"
	"github.com/stashapp/stash-box/pkg/logger"
)

// The ontology exchange (phase 12, slice 12f): the outbound half's first production caller.
//
// `Client.Broadcast` has existed since the mesh was built and every caller of it was a test. A broadcast
// method with no caller is not a capability -- it is a comment with an HTTP client in it -- and the real
// consequence was that the wire had ONE END WIRED: this instance could answer a peer (12b, 12c) and had
// no mechanism to ask one. This job is the mechanism.
//
// WHY IT IS A CRON JOB AND NOT A REQUEST PATH. Discovery is not something a person asks for: it is the
// mesh keeping its own picture of who speaks what, and doing it inside a request would put a
// multi-second, partly-failing network fan-out on a page's critical path. It is also, deliberately, the
// mildest possible caller: it asks a question every peer can answer from config (an ontology claim is a
// version number, a digest and two counts -- no content, no names, no people), and it does nothing with
// what it learns except log one line.
//
// WHAT IT DOES NOT DO. It does not record the answers. The registry is the operator's, and writing a
// peer's self-description into it would make a claim this instance has not verified look like a fact it
// knows. The rows are returned, summarised, and dropped; the first consumer that needs them stored will
// have to say why.
//
// AN INSTANCE WITH NO PEERS ASKS NOTHING. `List` on an empty registry returns nothing and this returns
// before any network call: the exchange is off for exactly the instances that have no federation
// configured, which is every instance by default.
func (c Cron) exchangeOntologies() {
	ctx, span := otel.Tracer(tracerName).Start(context.Background(), "cron.exchangeOntologies")
	defer span.End()

	// ListEnabled, NOT List: the registry's `enabled` flag is the operator's switch, and a job that reads
	// every row would contact a peer they had switched off. (Writing this found that the exchange itself
	// had the same hole a layer down -- `AskableAt` is about freshness and trust, not about the switch --
	// so ExchangeOntologies now checks `Enabled` as well. Both, because one of the two is the one that
	// gets refactored away.)
	peers, err := c.fac.Federation().ListEnabled(ctx)
	if err != nil {
		logger.Errorf("federation exchange: reading the peer registry: %v", err)
		return
	}
	if len(peers) == 0 {
		return
	}

	// Our own claim, built by the same code the answering half uses -- so a peer comparing what we SAY
	// against what we ANSWER cannot find two different stories.
	local, err := federation.LocalDeclaration(ctx, c.fac.TagOntology())
	if err != nil {
		logger.Errorf("federation exchange: reading this instance's own ontology: %v", err)
		return
	}

	q := federation.Question{QueryID: uuid.Must(uuid.NewV7()), Ontology: federation.ClaimOf(local)}

	rows, err := federation.ExchangeOntologies(ctx, federation.NewClient(nil), peers, q,
		local.CoreVersions, time.Now())
	if err != nil {
		// The rows say which peers were skipped before the failure; logging only the error would lose
		// them, and they are the part an operator can act on.
		logger.Errorf("federation exchange: %s: %v", federation.Summary(rows), err)
		return
	}

	logger.Infof("federation exchange: %s", federation.Summary(rows))
}
