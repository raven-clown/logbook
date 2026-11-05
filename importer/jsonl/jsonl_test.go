package jsonl_test

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/raven-clown/logbook/importer"
	"github.com/raven-clown/logbook/importer/jsonl"
	"github.com/raven-clown/logbook/memory"
	"github.com/raven-clown/logbook/store/sqlite"
)

var alice = memory.Principal{OrgID: memory.DefaultOrg, Subject: "alice", Channel: memory.ChannelCLI}

func newService(t *testing.T) *memory.Service {
	t.Helper()
	st, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "logbook.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	return memory.NewService(st, memory.Options{Now: func() time.Time { return now }})
}

func export(t *testing.T, svc *memory.Service) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	if err := svc.Export(context.Background(), alice, nil, func(r memory.Record) error { return jsonl.Write(&buf, r) }); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func TestJSONLRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := newService(t)
	agent := alice
	agent.Channel = memory.ChannelMCP
	remember := func(r memory.RememberRequest) string {
		res, err := src.Remember(ctx, agent, r)
		if err != nil {
			t.Fatal(err)
		}
		return res.Memories[0].ID
	}
	a := remember(memory.RememberRequest{Content: "use npm"})
	remember(memory.RememberRequest{Content: "use pnpm, not npm", Corrects: a})
	c := remember(memory.RememberRequest{Content: "node 22 everywhere", Tags: []string{"node"}})
	remember(memory.RememberRequest{Content: "ci caches the pnpm store"})
	if _, err := src.Verify(ctx, alice, memory.VerifyRequest{ID: c, Verdict: memory.VerdictConfirm}); err != nil {
		t.Fatal(err)
	}

	dst := newService(t)
	imp, err := importer.Get("jsonl")
	if err != nil {
		t.Fatal(err)
	}
	report, err := importer.Run(ctx, dst, alice, "", imp, export(t, src))
	if err != nil {
		t.Fatal(err)
	}
	if report.Imported != 4 || len(report.Skipped) != 0 {
		t.Fatalf("report = %+v", report)
	}

	all := []memory.Status{memory.StatusUnverified, memory.StatusVerified, memory.StatusDisputed, memory.StatusSuperseded}
	want, _ := src.List(ctx, alice, memory.ListRequest{Statuses: all})
	got, _ := dst.List(ctx, alice, memory.ListRequest{Statuses: all})
	if len(got.Items) != len(want.Items) {
		t.Fatalf("imported %d, want %d", len(got.Items), len(want.Items))
	}
	byContent := map[string]*memory.Memory{}
	for _, m := range got.Items {
		byContent[m.Content] = m
	}
	for _, w := range want.Items {
		g := byContent[w.Content]
		wantStatus := w.Status
		if wantStatus == memory.StatusVerified {
			wantStatus = memory.StatusUnverified
		}
		if g == nil || g.Status != wantStatus || g.Confirmations != 0 || !g.CreatedAt.Equal(w.CreatedAt) {
			t.Fatalf("%q: got %+v, want status %s and no confirmations", w.Content, g, wantStatus)
		}
	}
	res, err := dst.Recall(ctx, alice, memory.RecallRequest{Query: "npm"})
	if err != nil || len(res.Results) == 0 || res.Results[0].Content != "use pnpm, not npm" {
		t.Fatalf("recall after import = %+v, %v", res, err)
	}
}

func TestJSONLBadLines(t *testing.T) {
	input := strings.Join([]string{
		`{"memory":{"content":"a good one"}}`,
		`{not json`,
		``,
		`{"memory":{"content":""}}`,
		`{"memory":{"content":"another good one"}}`,
	}, "\n")
	imp, _ := importer.Get("jsonl")
	report, err := importer.Run(context.Background(), newService(t), alice, "", imp, strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if report.Imported != 2 || len(report.Skipped) != 2 || report.Skipped[0].Line != 2 || report.Skipped[1].Line != 4 {
		t.Fatalf("report = %+v", report)
	}
	if !strings.Contains(report.Skipped[1].Err, "empty") {
		t.Fatalf("skip reason = %q", report.Skipped[1].Err)
	}
}

func TestJSONLLongLine(t *testing.T) {
	line := `{"memory":{"content":"` + strings.Repeat("a", 8000) + `","evidence":"` + strings.Repeat("e", 8000) + `"}}`
	imp, _ := importer.Get("jsonl")
	report, err := importer.Run(context.Background(), newService(t), alice, "", imp, strings.NewReader(line))
	if err != nil || report.Imported != 1 {
		t.Fatalf("long line: %+v, %v", report, err)
	}
}

func TestWriteEscapesControls(t *testing.T) {
	var buf bytes.Buffer
	content := "x" + string([]rune{0x9b, 0x202e}) + "y"
	if err := jsonl.Write(&buf, memory.Record{Memory: memory.Memory{ID: "mem_1", Content: content}}); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(buf.String(), string([]rune{0x9b, 0x202e})) {
		t.Fatalf("raw control runes in %q", buf.String())
	}
}
