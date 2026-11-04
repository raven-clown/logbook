package mcpserver_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/raven-clown/logbook/internal/mcpserver"
	"github.com/raven-clown/logbook/memory"
	"github.com/raven-clown/logbook/staleness"
	"github.com/raven-clown/logbook/store/sqlite"
)

type env struct {
	client *client.Client
	now    *time.Time
}

func start(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	st, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "logbook.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	svc := memory.NewService(st, memory.Options{Now: func() time.Time { return now }, Rules: staleness.Default()})
	principal := func(context.Context) memory.Principal {
		return memory.Principal{OrgID: memory.DefaultOrg, Subject: "local", Channel: memory.ChannelMCP, Project: "demo"}
	}
	c, err := client.NewInProcessClient(mcpserver.New(svc, principal, "test"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	req := mcp.InitializeRequest{}
	req.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	req.Params.ClientInfo = mcp.Implementation{Name: "test", Version: "1"}
	if _, err := c.Initialize(ctx, req); err != nil {
		t.Fatal(err)
	}
	return &env{client: c, now: &now}
}

func (e *env) call(t *testing.T, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	res, err := e.client.CallTool(context.Background(), req)
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	return res
}

func structured(t *testing.T, res *mcp.CallToolResult, into any) {
	t.Helper()
	if res.IsError {
		t.Fatalf("tool error: %s", text(res))
	}
	b, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, into); err != nil {
		t.Fatalf("decoding %s: %v", b, err)
	}
	var fromText map[string]any
	if err := json.Unmarshal([]byte(text(res)), &fromText); err != nil {
		t.Fatalf("text content is not JSON: %q", text(res))
	}
}

func text(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

func TestListTools(t *testing.T) {
	e := start(t)
	res, err := e.client.ListTools(context.Background(), mcp.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		if tool.Description == "" {
			t.Errorf("%s has no description", tool.Name)
		}
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"forget", "list_memories", "recall", "remember", "verify"}) {
		t.Fatalf("tools = %v", names)
	}
}

func TestRememberRecallOverMCP(t *testing.T) {
	e := start(t)
	var rem memory.RememberResult
	structured(t, e.call(t, "remember", map[string]any{"content": "the staging database is postgres 17", "tags": []string{"db"}}), &rem)
	if len(rem.Memories) != 1 || rem.Memories[0].Scope != "project:demo" {
		t.Fatalf("remember = %+v", rem)
	}
	var rec memory.RecallResult
	structured(t, e.call(t, "recall", map[string]any{"query": "staging"}), &rec)
	if len(rec.Results) != 1 || rec.Results[0].Content != "the staging database is postgres 17" {
		t.Fatalf("recall = %+v", rec)
	}
}

func TestVerifyForgetListOverMCP(t *testing.T) {
	e := start(t)
	var rem memory.RememberResult
	structured(t, e.call(t, "remember", map[string]any{"content": "tests run on every push"}), &rem)
	id := rem.Memories[0].ID

	var ver memory.VerifyResult
	structured(t, e.call(t, "verify", map[string]any{"id": id, "verdict": "dispute", "note": "only on main"}), &ver)
	if ver.Status != memory.StatusDisputed || !ver.Counted {
		t.Fatalf("verify = %+v", ver)
	}
	var list memory.ListResult
	structured(t, e.call(t, "list_memories", map[string]any{"order": "review", "limit": 10}), &list)
	if len(list.Items) != 1 || list.Items[0].ID != id {
		t.Fatalf("list = %+v", list)
	}
	var forgotten memory.Memory
	structured(t, e.call(t, "forget", map[string]any{"id": id, "reason": "wrong", "purge": true}), &forgotten)
	if forgotten.Status != memory.StatusForgotten || !forgotten.Purged {
		t.Fatalf("forget = %+v", forgotten)
	}
}

func TestToolErrors(t *testing.T) {
	e := start(t)
	tests := []struct {
		tool  string
		args  map[string]any
		code  string
		field string
	}{
		{"remember", map[string]any{"content": ""}, "invalid_argument", "content"},
		{"remember", map[string]any{"content": "x", "valid_until": "next friday"}, "invalid_argument", "valid_until"},
		{"remember", map[string]any{"content": 42}, "invalid_argument", ""},
		{"recall", map[string]any{"query": ""}, "invalid_argument", "query"},
		{"recall", map[string]any{"query": "x", "as_of": "yesterday"}, "invalid_argument", "as_of"},
		{"verify", map[string]any{"id": "mem_404", "verdict": "confirm"}, "not_found", "id"},
		{"list_memories", map[string]any{"order": "random"}, "invalid_argument", "order"},
	}
	for _, tt := range tests {
		t.Run(tt.tool+" "+tt.code+" "+tt.field, func(t *testing.T) {
			res := e.call(t, tt.tool, tt.args)
			if !res.IsError {
				t.Fatalf("expected an error result, got %s", text(res))
			}
			var body struct {
				Error struct {
					Code, Message, Field string
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(text(res)), &body); err != nil {
				t.Fatalf("error text is not JSON: %q", text(res))
			}
			if body.Error.Code != tt.code || body.Error.Field != tt.field || body.Error.Message == "" {
				t.Fatalf("error = %+v", body.Error)
			}
		})
	}
}

func TestIncludeStaleDefault(t *testing.T) {
	e := start(t)
	var rem memory.RememberResult
	structured(t, e.call(t, "remember", map[string]any{"content": "the build is slow", "kind": "observation"}), &rem)
	*e.now = e.now.Add(31 * 24 * time.Hour)

	var rec memory.RecallResult
	structured(t, e.call(t, "recall", map[string]any{"query": "build"}), &rec)
	if len(rec.Results) != 1 || !rec.Results[0].Stale {
		t.Fatalf("default recall = %+v", rec)
	}
	structured(t, e.call(t, "recall", map[string]any{"query": "build", "include_stale": false}), &rec)
	if len(rec.Results) != 0 {
		t.Fatalf("include_stale false = %+v", rec)
	}
}
