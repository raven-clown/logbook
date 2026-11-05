package mcpserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/raven-clown/logbook/internal/identity"
	"github.com/raven-clown/logbook/internal/mcpserver"
	"github.com/raven-clown/logbook/memory"
	"github.com/raven-clown/logbook/store/sqlite"
)

type httpEnv struct {
	url   string
	store memory.Store
}

func startHTTP(t *testing.T) *httpEnv {
	t.Helper()
	st, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "logbook.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	svc := memory.NewService(st, memory.Options{Now: func() time.Time { return time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC) }})
	h := mcpserver.HTTPHandler(mcpserver.New(svc, mcpserver.HTTPPrincipal, "test"), mcpserver.HTTPOptions{DefaultProject: "mothership"})
	fake := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sub := r.Header.Get("X-Test-Subject"); sub != "" {
			r = r.WithContext(identity.WithCaller(r.Context(), identity.Caller{Principal: memory.Principal{OrgID: memory.DefaultOrg, Subject: sub}}))
		}
		h.ServeHTTP(w, r)
	})
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	return &httpEnv{url: srv.URL + "/mcp", store: st}
}

func (e *httpEnv) client(t *testing.T, headers map[string]string) *client.Client {
	t.Helper()
	c, err := client.NewStreamableHttpClient(e.url, transport.WithHTTPHeaders(headers))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	req := mcp.InitializeRequest{}
	req.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	req.Params.ClientInfo = mcp.Implementation{Name: "agent-test", Version: "1"}
	if _, err := c.Initialize(ctx, req); err != nil {
		t.Fatal(err)
	}
	return c
}

func callHTTP(t *testing.T, c *client.Client, name string, args map[string]any, out any) {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	res, err := c.CallTool(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("%s failed: %+v", name, res.Content)
	}
	b, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(b, out); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPTools(t *testing.T) {
	e := startHTTP(t)
	alice := e.client(t, map[string]string{"X-Test-Subject": "alice"})
	var rem memory.RememberResult
	callHTTP(t, alice, "remember", map[string]any{"content": "the queue is rabbitmq"}, &rem)
	if rem.Memories[0].Scope != "project:mothership" {
		t.Fatalf("remember = %+v", rem)
	}
	m, _ := e.store.Get(context.Background(), memory.DefaultOrg, rem.Memories[0].ID)
	if m.Author.Subject != "alice" || m.Author.Channel != memory.ChannelMCP {
		t.Fatalf("author = %+v", m.Author)
	}
	bob := e.client(t, map[string]string{"X-Test-Subject": "bob"})
	var rec memory.RecallResult
	callHTTP(t, bob, "recall", map[string]any{"query": "queue"}, &rec)
	if len(rec.Results) != 1 {
		t.Fatalf("bob recall = %+v", rec)
	}
	callHTTP(t, bob, "remember", map[string]any{"content": "bob private", "scope": "user"}, &rem)
	callHTTP(t, alice, "recall", map[string]any{"query": "private"}, &rec)
	if len(rec.Results) != 0 {
		t.Fatalf("alice read bob's user scope: %+v", rec)
	}
}

func TestHTTPProjectHeader(t *testing.T) {
	e := startHTTP(t)
	c := e.client(t, map[string]string{"X-Test-Subject": "alice", "X-Project": "api"})
	var rem memory.RememberResult
	callHTTP(t, c, "remember", map[string]any{"content": "api uses grpc"}, &rem)
	if rem.Memories[0].Scope != "project:api" {
		t.Fatalf("scope = %s", rem.Memories[0].Scope)
	}
}

func TestHTTPBadProjectHeader(t *testing.T) {
	e := startHTTP(t)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, e.url, nil)
	req.Header.Set("X-Test-Subject", "alice")
	req.Header.Set("X-Project", "a b")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d", res.StatusCode)
	}
}

func TestHTTPNoCaller(t *testing.T) {
	e := startHTTP(t)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, e.url, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d", res.StatusCode)
	}
}

func TestHTTPClientName(t *testing.T) {
	e := startHTTP(t)
	c := e.client(t, map[string]string{"X-Test-Subject": "alice", "User-Agent": "editor/2"})
	var rem memory.RememberResult
	callHTTP(t, c, "remember", map[string]any{"content": "client names are recorded"}, &rem)
	m, _ := e.store.Get(context.Background(), memory.DefaultOrg, rem.Memories[0].ID)
	if m.Author.Client != "agent-test" {
		t.Fatalf("client = %q", m.Author.Client)
	}
}
