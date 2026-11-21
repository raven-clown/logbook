package openai_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/raven-clown/logbook/embed"
	"github.com/raven-clown/logbook/embed/openai"
	"github.com/raven-clown/logbook/internal/oai"
	"github.com/raven-clown/logbook/internal/outbound"
)

type seen struct {
	path, auth string
	body       map[string]any
}

func server(t *testing.T, status int, answer string) (*httptest.Server, *seen) {
	t.Helper()
	s := &seen{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.path, s.auth = r.URL.Path, r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&s.body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(srv.Close)
	return srv, s
}

func client(t *testing.T, url string, c embed.Config) embed.Embedder {
	t.Helper()
	c.BaseURL = url + "/v1"
	if c.Model == "" {
		c.Model = "text-embedding-3-small"
	}
	e, err := embed.Open("openai", c)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestEmbed(t *testing.T) {
	srv, s := server(t, 200, `{"data":[{"index":1,"embedding":[0,1]},{"index":0,"embedding":[1,0.5]}],"model":"x"}`)
	e := client(t, srv.URL, embed.Config{APIKey: "sk-test", Dimensions: 2})
	got, err := e.Embed(context.Background(), []string{"first", "second"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got[0], []float32{1, 0.5}) || !slices.Equal(got[1], []float32{0, 1}) {
		t.Fatalf("Embed = %v, want input order", got)
	}
	if s.path != "/v1/embeddings" || s.auth != "Bearer sk-test" {
		t.Fatalf("request = %s, %q", s.path, s.auth)
	}
	if s.body["model"] != "text-embedding-3-small" || s.body["dimensions"] != float64(2) || len(s.body["input"].([]any)) != 2 {
		t.Fatalf("body = %v", s.body)
	}
	if e.Model() != "text-embedding-3-small@2" {
		t.Fatalf("Model() = %q", e.Model())
	}
}

func TestEmbedNoDimensions(t *testing.T) {
	srv, s := server(t, 200, `{"data":[{"index":0,"embedding":[1]}]}`)
	e := client(t, srv.URL, embed.Config{})
	if _, err := e.Embed(context.Background(), []string{"x"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.body["dimensions"]; ok || e.Model() != "text-embedding-3-small" || s.auth != "" {
		t.Fatalf("body = %v, model %q, auth %q", s.body, e.Model(), s.auth)
	}
}

func TestEmbedForwardsToken(t *testing.T) {
	srv, s := server(t, 200, `{"data":[{"index":0,"embedding":[1]}]}`)
	e := client(t, srv.URL, embed.Config{APIKey: "sk-own", ForwardToken: true})
	ctx := outbound.WithToken(context.Background(), "caller.jwt.token")
	if _, err := e.Embed(ctx, []string{"x"}); err != nil {
		t.Fatal(err)
	}
	if s.auth != "Bearer caller.jwt.token" {
		t.Fatalf("Authorization = %q", s.auth)
	}
}

func TestEmbedErrors(t *testing.T) {
	for _, tt := range []struct {
		name, answer, want string
		status             int
	}{
		{"server error", `{"error":{"message":"model not loaded"}}`, "500: model not loaded", 500},
		{"plain error", `upstream down`, "upstream down", 502},
		{"too few", `{"data":[{"index":0,"embedding":[1]}]}`, "got 1", 200},
		{"repeated index", `{"data":[{"index":0,"embedding":[1]},{"index":0,"embedding":[1]}]}`, "index 0", 200},
		{"empty vector", `{"data":[{"index":0,"embedding":[]},{"index":1,"embedding":[1]}]}`, "index 0", 200},
		{"not json", `<html>`, "not valid JSON", 200},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := server(t, tt.status, tt.answer)
			_, err := client(t, srv.URL, embed.Config{}).Embed(context.Background(), []string{"secret text a", "secret text b"})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want one containing %q", err, tt.want)
			}
			if strings.Contains(err.Error(), "secret text") {
				t.Fatalf("error leaks the input: %v", err)
			}
		})
	}
}

func TestEmbedOversizeAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"index":0,"embedding":[`))
		chunk := []byte(strings.Repeat("0,", 1<<16))
		for written := 0; written <= oai.MaxBody; written += len(chunk) {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	_, err := client(t, srv.URL, embed.Config{}).Embed(context.Background(), []string{"x"})
	if err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("err = %v", err)
	}
}

func TestEmbedNothing(t *testing.T) {
	got, err := client(t, "http://127.0.0.1:1", embed.Config{}).Embed(context.Background(), nil)
	if err != nil || got != nil {
		t.Fatalf("Embed(nil) = %v, %v", got, err)
	}
}

func TestOpenErrors(t *testing.T) {
	if _, err := embed.Open("openai", embed.Config{}); err == nil {
		t.Fatal("no base url accepted")
	}
	if _, err := embed.Open("nope", embed.Config{}); err == nil || !strings.Contains(err.Error(), "openai") {
		t.Fatalf("unknown type = %v", err)
	}
	if _, err := openai.New(embed.Config{BaseURL: "http://x", Model: "m"}); err != nil {
		t.Fatal(err)
	}
}
