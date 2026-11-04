// Package e2e plays the README demo against the built binary.
package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/raven-clown/logbook/memory"
)

var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "logbook-e2e")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binary = filepath.Join(dir, "logbook")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "../cmd/logbook") // #nosec G204 -- fixed build of this repository
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "building logbook:", err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

type demo struct {
	t   *testing.T
	env []string
}

func (d *demo) cli(now string, args ...string) []byte {
	d.t.Helper()
	cmd := exec.CommandContext(context.Background(), binary, args...) // #nosec G204 -- the test runs the binary it built
	cmd.Env = append(append(os.Environ(), d.env...), "LOGBOOK_NOW="+now)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		d.t.Fatalf("logbook %v: %v\n%s", args, err, stderr.String())
	}
	return out
}

func call(t *testing.T, c *client.Client, name string, args map[string]any, into any) {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	res, err := c.CallTool(context.Background(), req)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("%s returned an error: %+v", name, res.Content)
	}
	b, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(b, into); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func TestDemoStory(t *testing.T) {
	ctx := context.Background()
	d := &demo{t: t, env: []string{"LOGBOOK_DATABASE__DSN=" + filepath.Join(t.TempDir(), "logbook.db")}}
	day0 := "2026-11-04T00:00:00Z"

	c, err := client.NewStdioMCPClient(binary, append(d.env, "LOGBOOK_NOW="+day0), "mcp", "--project", "demo")
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	var wg sync.WaitGroup
	if r, ok := client.GetStderr(c); ok {
		wg.Go(func() { _, _ = io.Copy(&stderr, r) })
	}
	init := mcp.InitializeRequest{}
	init.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	init.Params.ClientInfo = mcp.Implementation{Name: "e2e", Version: "1"}
	if _, err := c.Initialize(ctx, init); err != nil {
		t.Fatal(err)
	}

	var first, second, third memory.RememberResult
	call(t, c, "remember", map[string]any{"content": "CI runs on Go 1.25", "subject": "ci.go-version", "kind": "fact"}, &first)
	call(t, c, "remember", map[string]any{"content": "CI runs on Go 1.26", "subject": "ci.go-version", "kind": "fact"}, &second)
	if got := second.Memories[0].Conflicts; len(got) != 1 || got[0] != first.Memories[0].ID {
		t.Fatalf("conflicts = %v, want the first memory", got)
	}
	call(t, c, "remember", map[string]any{"content": "CI runs on Go 1.27", "corrects": second.Memories[0].ID}, &third)
	thirdID := third.Memories[0].ID

	var rec memory.RecallResult
	call(t, c, "recall", map[string]any{"query": "1.26"}, &rec)
	if len(rec.Results) == 0 || rec.Results[0].ID != thirdID || rec.Results[0].CorrectedFrom == nil || rec.Results[0].CorrectedFrom.Content != "CI runs on Go 1.26" {
		t.Fatalf("recall 1.26 = %+v", rec.Results)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()

	var ver memory.VerifyResult
	if err := json.Unmarshal(d.cli(day0, "verify", thirdID, "confirm", "--project", "demo", "--json"), &ver); err != nil {
		t.Fatal(err)
	}
	if !ver.Counted || ver.Status != memory.StatusVerified {
		t.Fatalf("verify from the CLI = %+v", ver)
	}

	var queue memory.ListResult
	if err := json.Unmarshal(d.cli("2027-06-01T00:00:00Z", "list", "--order", "review", "--project", "demo", "--json"), &queue); err != nil {
		t.Fatal(err)
	}
	if len(queue.Items) < 2 || queue.Items[0].ID != first.Memories[0].ID || queue.Items[0].Status != memory.StatusDisputed || queue.Items[1].ID != thirdID {
		t.Fatalf("review queue = %+v", queue.Items)
	}

	lines := bufio.NewScanner(&stderr)
	for lines.Scan() {
		line := strings.TrimSpace(lines.Text())
		if line != "" && !json.Valid([]byte(line)) {
			t.Fatalf("stderr line is not JSON: %q", line)
		}
	}
}
