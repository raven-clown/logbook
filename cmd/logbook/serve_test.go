package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/raven-clown/logbook/admin"
	"github.com/raven-clown/logbook/internal/config"
	"github.com/raven-clown/logbook/internal/obs"
	"github.com/raven-clown/logbook/memory"
	"github.com/raven-clown/logbook/store/sqlite"
)

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type response struct {
	StatusCode int
	Header     http.Header
}

type served struct {
	base string
	logs *lockedBuffer
	stop func() int
}

func createKey(t *testing.T, dsn, name string) string {
	t.Helper()
	st, err := sqlite.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	plain, hash, last4, err := admin.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateAPIKey(context.Background(), &admin.APIKey{OrgID: memory.DefaultOrg, ID: "key_" + name, Name: name, Hash: hash, Last4: last4, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	return plain
}

// startServe runs logbook serve in-process and waits for its listening line.
func startServe(t *testing.T, env []string, args ...string) *served {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	outR, outW := io.Pipe()
	logs := &lockedBuffer{}
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, append([]string{"serve", "--listen", "127.0.0.1:0"}, args...), strings.NewReader(""), outW, logs, env)
		_ = outW.Close()
	}()
	addr := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(outR)
		found := false
		for sc.Scan() {
			_, _ = logs.Write(append(sc.Bytes(), '\n'))
			var line map[string]any
			if !found && json.Unmarshal(sc.Bytes(), &line) == nil && line["msg"] == "listening" {
				found = true
				addr <- line["addr"].(string)
			}
		}
		if !found {
			close(addr)
		}
	}()
	var a string
	select {
	case got, ok := <-addr:
		if !ok {
			t.Fatalf("serve exited before listening: %s", logs.String())
		}
		a = got
	case <-time.After(15 * time.Second):
		t.Fatalf("serve did not start: %s", logs.String())
	}
	s := &served{base: "http://" + a, logs: logs}
	s.stop = func() int {
		cancel()
		select {
		case code := <-done:
			return code
		case <-time.After(15 * time.Second):
			t.Fatal("serve did not stop")
			return -1
		}
	}
	t.Cleanup(func() { cancel() })
	return s
}

func (s *served) get(t *testing.T, path, token string) (response, string) {
	t.Helper()
	return s.do(t, http.MethodGet, path, token, "")
}

func (s *served) do(t *testing.T, method, path, token, body string, header ...string) (response, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, s.base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	return response{StatusCode: res.StatusCode, Header: res.Header}, string(b)
}

func TestServeRoutes(t *testing.T) {
	c := newCLI(t)
	key := createKey(t, c.dsn(), "ci")
	s := startServe(t, append(c.env, "LOGBOOK_DEFAULT_PROJECT=mothership"))

	if res, body := s.get(t, "/healthz", ""); res.StatusCode != http.StatusOK || !strings.Contains(body, `"ok"`) {
		t.Fatalf("healthz %d %s", res.StatusCode, body)
	}
	if res, body := s.get(t, "/readyz", ""); res.StatusCode != http.StatusOK || !strings.Contains(body, `"database":"ok"`) || !strings.Contains(body, `"transponder":"not configured"`) {
		t.Fatalf("readyz %d %s", res.StatusCode, body)
	}

	res, body := s.get(t, "/v1/memories", "")
	if res.StatusCode != http.StatusUnauthorized || !strings.HasPrefix(res.Header.Get("WWW-Authenticate"), "Bearer") || !strings.Contains(body, "unauthenticated") {
		t.Fatalf("no token: %d %v %s", res.StatusCode, res.Header, body)
	}
	if res, _ := s.get(t, "/v1/memories", "lbk_wrong"); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad key: %d", res.StatusCode)
	}
	if res, _ := s.do(t, http.MethodPost, "/mcp", "", `{}`); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("mcp without a token: %d", res.StatusCode)
	}

	res, body = s.do(t, http.MethodPost, "/v1/memories", key, `{"content":"the server answers"}`, "X-Request-ID", "req-42")
	if res.StatusCode != http.StatusCreated || !strings.Contains(body, "project:mothership") || res.Header.Get("X-Request-ID") != "req-42" {
		t.Fatalf("remember %d %s %v", res.StatusCode, body, res.Header)
	}
	if res, body := s.get(t, "/v1/memories", key); res.StatusCode != http.StatusOK || !strings.Contains(body, "the server answers") {
		t.Fatalf("list %d %s", res.StatusCode, body)
	}

	res, body = s.get(t, "/metrics", "")
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `logbook_http_requests_total{code="201",method="POST",route="POST /v1/memories"} 1`) ||
		!strings.Contains(body, `code="401"`) {
		t.Fatalf("metrics %d:\n%s", res.StatusCode, body)
	}

	if code := s.stop(); code != 0 {
		t.Fatalf("serve exited %d: %s", code, s.logs.String())
	}
	if !strings.Contains(s.logs.String(), `"request_id":"req-42"`) {
		t.Fatalf("access log lacks the request id:\n%s", s.logs.String())
	}
}

func TestServeResourceMetadata(t *testing.T) {
	c := newCLI(t)
	s := startServe(t, append(c.env, "LOGBOOK_PUBLIC_URL=https://logbook.example.com", "LOGBOOK_AUTH__OIDC__ISSUER=https://idp.example.com", "LOGBOOK_AUTH__OIDC__AUDIENCE=logbook"))
	res, body := s.get(t, "/.well-known/oauth-protected-resource/mcp", "")
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `"resource":"https://logbook.example.com/mcp"`) || !strings.Contains(body, "https://idp.example.com") {
		t.Fatalf("metadata %d %s", res.StatusCode, body)
	}
	res, _ = s.get(t, "/v1/memories", "")
	if h := res.Header.Get("WWW-Authenticate"); !strings.Contains(h, `resource_metadata="https://logbook.example.com/.well-known/oauth-protected-resource/mcp"`) {
		t.Fatalf("WWW-Authenticate = %q", h)
	}
}

func TestServeBadConfig(t *testing.T) {
	c := newCLI(t)
	code, _, errOut := c.run("serve", "--listen", "not an address")
	if code != 1 || errOut == "" {
		t.Fatalf("serve with a bad address exited %d: %s", code, errOut)
	}
}

func testHandler(t *testing.T) (http.Handler, func()) {
	t.Helper()
	st, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "logbook.db"))
	if err != nil {
		t.Fatal(err)
	}
	d := deps{cfg: config.Default(), store: st, access: st, svc: memory.NewService(st, memory.Options{}), metrics: obs.NewMetrics(), logger: obs.NewLogger(io.Discard, slog.LevelInfo)}
	return newHandler(d), func() { _ = st.Close() }
}

func TestReadyzDatabaseDown(t *testing.T) {
	h, closeStore := testHandler(t)
	closeStore()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"database":"unavailable"`) {
		t.Fatalf("readyz %d %s", rec.Code, rec.Body.String())
	}
}
