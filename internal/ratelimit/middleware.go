package ratelimit

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// THE REFUSAL IS AN ORDINARY ERROR OF THIS API, and the plan says why it is worth a slice: this repo answers
// every GraphQL error as {"errors":[{"message": ...}]}, so a 429 that returns HTML, a bare string or an empty
// body is a client bug reported as a server mystery. A client that already parses that shape needs no special
// case to read this one -- it needs a status code it can act on and a Retry-After it can obey.
//
// The message states the limit and the wait, because the caller of a rate limiter is usually a script somebody
// wrote, and the person debugging it is reading exactly one line.
func refusedBody(decision Decision) []byte {
	body, err := json.Marshal(map[string]any{
		"errors": []map[string]any{{
			"message": fmt.Sprintf(
				"Too many requests: this instance allows %d requests per minute per API key, and this one is "+
					"over that. Retry in %d seconds.",
				decision.Limit, int(decision.RetryAfter.Seconds())),
		}},
	})
	if err != nil {
		// Unreachable with these types, and a limiter that panics on its own error path is worse than one
		// that answers something a client can still read.
		return []byte(`{"errors":[{"message":"Too many requests."}]}`)
	}
	return body
}

// Credential reads the identity to count from a request: the API key header this instance authenticates with,
// the feed token in a feed URL, or -- when the caller presents neither -- the connecting address.
//
// IT IS A PARAMETER RATHER THAN A CONSTANT because this package should not know which header this instance
// uses; the wiring supplies it, and the same middleware then counts a GraphQL caller and a feed reader without
// either surface's naming leaking in here.
func Credential(r *http.Request) string {
	for _, header := range []string{"ApiKey", "X-Api-Key", "Authorization"} {
		if value := strings.TrimSpace(r.Header.Get(header)); value != "" {
			// A bearer prefix is part of the syntax, not part of the credential.
			return header + ":" + strings.TrimPrefix(value, "Bearer ")
		}
	}
	return "address:" + clientAddress(r)
}

// FeedToken counts a feed reader by the token in its URL, which is the credential that URL IS: a reader sends
// no headers, so the address is the identity, and counting it as an anonymous address would put every reader
// behind one proxy into one bucket.
func FeedToken(pathPrefix string) func(*http.Request) string {
	return func(r *http.Request) string {
		token := strings.TrimPrefix(r.URL.Path, pathPrefix)
		if token == "" || strings.Contains(token, "/") {
			// Not a token at all: the request is a scanner's, and it is counted by address so that a scan of
			// random paths shares one bucket.
			return "address:" + clientAddress(r)
		}
		return "feed:" + token
	}
}

func clientAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// Middleware refuses the requests over the limit and passes the rest through, stating the limit on every
// response -- including the ones that are allowed, because a client that only learns the limit when it is
// refused is a client that will be refused again.
func Middleware(limiter *Limiter, credential func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if limiter == nil {
				next.ServeHTTP(w, r)
				return
			}

			decision := limiter.AllowNow(credential(r))
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(decision.Limit))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(decision.Remaining))

			if !decision.Allowed {
				body := refusedBody(decision)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", strconv.Itoa(int(decision.RetryAfter.Seconds())))
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write(body)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// PostureLine states the limit this process is running, for the startup log -- the same reason the content
// postures are logged there: an operator's question is "which posture is THIS instance in", and a limit that a
// process never mentions is a limit nobody can tell was ignored.
func PostureLine(limiter *Limiter) string {
	config := limiter.Config()
	if config.Disabled() {
		return "rate limit: NONE -- this instance refuses no caller, however fast it goes"
	}
	return fmt.Sprintf("rate limit: %d requests per minute per credential, burst %d",
		config.RequestsPerMinute, config.Burst)
}
