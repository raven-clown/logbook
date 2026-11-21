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
// budgets through the context. It follows no redirects, so Authorization is
// never carried to another URL, such as from https down to http.
var DefaultClient = &http.Client{Timeout: 60 * time.Second, CheckRedirect: noRedirect}

func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// Endpoint is one OpenAI-compatible base URL and how to sign in to it.
type Endpoint struct {
	BaseURL      string
	APIKey       string
	ForwardToken bool
	Client       *http.Client
}

// Post sends body as JSON to path under the base URL and decodes the answer
// into out. Errors name the status and the endpoint's error code, never its
// message or the request body, since an endpoint may echo memory text back.
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
	client := DefaultClient
	if e.Client != nil {
		c := *e.Client
		c.CheckRedirect = noRedirect
		client = &c
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
		return fmt.Errorf("%s answered %d (%s)", url, resp.StatusCode, errorCode(data))
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("the answer from %s is not valid JSON: %w", url, err)
	}
	return nil
}

// knownCodes are the OpenAI-style error codes and types an error may name.
// Anything else stays out, since an endpoint can put echoed input in any field.
var knownCodes = map[string]bool{
	"rate_limit_exceeded": true, "insufficient_quota": true, "invalid_api_key": true, "model_not_found": true,
	"context_length_exceeded": true, "invalid_request_error": true, "authentication_error": true,
	"permission_error": true, "not_found_error": true, "rate_limit_error": true, "server_error": true,
	"api_error": true, "overloaded_error": true, "timeout": true,
}

// errorCode returns the endpoint's error code or type when it is a known one.
func errorCode(data []byte) string {
	var e struct {
		Error struct {
			Code any `json:"code"`
			Type any `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &e) == nil {
		for _, v := range []any{e.Error.Code, e.Error.Type} {
			if s, ok := v.(string); ok && knownCodes[s] {
				return s
			}
		}
	}
	return "no known error code"
}

// ErrShape is wrapped by errors about an answer that has the wrong shape.
var ErrShape = errors.New("unexpected answer")
