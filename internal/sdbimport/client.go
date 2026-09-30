// Package sdbimport extracts records from a stash-box-compatible source
// (stashdb.org) and writes them into a local stash-box through its service
// layer.
//
// WHY A SEPARATE BINARY AND NOT DIRECT SQL. The obvious approach — COPY rows
// into Postgres — is much faster and much worse. It bypasses the service layer,
// which means:
//
//   - no de-duplication, so re-running duplicates every row;
//   - no fingerprint handling for scenes, so scene search-by-fingerprint (the
//     single most important feature of the instance) finds nothing;
//   - no alias rows, so tag/studio alias search silently returns nothing;
//   - no relation rows, so a scene's performers and tags do not exist.
//
// The result is rows in a table and an instance that behaves as if empty. Every
// one of those failures is invisible to a row count, which is the trap this
// design exists to avoid.
//
// IDEMPOTENCE. Everything is keyed on a natural key (name, or name +
// disambiguation for performers) and every write is create-or-skip, never
// blind-insert. Re-running after a partial failure is safe and resumes rather
// than duplicating.
//
// ORDER MATTERS. Tags, sites, studios and performers are written before scenes
// because scenes reference them by id. Scenes are last and are the bulk of the
// volume.
package sdbimport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to a stash-box GraphQL API.
//
// Deliberately minimal: one endpoint, one query at a time. Batch this and the
// first thing that breaks is the source's response time on a query that already
// returns 5,000 records.
type Client struct {
	endpoint string
	cookies  []*http.Cookie
	http     *http.Client

	// Requests counts queries issued, so a long run can report progress without
	// a second source of truth.
	Requests int
}

// NewClient returns a client for the given GraphQL endpoint.
func NewClient(endpoint string) *Client {
	return &Client{
		endpoint: endpoint,
		// A generous timeout: a 5,000-record scene query is legitimately slow,
		// and a short timeout turns a slow success into a retry storm against a
		// third-party API.
		http: &http.Client{Timeout: 5 * time.Minute},
	}
}

// Authenticate performs a form login and keeps the session cookie.
//
// stash-box has no login mutation. The UI POSTs to /login and the response
// sets the session cookie that the GraphQL handler authorises against; HTTP
// Basic is NOT accepted by the API even though the same credentials work for
// the web form.
func (c *Client) Authenticate(ctx context.Context, loginURL, user, pass string) error {
	form := url.Values{"username": {user}, "password": {pass}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, loginURL, strings.NewReader(form))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("login request: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("login returned HTTP %d, want 200", resp.StatusCode)
	}

	c.cookies = resp.Cookies()
	if len(c.cookies) == 0 {
		// A 200 with no cookie is a silent auth failure: the source serves its
		// SPA shell for any path, so a mistyped URL 200s and looks like success
		// right up until every query returns "not authorized".
		return fmt.Errorf("login succeeded with no session cookie; the URL is probably not the login endpoint")
	}
	return nil
}

// Query runs one GraphQL query and decodes the result into out.
//
// GraphQL errors are returned as errors, never swallowed: a stash-box API
// answers a failed query with HTTP 200 and an `errors` array, so a client that
// only checks the status code reports success on a rejected query and imports
// nothing while claiming it imported everything.
func (c *Client) Query(ctx context.Context, query string, vars map[string]any, out any) error {
	payload := map[string]any{"query": query}
	if vars != nil {
		payload["variables"] = vars
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for _, ck := range c.cookies {
		req.AddCookie(ck)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("graphql request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("graphql returned HTTP %d", resp.StatusCode)
	}

	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("decoding graphql response: %w", err)
	}
	if len(envelope.Errors) > 0 {
		return fmt.Errorf("graphql error: %s", envelope.Errors[0].Message)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(envelope.Data, out)
}

// Paged is one page of a paged query result.
type Paged[T any] struct {
	Count      int `json:"count"`
	PerPage    int `json:"per_page"`
	Page       int `json:"page"`
	NumPages   int `json:"num_pages"`
	Performers []T `json:"performers"`
	Scenes     []T `json:"scenes"`
	Tags       []T `json:"tags"`
	Studios    []T `json:"studios"`
	Sites      []T `json:"sites"`
}
