// Package oai posts JSON to OpenAI-compatible endpoints for the embedder and
// extractor clients.
package oai

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

	"github.com/raven-clown/logbook/internal/outbound"
)

// MaxBody is the most logbook reads from an endpoint's response.
const MaxBody = 16 << 20

// DefaultClient is used when a config names no HTTP client. Callers set tighter
// budgets through the context.
var DefaultClient = &http.Client{Timeout: 60 * time.Second}

// Endpoint is one OpenAI-compatible base URL and how to sign in to it.
type Endpoint struct {
	BaseURL      string
	APIKey       string
	ForwardToken bool
	Client       *http.Client
}

// Post sends body as JSON to path under the base URL and decodes the answer
// into out. Errors name the status and the endpoint's own message, never the
// request body.
func (e Endpoint) Post(ctx context.Context, path string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	url := strings.TrimSuffix(e.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	outbound.Apply(ctx, req.Header, e.APIKey, e.ForwardToken)
	client := e.Client
	if client == nil {
		client = DefaultClient
	}
	resp, err := client.Do(req) // #nosec G704 -- the URL comes from the operator's config
	if err != nil {
		return fmt.Errorf("calling %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody+1))
	if err != nil {
		return fmt.Errorf("reading the answer from %s: %w", url, err)
	}
	if len(data) > MaxBody {
		return fmt.Errorf("the answer from %s is larger than %d bytes", url, MaxBody)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%s answered %d: %s", url, resp.StatusCode, message(data))
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("the answer from %s is not valid JSON: %w", url, err)
	}
	return nil
}

func message(data []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &e) == nil && e.Error.Message != "" {
		return truncate(e.Error.Message)
	}
	return truncate(strings.TrimSpace(string(data)))
}

func truncate(s string) string {
	const max = 300
	if len(s) > max {
		return s[:max] + "..."
	}
	if s == "" {
		return "no message"
	}
	return s
}

// ErrShape is wrapped by errors about an answer that has the wrong shape.
var ErrShape = errors.New("unexpected answer")
