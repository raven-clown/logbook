package oai_test

import (
	"context"
	"encoding/json"
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
	for _, tt := range []struct {
		name, answer, want string
		status             int
	}{
		{"error code", `{"error":{"message":"slow down","code":"rate_limit_exceeded"}}`, "429 (rate_limit_exceeded)", 429},
		{"error type", `{"error":{"message":"x","type":"server_error","code":null}}`, "500 (server_error)", 500},
		{"plain body", `upstream down`, "502 (no known error code)", 502},
		{"empty body", ``, "503 (no known error code)", 503},
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

func TestPostKeepsEchoedTextOutOfErrors(t *testing.T) {
	for _, field := range []string{"message", "code", "type"} {
		t.Run(field, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var in map[string]string
				_ = json.NewDecoder(r.Body).Decode(&in)
				b, _ := json.Marshal(map[string]any{"error": map[string]string{field: in["input"]}})
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write(b)
			}))
			t.Cleanup(srv.Close)
			var out any
			err := oai.Endpoint{BaseURL: srv.URL}.Post(context.Background(), "/x", map[string]string{"input": "vpn_password_hunter2"}, &out)
			if err == nil || strings.Contains(err.Error(), "hunter2") || !strings.Contains(err.Error(), "400 (no known error code)") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestPostFollowsNoRedirect(t *testing.T) {
	var got string
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r.Header.Get("Authorization") }))
	t.Cleanup(target.Close)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/x", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(srv.Close)
	client := srv.Client()
	var out any
	err := oai.Endpoint{BaseURL: srv.URL, APIKey: "sk-secret", Client: client}.Post(context.Background(), "/x", 1, &out)
	if err == nil || !strings.Contains(err.Error(), "307") || got != "" {
		t.Fatalf("err = %v, the http target saw Authorization %q", err, got)
	}
	if client.CheckRedirect != nil {
		t.Fatal("Post changed the caller's client")
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
