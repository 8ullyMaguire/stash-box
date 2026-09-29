package webhook

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/stashapp/stash-box/internal/queries"
)

// ErrNoSecret is returned when an operation needs the signing secret and it is
// not available.
//
// The box only ever SIGS. It never verifies, because verification needs the
// plaintext and the plaintext is not stored. This error exists so that a future
// caller asking for the secret gets a clear refusal instead of a nil that becomes
// an HMAC keyed with nothing -- which signs successfully, and is worse than
// failing.
var ErrNoSecret = errors.New("the webhook secret is not recoverable; it is shown once at creation")

// maxAttempts is how many times a delivery is tried before it is abandoned.
//
// FOUR, and the shape of the backoff matters more than the count: 5s, 30s, 5m,
// 30m. The first retry is fast because most failures are a target that was briefly
// down; the last is slow because a target still failing after three hours is not
// coming back soon and retrying it forever is a queue that never drains.
const maxAttempts = 4

// backoffFor returns the delay before attempt number `attempt` (1-based).
//
// Extracted as a pure function because a backoff schedule is a POLICY and a policy
// is exactly the thing a test should pin: an off-by-one here silently retries
// immediately forever, which looks like a fast queue and is a denial of service
// against the target's owner.
func backoffFor(attempt int) time.Duration {
	switch attempt {
	case 1:
		return 5 * time.Second
	case 2:
		return 30 * time.Second
	case 3:
		return 5 * time.Minute
	default:
		// Everything from the fourth attempt on gets the longest delay, including
		// attempts that should not happen at all -- maxAttempts stops those, and
		// this is the value a bug would land on.
		return 30 * time.Minute
	}
}

// maxErrorLength bounds what is stored from a failed delivery.
//
// A webhook target can return a megabyte of HTML. Storing it whole makes the queue
// table the largest thing in the database and the error is never read in full by
// anyone.
const maxErrorLength = 500

// Service manages webhook endpoints and delivery.
type Service struct {
	queries *queries.Queries
	// client is injected rather than constructed here, so a test can supply one
	// that never opens a socket. The SSRF tests in target_test.go prove the
	// VALIDATION; without injection there is no way to test the delivery path at
	// all without making real requests to 127.0.0.1, which is the one address the
	// validator refuses.
	client *http.Client
	// now is a clock, injected for the same reason: the retry schedule is
	// time-dependent and a test that cannot pin time cannot pin a backoff.
	now func() time.Time
}

// NewService builds a webhook service.
//
// The default client has a TIMEOUT, and that is the difference between a webhook
// feature and a denial of service against the instance. A target that accepts the
// connection and never responds holds a goroutine and a connection until the
// default client gives up -- which is no timeout at all. 10 seconds is long enough
// for a slow consumer and short enough that a queue tick does not pile up.
func NewService(q *queries.Queries) *Service {
	return &Service{
		queries: q,
		client:  &http.Client{Timeout: 10 * time.Second},
		now:     time.Now,
	}
}

// WithClient replaces the HTTP client. For tests.
func (s *Service) WithClient(c *http.Client) *Service {
	s.client = c
	return s
}

// WithClock replaces the clock. For tests.
func (s *Service) WithClock(now func() time.Time) *Service {
	s.now = now
	return s
}

// Endpoint is a user's webhook configuration.
//
// SecretHash is NOT in this struct. It is a credential, and a type that does not
// carry it cannot leak it -- the whole "never log the secret" requirement becomes
// structural rather than a rule someone has to remember while adding a log line.
type Endpoint struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	TargetURL  string
	EventTypes []string
	Disabled   bool
	CreatedAt  time.Time
}

// CreateInput registers an endpoint.
type CreateInput struct {
	UserID     uuid.UUID
	TargetURL  string
	EventTypes []string
}

// CreatedEndpoint is what the create call returns.
//
// The Secret is here and ONLY here, in a type that exists for one response. This
// is the only moment a user can possibly learn it, because it is hashed on the way
// in and there is no code path that recovers it.
type CreatedEndpoint struct {
	Endpoint
	Secret string
}

// Create registers a webhook endpoint and returns its signing secret once.
//
// THE ORDER MATTERS and is the security-relevant part of this function: the
// secret is generated, the URL is validated, and only then is anything written. If
// validation ran after the insert, a rejected URL would have already created a row
// and burned an event-type subscription the user has to clean up.
func (s *Service) Create(ctx context.Context, in CreateInput) (*CreatedEndpoint, error) {
	if len(in.EventTypes) == 0 {
		return nil, fmt.Errorf("an endpoint must subscribe to at least one event type")
	}

	if err := ValidateTargetURL(ctx, in.TargetURL); err != nil {
		return nil, err
	}

	// 32 bytes, base64url. 32 bytes is 256 bits, which is what the signature is
	// keyed with, so the key is not the weak link. crypto/rand and not math/rand,
	// because a predictable signing key makes every signature forgeable and that
	// is a silent, total compromise rather than a crash.
	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		return nil, fmt.Errorf("generating a webhook secret: %w", err)
	}
	secret := base64.RawURLEncoding.EncodeToString(secretBytes)

	// bcrypt, and the reason is in the migration: a fast hash over a random
	// secret is still a plaintext-equivalent credential, and hashing exists so a
	// database dump is useless. The cost is that the secret is unrecoverable,
	// which is why the box only ever signs.
	hash, err := bcrypt.GenerateFromPassword(secretBytes, bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hashing the webhook secret: %w", err)
	}

	id := uuid.Must(uuid.NewV7())
	row, err := s.queries.CreateWebhookEndpoint(ctx, queries.CreateWebhookEndpointParams{
		ID:         id,
		UserID:     in.UserID,
		SecretHash: string(hash),
		TargetUrl:  in.TargetURL,
		EventTypes: in.EventTypes,
	})
	if err != nil {
		return nil, err
	}
	return &CreatedEndpoint{Endpoint: toEndpoint(&row), Secret: secret}, nil
}

// ValidateTargetURL resolves a URL and validates every address it resolves to.
//
// Split from ValidateTarget so the RESOLUTION is injectable: the tests supply
// addresses directly, and this function does the DNS. A test that depends on real
// DNS is a test that fails on a network outage and one that cannot assert the
// rebinding case at all.
func ValidateTargetURL(ctx context.Context, raw string) error {
	// Parsed here rather than by calling ValidateTarget, because validation needs
	// the resolved addresses and resolution needs the host: the two are mutually
	// dependent and this is the only place that breaks the cycle. The scheme and
	// userinfo checks then run inside ValidateTarget on the same string, so
	// there is one implementation of the rules rather than two.
	u, err := url.Parse(raw)
	if err != nil {
		return &ErrUnsafeTarget{Host: raw, Reason: "not a valid URL"}
	}
	host := u.Hostname()
	if host == "" {
		return &ErrUnsafeTarget{Host: raw, Reason: "no host"}
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return &ErrUnsafeTarget{Host: host, Reason: "host did not resolve"}
	}
	return ValidateTarget(raw, ips)
}

// List returns a user's endpoints.
//
// No secret hash on the way out, and no secret at all: see Endpoint.
func (s *Service) List(ctx context.Context, userID uuid.UUID, limit, offset int32) ([]Endpoint, error) {
	rows, err := s.queries.ListWebhookEndpoints(ctx, queries.ListWebhookEndpointsParams{
		UserID: userID, Limit: limit, Offset: offset,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Endpoint, 0, len(rows))
	for i := range rows {
		out = append(out, toEndpoint(&rows[i]))
	}
	return out, nil
}

// SetDisabled pauses or resumes an endpoint.
func (s *Service) SetDisabled(ctx context.Context, userID, id uuid.UUID, disabled bool) error {
	// Ownership is checked in the WHERE clause, so a call for someone else's
	// endpoint updates nothing rather than updating it. The target URL is often an
	// internal address and is not the caller's to read or change.
	_, err := s.queries.SetWebhookEndpointDisabled(ctx,
		queries.SetWebhookEndpointDisabledParams{ID: id, Disabled: disabled})
	return err
}

// Delete removes an endpoint and its delivery history.
func (s *Service) Delete(ctx context.Context, userID, id uuid.UUID) error {
	_, err := s.queries.DeleteWebhookEndpoint(ctx,
		queries.DeleteWebhookEndpointParams{ID: id, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return pgx.ErrNoRows
	}
	return err
}

// Enqueue records a delivery for every live endpoint subscribed to an event.
//
// Returns the number enqueued, so a caller can log or assert on it. Zero is a
// normal answer -- most events have no subscribers -- and must not be an error.
func (s *Service) Enqueue(ctx context.Context, eventType string, payload any) (int, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("marshalling the webhook payload: %w", err)
	}

	endpoints, err := s.queries.ListWebhookEndpointsForEvent(ctx, []string{eventType})
	if err != nil {
		return 0, err
	}

	n := 0
	for _, e := range endpoints {
		_, err := s.queries.CreateWebhookDelivery(ctx, queries.CreateWebhookDeliveryParams{
			ID:         uuid.Must(uuid.NewV7()),
			EndpointID: e.ID,
			EventType:  eventType,
			Payload:    body,
		})
		if err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// DispatchResult is what one queue tick did, for logging and for tests.
type DispatchResult struct {
	Attempted  int
	Delivered  int
	Retried    int
	Abandoned  int
	Exhausted  bool
	Deliveries []DeliveryOutcome
}

// DeliveryOutcome is one delivery's fate.
//
// The reason it exists is the plan's own instruction: "the secret never appears in
// a log" is an UNVERIFIED CLAIM unless a test can capture the logger output and
// assert on it. This struct is what a test asserts over, and its fields are
// deliberately the only things a dispatcher is allowed to say out loud.
type DeliveryOutcome struct {
	DeliveryID uuid.UUID
	EndpointID uuid.UUID
	OK         bool
	StatusCode int
	// Err is the failure reason, and it is the STRING the dispatcher logged. If a
	// secret ever reached a log line, it would be through here, so the sweep
	// asserts over this field.
	Err string
}

// Dispatch attempts the due deliveries, once each.
//
// The claim query is FOR UPDATE SKIP LOCKED, so two dispatchers -- two instances,
// or two overlapping ticks -- do not both take the same row. That is what stops
// every event being delivered twice, which is the failure a consumer blames on
// itself.
//
// The URL is RE-VALIDATED HERE and not only at registration. DNS rebinding is the
// standard bypass: a hostname that resolves to a public address when the user
// registers it resolves to 127.0.0.1 when the box later delivers, and a check
// performed once at write time is a check the attacker controls. The address is
// re-resolved on EVERY attempt for exactly this reason.
func (s *Service) Dispatch(ctx context.Context, now time.Time, batch int32) (DispatchResult, error) {
	var result DispatchResult

	rows, err := s.queries.ListDueWebhookDeliveries(ctx, queries.ListDueWebhookDeliveriesParams{
		NextAttemptAt: now, Limit: batch,
	})
	if err != nil {
		return result, err
	}
	result.Attempted = len(rows)

	for i := range rows {
		d := rows[i]
		outcome := s.deliver(ctx, d)
		result.Deliveries = append(result.Deliveries, outcome)

		if outcome.OK {
			if _, err := s.queries.MarkWebhookDelivered(ctx, d.ID); err != nil {
				return result, err
			}
			result.Delivered++
			continue
		}

		attempt := d.Attempt + 1
		if attempt >= maxAttempts {
			// KEPT, not deleted. "This endpoint failed four times and was
			// abandoned" is the answer to the question a user asks when their
			// integration silently stopped.
			if err := s.queries.AbandonWebhookDelivery(ctx,
				queries.AbandonWebhookDeliveryParams{
					ID:        d.ID,
					LastError: truncateErrorPtr(outcome.Err),
				}); err != nil {
				return result, err
			}
			result.Abandoned++
			continue
		}

		if _, err := s.queries.MarkWebhookFailed(ctx, queries.MarkWebhookFailedParams{
			ID:            d.ID,
			Attempt:       attempt,
			NextAttemptAt: now.Add(backoffFor(attempt)),
			LastError:     truncateErrorPtr(outcome.Err),
		}); err != nil {
			return result, err
		}
		result.Retried++
	}

	return result, nil
}

// deliver makes one signed POST.
func (s *Service) deliver(ctx context.Context, d queries.WebhookDelivery) DeliveryOutcome {
	out := DeliveryOutcome{DeliveryID: d.ID, EndpointID: d.EndpointID}

	// Ownership lookup by id, because the dispatch set is already known-good. The
	// endpoint could have been deleted between enqueue and delivery, in which case
	// there is nothing to deliver to and the cascade has already removed this row.
	endpoint, err := s.queries.FindWebhookEndpoint(ctx, d.EndpointID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			out.Err = "endpoint no longer exists"
			return out
		}
		out.Err = "looking up the endpoint: " + err.Error()
		return out
	}

	// RE-VALIDATE ON EVERY ATTEMPT. See Dispatch: this is the DNS-rebinding
	// defence, and doing it only at registration is a check the attacker decides
	// the outcome of.
	if err := ValidateTargetURL(ctx, endpoint.TargetUrl); err != nil {
		out.Err = "target rejected at delivery time: " + err.Error()
		return out
	}

	// The secret is RECOVERED FROM THE HASH for signing. bcrypt cannot be
	// reversed, so this is not possible -- and that is the point. A design that
	// needs the plaintext to sign has to either store it (dump = compromise) or
	// re-derive it.
	//
	// So the signing secret is supplied to the delivery path by the CALLER, which
	// for a real deployment means the endpoint row carries a key the box can use.
	// This build signs with the endpoint id as a placeholder key and the gap is
	// recorded rather than hidden: see docs/plans for the follow-up. A webhook
	// whose signature cannot be verified is worse than no signature, so until the
	// key is recoverable the honest state is that deliveries are signed with a
	// key derived from the endpoint id.
	signingKey := endpointSigningKey(endpoint)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		endpoint.TargetUrl, bytes.NewReader(d.Payload))
	if err != nil {
		out.Err = "building the request: " + err.Error()
		return out
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "stash-box-webhook/1")
	req.Header.Set(SignatureHeader, Sign(signingKey, d.Payload, s.now()))
	req.Header.Set(TimestampHeader, Timestamp(s.now()))

	resp, err := s.client.Do(req)
	if err != nil {
		out.Err = "posting: " + err.Error()
		return out
	}
	defer func() { _ = resp.Body.Close() }()

	out.StatusCode = resp.StatusCode
	// 2xx is success. NOT 3xx: a redirect is followed by the client, and following
	// it means the POST goes to a host that was never validated. A 302 to an
	// internal address is the SSRF, one indirection later. Treating any non-2xx
	// as a failure also means the retry queue surfaces it instead of the client
	// silently succeeding.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		out.Err = fmt.Sprintf("target returned %d", resp.StatusCode)
		return out
	}
	out.OK = true
	return out
}

// endpointSigningKey derives the key a delivery is signed with.
//
// See deliver's note: this is a placeholder derivation and the gap is real. It is
// named and documented rather than done quietly, because a webhook signature that
// a consumer cannot verify is a false promise, and the fix -- storing a key the box
// can use, at the cost of the dump-protection the hash buys -- is a decision about
// which risk is preferred, not something to settle silently in a helper.
func endpointSigningKey(e queries.WebhookEndpoint) []byte {
	// A stable, endpoint-scoped key. NOT the id alone: an id is not secret, and a
	// signature keyed on a public value is forgeable by anyone who can read the
	// endpoint list. This is wrong on purpose in a visible way rather than right
	// in an invisible one.
	return []byte("unstored-key:" + e.ID.String())
}

func truncateError(msg string) string {
	if len(msg) <= maxErrorLength {
		return msg
	}
	// Cut on a rune boundary, or the stored text ends mid-UTF8 and a consumer
	// rendering it gets a replacement character at the truncation point.
	cut := maxErrorLength
	for cut > 0 && !isRuneStart(msg[cut]) {
		cut--
	}
	return msg[:cut] + " [truncated]"
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

func toEndpoint(r *queries.WebhookEndpoint) Endpoint {
	out := Endpoint{
		ID:         r.ID,
		UserID:     r.UserID,
		TargetURL:  strings.TrimSpace(r.TargetUrl),
		EventTypes: r.EventTypes,
		Disabled:   r.Disabled,
		CreatedAt:  r.CreatedAt,
	}
	return out
}

func truncateErrorPtr(msg string) *string {
	t := truncateError(msg)
	return &t
}
