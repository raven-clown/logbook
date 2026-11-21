package oai_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/raven-clown/logbook/internal/oai"
)

func TestPost(t *testing.T) {
	var path, ctype, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		path, ctype, body = r.URL.Path, r.Header.Get("Content-Type"), string(b)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	var out struct{ OK bool }
	err := oai.Endpoint{BaseURL: srv.URL + "/v1/"}.Post(context.Background(), "/things", map[string]int{"n": 1}, &out)
	if err != nil || !out.OK || path != "/v1/things" || ctype != "application/json" || body != `{"n":1}` {
		t.Fatalf("Post = %v, ok %v, path %q, type %q, body %q", err, out.OK, path, ctype, body)
	}
}

func TestPostErrors(t *testing.T) {
	long := strings.Repeat("x", 400)
	for _, tt := range []struct {
		name, answer, want string
		status             int
	}{
		{"error message", `{"error":{"message":"rate limited"}}`, "429: rate limited", 429},
		{"long body cut", long, strings.Repeat("x", 300) + "...", 500},
		{"empty body", ``, "no message", 503},
		{"not json", `nope`, "not valid JSON", 200},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.answer))
			}))
			t.Cleanup(srv.Close)
			var out any
			err := oai.Endpoint{BaseURL: srv.URL}.Post(context.Background(), "/x", "secret", &out)
			if err == nil || !strings.Contains(err.Error(), tt.want) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("err = %v, want one containing %q", err, tt.want)
			}
		})
	}
}

func TestPostUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	var out any
	if err := (oai.Endpoint{BaseURL: url}).Post(context.Background(), "/x", 1, &out); err == nil {
		t.Fatal("a closed server answered")
	}
	if err := (oai.Endpoint{BaseURL: "http://bad host"}).Post(context.Background(), "/x", 1, &out); err == nil {
		t.Fatal("a bad URL was accepted")
	}
}
