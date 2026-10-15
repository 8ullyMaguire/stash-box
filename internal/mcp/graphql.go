package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is the instance, as this package needs it: one GraphQL endpoint and one API key.
//
// The key is a FIELD, not a log line. It is never printed, and an error from the instance is passed through
// as the instance phrased it -- this server does not re-word "not authorized" into anything of its own,
// because the decision was not its to make or to explain away.
type Client struct {
	URL    string
	APIKey string
	HTTP   *http.Client
}

func NewClient(url, apiKey string) *Client {
	return &Client{
		URL:    url,
		APIKey: apiKey,
		// A tool call is a person waiting for a model that is waiting for a lookup, so the timeout is
		// generous and still finite: a hung request must end in an error the model can report rather than
		// in a client that appears to be thinking.
		HTTP: &http.Client{Timeout: 60 * time.Second},
	}
}

// InstanceError is an error the INSTANCE gave, with its own message.
//
// A separate type so the server can tell "the instance refused or failed this" from "this server could not
// build the request", and report both faithfully: the first is the instance's answer and the second is a
// defect here.
type InstanceError struct {
	Messages []string
}

func (e *InstanceError) Error() string {
	return strings.Join(e.Messages, "; ")
}

// graphQLResponse is the shape this instance answers with. `errors` is read even when data is present:
// GraphQL allows a partial answer, and a tool that returned the partial half as a success would be
// answering a question with however much of it happened to survive.
type graphQLResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// Do runs one query and decodes its `data` into out.
func (c *Client) Do(ctx context.Context, query string, variables map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return fmt.Errorf("building the request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("building the request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// The instance's API keys travel in this header (see internal/api): a client that put the key in the
	// body would be ignored rather than refused, and the failure would read as "not authorized".
	if c.APIKey != "" {
		req.Header.Set("ApiKey", c.APIKey)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("reaching the instance at %s: %w", c.URL, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return fmt.Errorf("reading the instance's answer: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// A non-200 from the GraphQL endpoint is a fact worth naming exactly: 401 means the key was
		// refused, and 429 means too many calls -- two different next actions.
		return fmt.Errorf("the instance answered HTTP %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}

	var decoded graphQLResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return fmt.Errorf("the instance's answer was not JSON: %s", truncate(string(raw), 300))
	}

	if len(decoded.Errors) > 0 {
		messages := make([]string, 0, len(decoded.Errors))
		for _, e := range decoded.Errors {
			messages = append(messages, e.Message)
		}
		return &InstanceError{Messages: messages}
	}

	if out == nil {
		return nil
	}
	if err := json.Unmarshal(decoded.Data, out); err != nil {
		return fmt.Errorf("decoding the answer to %T: %w", out, err)
	}
	return nil
}

// IsInstanceError reports whether the instance itself refused or failed the request, as opposed to this
// server being unable to make it. Used only for the wording of what the model is told.
func IsInstanceError(err error) bool {
	var ie *InstanceError
	return errors.As(err, &ie)
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
