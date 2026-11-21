// Package openai is an embedder for any OpenAI-compatible /v1/embeddings
// endpoint, such as transponder, OpenAI, Ollama or vLLM.
package openai

import (
	"context"
	"fmt"
	"strconv"

	"github.com/raven-clown/logbook/embed"
	"github.com/raven-clown/logbook/internal/oai"
)

func init() {
	embed.Register("openai", func(c embed.Config) (embed.Embedder, error) { return New(c) })
}

// Client calls POST {base_url}/embeddings.
type Client struct {
	ep         oai.Endpoint
	model      string
	dimensions int
}

// New returns a client for c.
func New(c embed.Config) (*Client, error) {
	if c.BaseURL == "" || c.Model == "" {
		return nil, fmt.Errorf("the openai embedder needs a base URL and a model")
	}
	return &Client{
		ep:    oai.Endpoint{BaseURL: c.BaseURL, APIKey: c.APIKey, ForwardToken: c.ForwardToken, Client: c.HTTPClient},
		model: c.Model, dimensions: c.Dimensions,
	}, nil
}

// Model is the model name, plus @dimensions when shorter vectors are asked for.
func (c *Client) Model() string {
	if c.dimensions > 0 {
		return c.model + "@" + strconv.Itoa(c.dimensions)
	}
	return c.model
}

type request struct {
	Model          string   `json:"model"`
	Input          []string `json:"input"`
	Dimensions     int      `json:"dimensions,omitempty"`
	EncodingFormat string   `json:"encoding_format"`
}

type response struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// Embed returns one vector per text, in input order.
func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	var resp response
	if err := c.ep.Post(ctx, "/embeddings", request{Model: c.model, Input: texts, Dimensions: c.dimensions, EncodingFormat: "float"}, &resp); err != nil {
		return nil, err
	}
	if len(resp.Data) != len(texts) {
		return nil, fmt.Errorf("%w: asked for %d embeddings, got %d", oai.ErrShape, len(texts), len(resp.Data))
	}
	out := make([][]float32, len(texts))
	for _, d := range resp.Data {
		if d.Index < 0 || d.Index >= len(out) || out[d.Index] != nil || len(d.Embedding) == 0 {
			return nil, fmt.Errorf("%w: embedding index %d is missing, repeated or empty", oai.ErrShape, d.Index)
		}
		out[d.Index] = d.Embedding
	}
	return out, nil
}
