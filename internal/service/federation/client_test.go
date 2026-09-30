package federation

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/uuid"
)

// peerServer is a stand-in for another stash-box instance.
//
// httptest rather than a real listener on a fixed port: it cannot collide with
// another profile's test, and it is the only way to test "a peer that never
// answers" without a real hang.
func peerServer(t *testing.T, instanceID string, handler func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != answerPath {
			http.Error(w, "wrong path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func answerHandler(instanceID string, candidates []RemoteCandidate) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		var q Question
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			http.Error(w, "bad question", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Answer{
			PeerInstanceID: instanceID,
			Candidates:     candidates,
		})
	}
}

// testResolver treats every host as a public address.
//
// It is here because the dial-time guard is CORRECT and httptest is not: every
// test peer listens on 127.0.0.1, which R074 rule 2 refuses for exactly the
// reason it exists. Injecting a resolver that calls 127.0.0.1 public is the only
// way to test a client that must dial loopback without weakening the guard --
// and it bypasses the ADDRESS judgement only. Scheme, path and host-shape checks
// all still run, so a test that feeds a file:// or a traversal still fails.
type testResolver struct{ calls int }

// It answers EVERY host -- loopback included -- with the same public address.
//
// A first attempt made a literal address resolve to itself, "as real DNS does".
// That was the bug: the guard then correctly rejected 127.0.0.1, so every
// broadcast test failed, and the failure read like a broken guard. The point of
// the injection is to make the ADDRESS judgement permissive for httptest while
// leaving every other check real -- and the address judgement is precisely the
// one being overridden. Echoing the literal was asking the guard to reject the
// test and then wondering why it did.
func (r *testResolver) LookupIP(ctx context.Context, host string) ([]net.IP, error) {
	r.calls++
	return []net.IP{net.ParseIP("93.184.216.34")}, nil
}

// testClient is the client every broadcast test uses: production defaults, with
// loopback dialling permitted.
func testClient(t *testing.T, httpClient *http.Client) *Client {
	t.Helper()
	return NewClient(httpClient).WithResolver(&testResolver{})
}

func freshPeer(id, name, baseURL string) Peer {
	now := time.Now()
	return Peer{
		ID:          uuid.Must(uuid.NewV7()),
		Name:        name,
		BaseURL:     baseURL,
		InstanceID:  id,
		TrustWeight: 0.5,
		LastSeenAt:  &now,
	}
}

// TestBroadcastCollectsAnswers is the happy path, and the control for every
// failure test below: if this passes, a failure test failing means the failure
// is detected rather than the client being broken.
func TestBroadcastCollectsAnswers(t *testing.T) {
	srv := peerServer(t, "peer-1", answerHandler("peer-1", []RemoteCandidate{
		{Name: "Alex Winter", PeerID: "remote-1", SuggesterCount: 3},
	}))

	c := testClient(t, nil)
	res, err := c.Broadcast(context.Background(),
		[]Peer{freshPeer("peer-1", "one", srv.URL)}, goodQuestion())
	if err != nil {
		t.Fatalf("Broadcast: %v", err)
	}
	if len(res.Failed) != 0 {
		t.Fatalf("unexpected failures: %v", res.Failed)
	}
	if len(res.Answers) != 1 {
		t.Fatalf("got %d answers, want 1", len(res.Answers))
	}
	if got := res.Answers[0].Candidates[0].Name; got != "Alex Winter" {
		t.Errorf("candidate name = %q", got)
	}
}

// TestBroadcastPartialFailure is the case that matters most: one dead peer must
// not fail the query for everyone else.
func TestBroadcastPartialFailure(t *testing.T) {
	good := peerServer(t, "peer-good", answerHandler("peer-good", []RemoteCandidate{
		{Name: "Alex Winter", PeerID: "r1", SuggesterCount: 2},
	}))

	c := testClient(t, nil).WithPerPeerTimeout(2 * time.Second)

	// A peer pointed at a closed port: the classic "down peer".
	dead := freshPeer("peer-dead", "dead", "http://127.0.0.1:1")

	res, err := c.Broadcast(context.Background(),
		[]Peer{freshPeer("peer-good", "good", good.URL), dead}, goodQuestion())
	if err != nil {
		t.Fatalf("one unreachable peer must not fail the whole broadcast: %v", err)
	}
	if len(res.Answers) != 1 {
		t.Errorf("got %d answers, want 1 — the healthy peer's evidence is lost", len(res.Answers))
	}
	if len(res.Failed) != 1 || res.Failed[0].InstanceID != "peer-dead" {
		t.Errorf("failures = %v, want exactly peer-dead", res.Failed)
	}
}

// TestBroadcastSlowPeerDoesNotBlock is the reason there is a per-peer timeout.
//
// A peer that accepts the connection and never writes a response. Without the
// timeout the broadcast blocks until the caller's context dies.
func TestBroadcastSlowPeerDoesNotBlock(t *testing.T) {
	slow := peerServer(t, "peer-slow", func(w http.ResponseWriter, r *http.Request) {
		// Block well past the per-peer budget, then give up. If the client has
		// no timeout this is where the test hangs instead of failing.
		time.Sleep(3 * time.Second)
		w.WriteHeader(http.StatusOK)
	})

	fast := peerServer(t, "peer-fast", answerHandler("peer-fast", []RemoteCandidate{
		{Name: "Quick", PeerID: "r1", SuggesterCount: 1},
	}))

	c := testClient(t, nil).WithPerPeerTimeout(200 * time.Millisecond)

	start := time.Now()
	res, err := c.Broadcast(context.Background(),
		[]Peer{freshPeer("peer-slow", "slow", slow.URL),
			freshPeer("peer-fast", "fast", fast.URL)},
		goodQuestion())
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Broadcast: %v", err)
	}
	if len(res.Answers) != 1 {
		t.Errorf("got %d answers, want 1; a slow peer must not lose the fast one's", len(res.Answers))
	}
	// 3s is the slow peer's own sleep. Well under that means the client gave up.
	if elapsed > 2*time.Second {
		t.Errorf("broadcast took %v; the per-peer timeout did not fire", elapsed)
	}
}

// TestBroadcastRejectsImpostor is the positive control for the identity check.
//
// A peer that answers claiming to be a DIFFERENT instance. Accepting it would
// let a hostile peer launder its claims through an instance an operator has
// disabled — the evidence would be stored under peer-a's row.
func TestBroadcastRejectsImpostor(t *testing.T) {
	// Configured as peer-a, answers as peer-b.
	srv := peerServer(t, "peer-b", answerHandler("peer-b", []RemoteCandidate{
		{Name: "Injected", PeerID: "r1", SuggesterCount: 99},
	}))

	c := testClient(t, nil)
	res, err := c.Broadcast(context.Background(),
		[]Peer{freshPeer("peer-a", "a", srv.URL)}, goodQuestion())
	if err != nil {
		t.Fatalf("Broadcast: %v", err)
	}
	if len(res.Answers) != 0 {
		t.Errorf("an impostor answer was ACCEPTED: %+v", res.Answers)
	}
	if len(res.Failed) != 1 {
		t.Fatalf("expected one failure, got %v", res.Failed)
	}
	if res.Failed[0].InstanceID != "peer-a" {
		t.Errorf("failure attributed to %q, want peer-a", res.Failed[0].InstanceID)
	}
}

// TestBroadcastRefusesDirtyQuestion is F1 at the boundary: the guard runs on
// the way OUT, so Broadcast is where it has to hold.
func TestBroadcastRefusesDirtyQuestion(t *testing.T) {
	srv := peerServer(t, "peer-1", answerHandler("peer-1", nil))
	called := false
	spy := peerServer(t, "peer-1", func(w http.ResponseWriter, r *http.Request) {
		called = true
		answerHandler("peer-1", nil)(w, r)
	})
	_ = srv

	dirty := goodQuestion()
	dirty.Description = "tall, see /etc/passwd for details"

	c := testClient(t, nil)
	_, err := c.Broadcast(context.Background(),
		[]Peer{freshPeer("peer-1", "one", spy.URL)}, dirty)
	if err == nil {
		t.Fatal("Broadcast sent a question containing a path")
	}
	if called {
		t.Error("the peer was contacted despite the question being rejected")
	}
}

// TestBroadcastSkipsDisabledAndStale: Askable() is the gate, so a stale peer is
// not asked at all. Its evidence is old by definition (F3).
func TestBroadcastSkipsDisabledAndStale(t *testing.T) {
	asked := 0
	var mu sync.Mutex
	srv := peerServer(t, "peer-1", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked++
		mu.Unlock()
		answerHandler("peer-1", nil)(w, r)
	})

	// LastSeenAt far in the past: stale.
	stale := freshPeer("peer-stale", "stale", srv.URL)
	longAgo := time.Now().Add(-1000 * time.Hour)
	stale.LastSeenAt = &longAgo

	if stale.Askable() {
		t.Fatal("test setup: a 1000-hour-old peer should not be askable")
	}

	c := testClient(t, nil)
	res, err := c.Broadcast(context.Background(),
		[]Peer{stale, freshPeer("peer-1", "ok", srv.URL)}, goodQuestion())
	if err != nil {
		t.Fatalf("Broadcast: %v", err)
	}
	mu.Lock()
	got := asked
	mu.Unlock()
	if got != 1 {
		t.Errorf("peer was contacted %d times, want 1 (only the fresh peer)", got)
	}
	if len(res.Answers) != 1 {
		t.Errorf("got %d answers, want 1", len(res.Answers))
	}
}

// TestBroadcastEmptyPeerList is a no-op, not an error.
func TestBroadcastEmptyPeerList(t *testing.T) {
	c := testClient(t, nil)
	res, err := c.Broadcast(context.Background(), nil, goodQuestion())
	if err != nil {
		t.Errorf("an empty peer list should not error: %v", err)
	}
	if len(res.Answers) != 0 || len(res.Failed) != 0 {
		t.Errorf("expected empty result, got %+v", res)
	}
}

// TestBroadcastSendsProtocolVersion: a peer on a different schedule needs to be
// recognisable as different, not as broken.
func TestBroadcastSendsProtocolVersion(t *testing.T) {
	var got string
	srv := peerServer(t, "peer-1", func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Stash-Federation-Version")
		answerHandler("peer-1", nil)(w, r)
	})

	c := testClient(t, nil)
	if _, err := c.Broadcast(context.Background(),
		[]Peer{freshPeer("peer-1", "one", srv.URL)}, goodQuestion()); err != nil {
		t.Fatalf("Broadcast: %v", err)
	}
	if got == "" {
		t.Fatal("no protocol version header was sent")
	}
	if got != "1" {
		t.Errorf("protocol version = %q, want %q", got, "1")
	}
}

// TestBroadcastConcurrent: peers are asked in parallel, so N slow-but-OK peers
// take about as long as one, not N times as long.
func TestBroadcastConcurrent(t *testing.T) {
	const n = 4
	srv := peerServer(t, "peer-1", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		answerHandler("peer-1", nil)(w, r)
	})

	peers := make([]Peer, 0, n)
	for i := 0; i < n; i++ {
		p := freshPeer("peer-1", "p", srv.URL)
		p.ID = uuid.Must(uuid.NewV7())
		peers = append(peers, p)
	}

	c := testClient(t, nil).WithPerPeerTimeout(3 * time.Second)
	start := time.Now()
	res, err := c.Broadcast(context.Background(), peers, goodQuestion())
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Broadcast: %v", err)
	}
	if len(res.Answers) != n {
		t.Errorf("got %d answers, want %d", len(res.Answers), n)
	}
	// Sequential would be 800ms. Allow generous headroom for a loaded host but
	// stay well under the sequential figure.
	if elapsed > 600*time.Millisecond {
		t.Errorf("broadcast of %d peers took %v; they were asked sequentially", n, elapsed)
	}
}

// TestBroadcastRefusesDirtyQuestionIsTheF1Guard is a mutation-resistant restatement
// of why the guard call in Broadcast is load-bearing.
//
// The line it replaced read `if err := error(nil); err != nil` -- a condition
// that is false for every possible input. It compiled clean, it returned no
// error, and it protected nothing: a question carrying /etc/passwd went to every
// askable peer. It survived because the test that would have caught it lived in
// a file that was never compiled.
//
// This restates the property rather than repeating the test, because the test
// alone does not distinguish "the guard fires" from "the peer happened not to be
// asked". Here the peer IS askable and WOULD answer, and the assertion is that
// it was never contacted at all.
func TestBroadcastRefusesDirtyQuestionWithoutContactingAnyPeer(t *testing.T) {
	contacted := false
	srv := peerServer(t, "peer-1", func(w http.ResponseWriter, r *http.Request) {
		contacted = true
		answerHandler("peer-1", nil)(w, r)
	})

	peer := freshPeer("peer-1", "one", srv.URL)
	if !peer.Askable() {
		t.Fatal("test setup: the peer must be askable, or this proves nothing")
	}

	dirty := goodQuestion()
	dirty.Description = "tall, see /etc/passwd for details"

	c := testClient(t, nil)
	res, err := c.Broadcast(context.Background(), []Peer{peer}, dirty)

	if err == nil {
		t.Fatal("Broadcast accepted a question carrying a local path.\n\n" +
			"F1 exists so this repo never SENDS an identifier to a peer, and the " +
			"call site in Broadcast used to be `error(nil) != nil` -- always false.")
	}
	if contacted {
		t.Error("a peer was contacted while broadcasting a rejected question")
	}
	// A per-peer failure would ALSO mean the question reached the wire, just
	// somewhere else. Only a whole-broadcast refusal satisfies F1.
	if len(res.Failed) != 0 {
		t.Errorf("expected a broadcast-level refusal, got per-peer failures: %+v", res.Failed)
	}
}

// TestBroadcastRefusesToDialAnUnsafePeer is the dial-time guard's positive
// control AT THE CALL SITE.
//
// DialGuard has its own unit tests (dialguard_test.go), and those all pass with
// this line deleted -- because they test the function, not whether anything
// calls it. Two mutations confirmed that: removing the call entirely, and
// discarding its result, both left every test in this file green.
//
// That is the same class of defect as the F1 stub this commit fixes, one level
// up: a guard that exists, is well tested, and is never invoked. So the test has
// to be about the WIRING.
//
// The client below has NO resolver override, so DialGuard falls through to the
// real address judgement and refuses loopback -- exactly as it would refuse a
// peer row that rebound to 127.0.0.1 between registration and dial.
func TestBroadcastRefusesToDialAnUnsafePeer(t *testing.T) {
	contacted := false
	srv := peerServer(t, "peer-1", func(w http.ResponseWriter, r *http.Request) {
		contacted = true
		answerHandler("peer-1", nil)(w, r)
	})

	peer := freshPeer("peer-1", "one", srv.URL)
	if !peer.Askable() {
		t.Fatal("test setup: the peer must be askable, or the dial-time guard " +
			"would never be reached and this test would pass vacuously")
	}

	// NewClient with no WithResolver: production behaviour, live resolution.
	res, err := NewClient(nil).Broadcast(context.Background(), []Peer{peer}, goodQuestion())
	if err != nil {
		t.Fatalf("a per-peer refusal must not fail the whole broadcast: %v", err)
	}
	if contacted {
		t.Error("the peer was dialled. R074 rule 2 requires the address to be " +
			"re-checked at dial time, not only when the row was written.")
	}
	if len(res.Answers) != 0 {
		t.Errorf("got %d answer(s) from a peer that should have been refused", len(res.Answers))
	}
	// The refusal has to be REPORTED, not swallowed: a silently dropped peer is
	// indistinguishable from a peer that had nothing to say.
	if len(res.Failed) != 1 {
		t.Fatalf("expected exactly one peer failure, got %d: %+v", len(res.Failed), res.Failed)
	}
	if !strings.Contains(res.Failed[0].Reason, "refused") {
		t.Errorf("the failure reason does not say the peer was refused: %q", res.Failed[0].Reason)
	}
}

// TestBroadcastSkipsAPeerWithNoTrustWeight covers the trust gate at the DIAL
// site.
//
// TrustWeight's range is already tested on the write path
// (TestCreateRejectsOutOfRangeTrustWeight), and the mutation that drops the gate
// from Askable() survived every test here -- because the range check upstream
// does not imply the gate is applied when deciding who to ask. A row written
// before the range rule existed, or written by another tool, would be dialled.
func TestBroadcastSkipsAPeerWithNoTrustWeight(t *testing.T) {
	for _, weight := range []float64{0, -0.5, 1.5} {
		asked := 0
		srv := peerServer(t, "peer-1", func(w http.ResponseWriter, r *http.Request) {
			asked++
			answerHandler("peer-1", nil)(w, r)
		})

		peer := freshPeer("peer-1", "one", srv.URL)
		peer.TrustWeight = weight

		c := testClient(t, nil)
		if _, err := c.Broadcast(context.Background(), []Peer{peer}, goodQuestion()); err != nil {
			t.Fatalf("Broadcast: %v", err)
		}
		if asked != 0 {
			t.Errorf("a peer with TrustWeight %v was dialled %d time(s); the gate "+
				"is (0, 1]", weight, asked)
		}
	}
}
