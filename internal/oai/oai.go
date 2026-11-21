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
	"regexp"
	"strings"
	"time"

	"github.com/raven-clown/logbook/internal/outbound"
)

// MaxBody is the most logbook reads from an endpoint's response.
const MaxBody = 16 << 20

// DefaultClient is used when a config names no HTTP client. Callers set tighter
// budgets through the context. It follows no redirects, so Authorization is
// never carried to another URL, such as from https down to http.
var DefaultClient = &http.Client{
	Timeout:       60 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

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
		return fmt.Errorf("%s answered %d (%s)", url, resp.StatusCode, errorCode(data))
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("the answer from %s is not valid JSON: %w", url, err)
	}
	return nil
}

var codeRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// errorCode returns the OpenAI-style error code or type, such as
// rate_limit_exceeded, when it is a short identifier.
func errorCode(data []byte) string {
	var e struct {
		Error struct {
			Code any    `json:"code"`
			Type string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &e) == nil {
		if c, ok := e.Error.Code.(string); ok && codeRe.MatchString(c) {
			return c
		}
		if codeRe.MatchString(e.Error.Type) {
			return e.Error.Type
		}
	}
	return "no error code"
}

// ErrShape is wrapped by errors about an answer that has the wrong shape.
var ErrShape = errors.New("unexpected answer")
