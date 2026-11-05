package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/raven-clown/logbook/memory"
)

func (d *demo) key(name string) string {
	d.t.Helper()
	var k struct{ Key string }
	if err := json.Unmarshal(d.cli("2026-11-04T00:00:00Z", "key", "create", name, "--json"), &k); err != nil {
		d.t.Fatal(err)
	}
	return k.Key
}

// serve starts logbook serve and returns its base URL once it logs the bound address.
func (d *demo) serve(now string) string {
	d.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, binary, "serve", "--listen", "127.0.0.1:0") // #nosec G204 -- the test runs the binary it built
	cmd.Env = append(append(os.Environ(), d.env...), "LOGBOOK_NOW="+now)
	out, err := cmd.StdoutPipe()
	if err != nil {
		d.t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		d.t.Fatal(err)
	}
	d.t.Cleanup(func() {
		cancel()
		_ = cmd.Wait()
	})
	addr := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			var line map[string]any
			if json.Unmarshal(sc.Bytes(), &line) != nil {
				d.t.Errorf("stdout line is not JSON: %q", sc.Text())
				continue
			}
			if line["msg"] == "listening" {
				addr <- line["addr"].(string)
			}
		}
	}()
	select {
	case a := <-addr:
		return "http://" + a
	case <-time.After(30 * time.Second):
		d.t.Fatal("logbook serve did not start")
		return ""
	}
}

func get(t *testing.T, url, token string) (int, string) {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	return res.StatusCode, string(b)
}

func TestServeEndToEnd(t *testing.T) {
	ctx := context.Background()
	d := &demo{t: t, env: []string{"LOGBOOK_DATABASE__DSN=" + filepath.Join(t.TempDir(), "logbook.db")}}
	day0 := "2026-11-04T00:00:00Z"
	agentKey, aliceKey := d.key("agent"), d.key("alice")
	base := d.serve(day0)

	if code, _ := get(t, base+"/v1/memories", ""); code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", code)
	}

	c, err := client.NewStreamableHttpClient(base+"/mcp", transport.WithHTTPHeaders(map[string]string{
		"Authorization": "Bearer " + agentKey, "X-Project": "demo",
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	init := mcp.InitializeRequest{}
	init.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	init.Params.ClientInfo = mcp.Implementation{Name: "e2e-agent", Version: "1"}
	if _, err := c.Initialize(ctx, init); err != nil {
		t.Fatal(err)
	}

	var first, second, third memory.RememberResult
	call(t, c, "remember", map[string]any{"content": "CI runs on Go 1.25", "subject": "ci.go-version"}, &first)
	call(t, c, "remember", map[string]any{"content": "CI runs on Go 1.26", "subject": "ci.go-version"}, &second)
	call(t, c, "remember", map[string]any{"content": "CI runs on Go 1.27", "corrects": second.Memories[0].ID}, &third)
	thirdID := third.Memories[0].ID
	if third.Memories[0].Scope != "project:demo" {
		t.Fatalf("X-Project did not set the scope: %+v", third)
	}
	var rec memory.RecallResult
	call(t, c, "recall", map[string]any{"query": "1.26"}, &rec)
	if len(rec.Results) == 0 || rec.Results[0].ID != thirdID || rec.Results[0].CorrectedFrom == nil {
		t.Fatalf("recall 1.26 = %+v", rec.Results)
	}

	d.env = append(d.env, "LOGBOOK_TOKEN="+aliceKey)
	var ver memory.VerifyResult
	if err := json.Unmarshal(d.cli(day0, "verify", thirdID, "confirm", "--server", base, "--json"), &ver); err != nil {
		t.Fatal(err)
	}
	if !ver.Counted || ver.Status != memory.StatusVerified {
		t.Fatalf("a person confirming the agent's memory = %+v", ver)
	}
	var queue memory.ListResult
	if err := json.Unmarshal(d.cli(day0, "list", "--order", "review", "--project", "demo", "--server", base, "--json"), &queue); err != nil {
		t.Fatal(err)
	}
	if len(queue.Items) != 2 || queue.Items[0].ID != first.Memories[0].ID || queue.Items[0].Status != memory.StatusDisputed || queue.Items[1].ID != thirdID {
		t.Fatalf("review queue = %+v", queue.Items)
	}
	if queue.Items[1].Author.Client != "e2e-agent" || queue.Items[1].Author.Subject != "key:agent" {
		t.Fatalf("author = %+v", queue.Items[1].Author)
	}

	code, metrics := get(t, base+"/metrics", "")
	if code != http.StatusOK || !strings.Contains(metrics, `route="/mcp"`) || !strings.Contains(metrics, `route="POST /v1/memories/{id}/verify"`) {
		t.Fatalf("metrics %d:\n%s", code, metrics)
	}
}
