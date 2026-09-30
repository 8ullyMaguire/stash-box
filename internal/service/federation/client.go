package federation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// Client asks peers questions and collects their answers.
//
// ONE GOROUTINE PER PEER, with a per-peer deadline. The reason is arithmetic
// rather than stylistic: a broadcast to N peers runs concurrently, so the
// query's latency is the SLOWEST peer's, not the median. Without a per-peer cap,
// one peer that accepts the connection and never responds holds every query
// open for as long as the caller's context allows — and the caller is usually a
// user waiting on a page.
//
// A peer that fails is NOT an error for the whole broadcast. Partial answers are
// the normal case: some peers are down, some are misconfigured, and one instance
// being unreachable is not a reason to fail the query for everyone else.

// Client broadcasts questions to peers.
type Client struct {
	// httpClient is injectable so tests can point it at an httptest server and
	// so a test never depends on real DNS or a real network.
	httpClient *http.Client

	// perPeerTimeout overrides RequestTimeout when non-zero. Exists for tests
	// that need to observe a timeout without waiting five seconds; production
	// leaves it zero and gets RequestTimeout.
	perPeerTimeout time.Duration

	// resolver is the DNS answerer used by the dial-time guard. Nil means the
	// real one, so production resolves live and is unaffected.
	//
	// It exists because the guard is CORRECT and the tests are not: every test
	// peer is an httptest server on 127.0.0.1, which R074 rule 2 refuses for
	// precisely the reason it exists. Without an injection point the only ways
	// forward are to weaken the guard or to delete the tests, and both trade a
	// real protection for a green suite.
	//
	// So the tests inject a resolver that says 127.0.0.1 is fine, and the guard
	// keeps every other check -- scheme, path, host shape. What is bypassed is
	// the ADDRESS judgement only, and only in tests.
	resolver Resolver
}

// WithResolver returns a copy of the client that resolves through r at dial time.
// Test-only, and named so its purpose is obvious at the call site.
func (c *Client) WithResolver(r Resolver) *Client {
	return &Client{httpClient: c.httpClient, perPeerTimeout: c.perPeerTimeout, resolver: r}
}

// NewClient returns a Client using the given http client.
//
// A nil httpClient is replaced with a client bounded by RequestTimeout, because
// http.DefaultClient has NO timeout at all and the first caller who forgets to
// set one gets a broadcast that hangs for as long as the server feels like.
func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: RequestTimeout}
	}
	return &Client{httpClient: httpClient}
}

// WithPerPeerTimeout returns a copy of the client with a different per-peer
// budget. Test-only, and named so its purpose is obvious at the call site.
func (c *Client) WithPerPeerTimeout(d time.Duration) *Client {
	return &Client{httpClient: c.httpClient, perPeerTimeout: d, resolver: c.resolver}
}

func (c *Client) timeout() time.Duration {
	if c.perPeerTimeout > 0 {
		return c.perPeerTimeout
	}
	return RequestTimeout
}

// answerPath is the endpoint a peer serves.
//
// A path rather than the peer's base URL alone, because the base URL is the
// instance root and this is one resource under it. Kept as a constant so both
// sides of the wire are changed together rather than by two people editing two
// repositories and neither knowing.
const answerPath = "/federation/identify"

// BroadcastResult is what happened, per peer.
//
// Returned rather than only the successful answers, because "asked 5 peers,
// heard from 2" is the single most useful thing an operator can be told about a
// federated query, and discarding the failures throws it away.
type BroadcastResult struct {
	// Answers are the replies received, tagged with which peer sent them.
	Answers []Answer
	// Failed lists the peers that did not answer, with the reason.
	Failed []PeerFailure
}

// PeerFailure is one peer that did not answer.
type PeerFailure struct {
	InstanceID string
	Reason     string
}

// Broadcast asks every peer and returns what came back.
//
// The question is validated ONCE here, not per peer: validation is pure and
// the question does not change, so validating per peer would either waste work
// or — worse — leave a caller that reaches Broadcast directly unprotected.
func (c *Client) Broadcast(ctx context.Context, peers []Peer, q Question) (BroadcastResult, error) {
	// The F1 content guard, and until now it was NOT running.
	//
	// This line read `if err := error(nil); err != nil` -- a comparison that is
	// false for every input, so the guard could never fire and any question
	// containing a path, a URL or an internal hostname was broadcast to every
	// askable peer. D2 step 4 committed Question.Validate() for exactly this and
	// the call site was left as a stub.
	//
	// It survived because the test that would have caught it, TestBroadcastRefuses
	// DirtyQuestion, lives in this file, and this file was never compiled: it was
	// an untracked file in another session's worktree. Nothing that imported the
	// package could reach the defect.
	//
	// The signature was right and the call was wrong, which is why it compiled
	// clean. That is the whole hazard: a guard that cannot fail is not a guard,
	// and `go build` has nothing to say about it.
	if err := q.Validate(); err != nil {
		return BroadcastResult{}, fmt.Errorf("refusing to broadcast: %w", err)
	}
	if len(peers) == 0 {
		return BroadcastResult{}, nil
	}

	// Askable peers only, via the same predicate the registry uses.
	//
	// NOT a second `p.Enabled` check: the enabled filter already happens in SQL
	// in ListEnabledFederationPeers, and that query's own comment says the
	// filter lives there precisely so the broadcast path cannot read a disabled
	// peer. Re-checking an unexported-away field here would need a field that
	// Peer does not carry, and adding one would create two places that decide
	// "may I ask this peer".
	askable := make([]Peer, 0, len(peers))
	for _, p := range peers {
		if p.Askable() {
			askable = append(askable, p)
		}
	}
	if len(askable) == 0 {
		return BroadcastResult{}, nil
	}

	results := make([]answerOrFailure, len(askable))
	var wg sync.WaitGroup
	for i, p := range askable {
		wg.Add(1)
		go func(i int, p Peer) {
			defer wg.Done()
			answer, err := c.askOne(ctx, p, q)
			if err != nil {
				results[i] = answerOrFailure{failure: PeerFailure{
					InstanceID: p.InstanceID, Reason: err.Error(),
				}}
				return
			}
			results[i] = answerOrFailure{answer: *answer}
		}(i, p)
	}
	wg.Wait()

	out := BroadcastResult{}
	for _, r := range results {
		if r.failure.InstanceID != "" {
			out.Failed = append(out.Failed, r.failure)
			continue
		}
		out.Answers = append(out.Answers, r.answer)
	}
	return out, nil
}

type answerOrFailure struct {
	answer  Answer
	failure PeerFailure
}

// askOne asks a single peer, bounded by the per-peer timeout.
//
// The per-peer context is derived from the caller's, so a cancelled broadcast
// cancels every in-flight request — the timeout is an upper bound on a healthy
// peer, not a way to ignore cancellation.
func (c *Client) askOne(ctx context.Context, p Peer, q Question) (*Answer, error) {
	peerCtx, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()

	// DIAL-TIME VALIDATION -- the second half of R074 rule 2, and the whole
	// reason DialGuard exists (internal/service/federation/dialguard.go).
	//
	// The write-time guard cannot cover this. A hostname that resolved to a
	// public address when the peer row was written can resolve to 127.0.0.1 by the
	// time we dial, because a DNS record's TTL has nothing to do with when an
	// operator registered the peer. An attacker with a short TTL walks straight
	// past a guard that ran only at insert.
	//
	// It runs here, per peer, and a refusal is a per-peer failure rather than a
	// fatal error: a peer that cannot be safely dialled is the same shape as a
	// peer that is down, and Broadcast already has somewhere to say so. Refusing
	// the whole broadcast because one peer is unsafe would let any peer operator
	// deny service to every other peer.
	if err := dialGuard(peerCtx, p, c.resolver); err != nil {
		return nil, err
	}

	body, err := json.Marshal(q)
	if err != nil {
		return nil, fmt.Errorf("encoding question: %w", err)
	}

	req, err := http.NewRequestWithContext(peerCtx, http.MethodPost,
		p.BaseURL+answerPath, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// The protocol version, because the two implementations are separate repos
	// on separate schedules. Without it the only signal a mismatched peer gives
	// is a deserialisation error, which reads as "that peer is down".
	req.Header.Set("X-Stash-Federation-Version", fmt.Sprintf("%d", ProtocolVersion))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// The URL is deliberately absent from this message: it can contain an
		// internal hostname, and an error string is the thing most likely to
		// end up in a log or a UI.
		return nil, fmt.Errorf("peer %s unreachable: %w", p.InstanceID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("peer %s returned %s", p.InstanceID, resp.Status)
	}

	// Bounded read. A peer that streams forever would otherwise consume memory
	// until the process dies, and a federation client is exactly the kind of
	// code that faces hostile input.
	limited := io.LimitReader(resp.Body, maxAnswerBytes)

	var answer Answer
	if err := json.NewDecoder(limited).Decode(&answer); err != nil {
		return nil, fmt.Errorf("peer %s sent an unreadable answer: %w", p.InstanceID, err)
	}

	// The peer's self-declared identity is CHECKED, not trusted. A peer that
	// answers claiming to be someone else is either misconfigured or hostile,
	// and storing its evidence under the wrong peer row would let it launder
	// claims through an instance an operator has disabled.
	if answer.PeerInstanceID != p.InstanceID {
		return nil, fmt.Errorf("peer %s answered claiming to be %q",
			p.InstanceID, answer.PeerInstanceID)
	}

	return &answer, nil
}

// maxAnswerBytes bounds one peer's reply.
//
// A generous bound — a peer with many candidates for one query is legitimate —
// but a bound nonetheless, because the reply comes from another instance and the
// decoder has no other way to stop a hostile or broken peer.
const maxAnswerBytes = 1 << 20 // 1 MiB
