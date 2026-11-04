package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/raven-clown/logbook/memory"
	"github.com/raven-clown/logbook/store/sqlite"
)

type cli struct {
	t   *testing.T
	env []string
}

func newCLI(t *testing.T) *cli {
	return &cli{t: t, env: []string{"LOGBOOK_DATABASE__DSN=" + filepath.Join(t.TempDir(), "logbook.db")}}
}

func (c *cli) run(args ...string) (int, string, string) {
	c.t.Helper()
	var out, errOut bytes.Buffer
	code := run(context.Background(), args, strings.NewReader(""), &out, &errOut, c.env)
	return code, out.String(), errOut.String()
}

func (c *cli) ok(args ...string) string {
	c.t.Helper()
	code, out, errOut := c.run(args...)
	if code != 0 {
		c.t.Fatalf("logbook %v exited %d: %s", args, code, errOut)
	}
	return out
}

func (c *cli) dsn() string {
	return strings.TrimPrefix(c.env[0], "LOGBOOK_DATABASE__DSN=")
}

func TestRunRememberRecall(t *testing.T) {
	c := newCLI(t)
	id := strings.TrimSpace(c.ok("remember", "use pnpm, not npm", "--tag", "node"))
	if !strings.HasPrefix(id, "mem_") {
		t.Fatalf("remember printed %q", id)
	}
	out := c.ok("recall", "pnpm")
	if !strings.Contains(out, id) || !strings.Contains(out, "unverified") || !strings.Contains(out, "use pnpm, not npm") {
		t.Fatalf("recall printed %q", out)
	}
}

func TestRunCorrectionShowsOldText(t *testing.T) {
	c := newCLI(t)
	old := strings.TrimSpace(c.ok("remember", "deploys go through Jenkins"))
	c.ok("remember", "deploys go through Argo CD", "--corrects", old)
	out := c.ok("recall", "Jenkins")
	if !strings.Contains(out, "Argo CD") || !strings.Contains(out, "corrected from: deploys go through Jenkins") {
		t.Fatalf("recall printed %q", out)
	}
}

func TestRunVerifyCounts(t *testing.T) {
	c := newCLI(t)
	st, err := sqlite.Open(context.Background(), c.dsn())
	if err != nil {
		t.Fatal(err)
	}
	svc := memory.NewService(st, memory.Options{})
	res, err := svc.Remember(context.Background(), memory.Principal{OrgID: memory.DefaultOrg, Subject: "local", Channel: memory.ChannelMCP}, memory.RememberRequest{Content: "an agent wrote this"})
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	id := res.Memories[0].ID
	out := c.ok("verify", id, "confirm", "--json")
	var v memory.VerifyResult
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("verify --json printed %q", out)
	}
	if !v.Counted || v.Status != memory.StatusVerified {
		t.Fatalf("verify = %+v", v)
	}
	if out := c.ok("verify", id, "dispute", "--note", "not any more"); !strings.Contains(out, "disputed") {
		t.Fatalf("verify printed %q", out)
	}
}

func TestRunJSON(t *testing.T) {
	c := newCLI(t)
	c.ok("remember", "use pnpm, not npm")
	var res memory.RecallResult
	if err := json.Unmarshal([]byte(c.ok("recall", "pnpm", "--json")), &res); err != nil || len(res.Results) != 1 {
		t.Fatalf("recall --json = %+v, %v", res, err)
	}
	var list memory.ListResult
	if err := json.Unmarshal([]byte(c.ok("list", "--json")), &list); err != nil || len(list.Items) != 1 {
		t.Fatalf("list --json = %+v, %v", list, err)
	}
	var rem memory.RememberResult
	if err := json.Unmarshal([]byte(c.ok("remember", "another one", "--json")), &rem); err != nil || len(rem.Memories) != 1 {
		t.Fatalf("remember --json = %+v, %v", rem, err)
	}
}

func TestRunListAndForget(t *testing.T) {
	c := newCLI(t)
	id := strings.TrimSpace(c.ok("remember", "temporary note", "--kind", "observation"))
	if out := c.ok("list", "--order", "review"); !strings.Contains(out, id) {
		t.Fatalf("list printed %q", out)
	}
	if out := c.ok("forget", id, "--purge", "--reason", "done"); !strings.Contains(out, "purged") {
		t.Fatalf("forget printed %q", out)
	}
	if out := c.ok("list"); strings.Contains(out, id) {
		t.Fatalf("forgotten memory still listed: %q", out)
	}
}

func TestRunErrors(t *testing.T) {
	c := newCLI(t)
	tests := []struct {
		name string
		args []string
		code int
		msg  string
	}{
		{"no subcommand", nil, 2, "usage"},
		{"unknown subcommand", []string{"fly"}, 2, "unknown command"},
		{"bad flag", []string{"recall", "--nope", "x"}, 2, "nope"},
		{"remember nothing", []string{"remember"}, 2, "usage"},
		{"empty content", []string{"remember", ""}, 1, "error:"},
		{"bad verdict", []string{"verify", "mem_1", "maybe"}, 1, "error:"},
		{"bad config path", []string{"list", "--config", filepath.Join(t.TempDir(), "nope.yaml")}, 1, "nope.yaml"},
		{"unknown import format", []string{"import", "csv", "x.csv"}, 1, "csv"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, _, errOut := c.run(tt.args...)
			if code != tt.code || !strings.Contains(errOut, tt.msg) {
				t.Fatalf("exit %d, stderr %q; want %d containing %q", code, errOut, tt.code, tt.msg)
			}
		})
	}
}

func TestRunExportImport(t *testing.T) {
	src := newCLI(t)
	src.ok("remember", "first memory")
	src.ok("remember", "second memory")
	path := filepath.Join(t.TempDir(), "backup.jsonl")
	if err := os.WriteFile(path, []byte(src.ok("export")), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := newCLI(t)
	if out := dst.ok("import", "jsonl", path); !strings.Contains(out, "imported 2") {
		t.Fatalf("import printed %q", out)
	}
	if out := dst.ok("list"); !strings.Contains(out, "first memory") || !strings.Contains(out, "second memory") {
		t.Fatalf("list after import = %q", out)
	}
}

func TestRunNowOverride(t *testing.T) {
	c := newCLI(t)
	base := c.env
	c.env = append(base, "LOGBOOK_NOW=2030-01-01T00:00:00Z")
	id := strings.TrimSpace(c.ok("remember", "the queue is slow", "--kind", "observation"))
	if out := c.ok("list", "--stale"); strings.Contains(out, id) {
		t.Fatalf("fresh memory listed as stale: %q", out)
	}
	c.env = append(base, "LOGBOOK_NOW=2030-03-01T00:00:00Z")
	if out := c.ok("list", "--stale"); !strings.Contains(out, id) || !strings.Contains(out, "stale") {
		t.Fatalf("stale list = %q", out)
	}
	c.env = append(base, "LOGBOOK_NOW=tomorrow")
	if code, _, errOut := c.run("list"); code != 1 || !strings.Contains(errOut, "LOGBOOK_NOW") {
		t.Fatalf("bad LOGBOOK_NOW: %d %q", code, errOut)
	}
}

func TestRunVersionAndMigrate(t *testing.T) {
	c := newCLI(t)
	if out := c.ok("version"); strings.TrimSpace(out) != "dev" {
		t.Fatalf("version printed %q", out)
	}
	if out := c.ok("migrate"); !strings.Contains(out, "up to date") {
		t.Fatalf("migrate printed %q", out)
	}
}

func TestRunMCP(t *testing.T) {
	c := newCLI(t)
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	var errOut bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- run(context.Background(), []string{"mcp", "--project", "demo"}, inR, outW, &errOut, c.env)
		_ = outW.Close()
	}()

	lines := bufio.NewScanner(outR)
	send := func(msg string) {
		if _, err := io.WriteString(inW, msg+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	read := func() map[string]any {
		if !lines.Scan() {
			t.Fatalf("no response, stderr: %s", errOut.String())
		}
		var m map[string]any
		if err := json.Unmarshal(lines.Bytes(), &m); err != nil {
			t.Fatalf("stdout line is not JSON: %q", lines.Text())
		}
		return m
	}
	send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`)
	if r := read(); r["id"].(float64) != 1 {
		t.Fatalf("initialize response = %v", r)
	}
	send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"remember","arguments":{"content":"stdio works"}}}`)
	r := read()
	body, _ := json.Marshal(r["result"])
	if !strings.Contains(string(body), "project:demo") {
		t.Fatalf("remember over stdio = %s", body)
	}
	_ = inW.Close()

	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("mcp exited %d: %s", code, errOut.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("mcp did not exit after stdin closed")
	}
}
