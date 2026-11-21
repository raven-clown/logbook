package memory_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/raven-clown/logbook/internal/testutil"
	"github.com/raven-clown/logbook/memory"
)

func withExtractor(ext memory.Extractor) func(*memory.Options) {
	return func(o *memory.Options) { o.Extractor = ext }
}

func TestRememberExtract(t *testing.T) {
	ext := &testutil.FakeExtractor{Facts: []memory.Candidate{
		{Content: "CI runs Go 1.26", Kind: memory.KindFact, Subject: "ci.go-version", Evidence: "we moved CI to Go 1.26 today"},
		{Content: "Deploys freeze on Fridays", Kind: memory.KindDecision, Evidence: "no deploys on fridays from now on"},
		{Content: "the build cache lives on s3", Kind: memory.KindFact},
		{Content: "   ", Kind: memory.KindFact},
	}}
	f := newFixture(t, withExtractor(ext))
	p := inProject(alice, "web")
	existing := f.remember(t, p, memory.RememberRequest{Content: "The build cache lives on S3"})
	until := day0.Add(30 * 24 * time.Hour)
	res, err := f.svc.Remember(background, p, memory.RememberRequest{
		Content: "we moved CI to Go 1.26 today, and no deploys on fridays from now on", Extract: true,
		Tags: []string{"standup"}, ValidUntil: until,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Memories) != 3 {
		t.Fatalf("memories = %+v, want three (the blank fact dropped)", res.Memories)
	}
	if !res.Memories[2].Duplicate || res.Memories[2].ID != existing.ID {
		t.Fatalf("third = %+v, want the existing memory as a duplicate", res.Memories[2])
	}
	first := f.get(t, res.Memories[0].ID)
	if first.Source != memory.SourceExtracted || first.Subject != "ci.go-version" || first.Evidence != "we moved CI to Go 1.26 today" ||
		first.Scope != "project:web" || first.Tags[0] != "standup" || !first.ValidUntil.Equal(until) || first.Kind != memory.KindFact {
		t.Fatalf("first = %+v", first)
	}
	if second := f.get(t, res.Memories[1].ID); second.Kind != memory.KindDecision || second.Subject != "" {
		t.Fatalf("second = %+v", second)
	}
}

func TestRememberExtractRules(t *testing.T) {
	var twelve []memory.Candidate
	for i := range 12 {
		twelve = append(twelve, memory.Candidate{Content: fmt.Sprintf("fact number %d", i), Kind: memory.KindFact})
	}
	for _, tt := range []struct {
		name  string
		facts []memory.Candidate
		check func(t *testing.T, f *fixture, res *memory.RememberResult)
	}{
		{"at most ten", twelve, func(t *testing.T, _ *fixture, res *memory.RememberResult) {
			if len(res.Memories) != 10 {
				t.Fatalf("stored %d, want 10", len(res.Memories))
			}
		}},
		{"unknown kind becomes fact", []memory.Candidate{{Content: "alice likes tabs", Kind: "opinion"}}, func(t *testing.T, f *fixture, res *memory.RememberResult) {
			if m := f.get(t, res.Memories[0].ID); m.Kind != memory.KindFact {
				t.Fatalf("kind = %s", m.Kind)
			}
		}},
		{"a bad subject or evidence is dropped", []memory.Candidate{{Content: "ci runs go 1.26", Kind: memory.KindFact, Subject: strings.Repeat("s", 201), Evidence: "we bumped it"}}, func(t *testing.T, f *fixture, res *memory.RememberResult) {
			if m := f.get(t, res.Memories[0].ID); m.Subject != "" || m.Evidence != "" || m.Content != "ci runs go 1.26" {
				t.Fatalf("stored = %+v", m)
			}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, withExtractor(&testutil.FakeExtractor{Facts: tt.facts}))
			res, err := f.svc.Remember(background, alice, memory.RememberRequest{Content: "raw text", Extract: true})
			if err != nil {
				t.Fatal(err)
			}
			tt.check(t, f, res)
		})
	}
}

func TestRememberExtractMergesDegraded(t *testing.T) {
	m := &models{ext: &testutil.FakeExtractor{Facts: []memory.Candidate{
		{Content: "the cache lives on s3", Kind: memory.KindFact},
		{Content: "the queue lives on sqs", Kind: memory.KindFact},
	}}}
	f := newModelFixture(t, m)
	m.emb.SetErr(errors.New("down"))
	res, err := f.svc.Remember(background, alice, memory.RememberRequest{Content: "raw text", Extract: true})
	if err != nil || len(res.Memories) != 2 || !slices.Equal(res.Degraded, []string{memory.DegradedEmbeddings}) {
		t.Fatalf("Remember = %+v, %v", res, err)
	}
}

func TestRememberExtractNothing(t *testing.T) {
	f := newFixture(t, withExtractor(&testutil.FakeExtractor{}))
	res, err := f.svc.Remember(background, alice, memory.RememberRequest{Content: "hello, how are you", Extract: true})
	if err != nil || res.Memories == nil || len(res.Memories) != 0 {
		t.Fatalf("Remember = %+v, %v, want an empty list", res, err)
	}
}

func TestRememberExtractErrors(t *testing.T) {
	for _, tt := range []struct {
		name  string
		ext   memory.Extractor
		r     memory.RememberRequest
		code  memory.Code
		field string
	}{
		{"no extractor", nil, memory.RememberRequest{Content: "text", Extract: true}, memory.CodeUnavailable, ""},
		{"extractor fails", &testutil.FakeExtractor{Err: errors.New("model overloaded")}, memory.RememberRequest{Content: "text", Extract: true}, memory.CodeUnavailable, ""},
		{"corrects", &testutil.FakeExtractor{}, memory.RememberRequest{Content: "text", Extract: true, Corrects: "mem_1"}, memory.CodeInvalidArgument, "corrects"},
		{"empty", &testutil.FakeExtractor{}, memory.RememberRequest{Content: " ", Extract: true}, memory.CodeInvalidArgument, "content"},
		{"bad kind", &testutil.FakeExtractor{}, memory.RememberRequest{Content: "text", Extract: true, Kind: "decison"}, memory.CodeInvalidArgument, "kind"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var opts []func(*memory.Options)
			if tt.ext != nil {
				opts = append(opts, withExtractor(tt.ext))
			}
			f := newFixture(t, opts...)
			_, err := f.svc.Remember(background, alice, tt.r)
			wantCode(t, err, tt.code, tt.field)
		})
	}
}
