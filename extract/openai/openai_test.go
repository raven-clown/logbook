package openai_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/raven-clown/logbook/extract"
	_ "github.com/raven-clown/logbook/extract/openai"
	"github.com/raven-clown/logbook/memory"
)

type seen struct {
	path, auth string
	body       struct {
		Model          string  `json:"model"`
		Temperature    float64 `json:"temperature"`
		Messages       []struct{ Role, Content string }
		ResponseFormat struct {
			Type       string `json:"type"`
			JSONSchema struct {
				Name   string         `json:"name"`
				Strict bool           `json:"strict"`
				Schema map[string]any `json:"schema"`
			} `json:"json_schema"`
		} `json:"response_format"`
	}
}

// chat answers like /v1/chat/completions with content as the message.
func chat(t *testing.T, content string) (extract.Extractor, *seen) {
	t.Helper()
	s := &seen{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.path, s.auth = r.URL.Path, r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&s.body); err != nil {
			t.Error(err)
		}
		b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}}}})
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	x, err := extract.Open("openai", extract.Config{BaseURL: srv.URL + "/v1", Model: "memory-extract", APIKey: "sk-test"})
	if err != nil {
		t.Fatal(err)
	}
	return x, s
}

func TestExtract(t *testing.T) {
	x, s := chat(t, `{"facts":[
		{"content":"CI runs Go 1.26","kind":"fact","subject":"ci.go-version","evidence":"we bumped CI to Go 1.26"},
		{"content":"  ","kind":"fact","subject":"","evidence":""},
		{"content":"Alice prefers tabs","kind":"opinion","subject":"","evidence":"alice said tabs"},
		{"content":"Deploys freeze on Fridays","kind":"decision","subject":"`+strings.Repeat("x", 201)+`","evidence":""}
	]}`)
	got, err := x.Extract(context.Background(), "we bumped CI to Go 1.26. alice said tabs.")
	if err != nil {
		t.Fatal(err)
	}
	want := []memory.Candidate{
		{Content: "CI runs Go 1.26", Kind: memory.KindFact, Subject: "ci.go-version", Evidence: "we bumped CI to Go 1.26"},
		{Content: "Alice prefers tabs", Kind: memory.KindFact, Evidence: "alice said tabs"},
		{Content: "Deploys freeze on Fridays", Kind: memory.KindDecision},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("Extract = %+v\nwant %+v", got, want)
	}
	if s.path != "/v1/chat/completions" || s.auth != "Bearer sk-test" || s.body.Model != "memory-extract" || s.body.Temperature != 0 {
		t.Fatalf("request = %s %q %+v", s.path, s.auth, s.body)
	}
	rf := s.body.ResponseFormat
	if rf.Type != "json_schema" || rf.JSONSchema.Name != "memory_facts" || !rf.JSONSchema.Strict || rf.JSONSchema.Schema["type"] != "object" {
		t.Fatalf("response_format = %+v", rf)
	}
	if len(s.body.Messages) != 2 || s.body.Messages[0].Role != "system" || s.body.Messages[1].Content != "we bumped CI to Go 1.26. alice said tabs." {
		t.Fatalf("messages = %+v", s.body.Messages)
	}
}

func TestExtractKeepsTen(t *testing.T) {
	var facts []string
	for i := range 11 {
		facts = append(facts, fmt.Sprintf(`{"content":"fact %d","kind":"fact","subject":"","evidence":""}`, i))
	}
	x, _ := chat(t, `{"facts":[`+strings.Join(facts, ",")+`]}`)
	got, err := x.Extract(context.Background(), "text")
	if err != nil || len(got) != 10 || got[9].Content != "fact 9" {
		t.Fatalf("Extract = %d facts, %v", len(got), err)
	}
}

func TestExtractBadAnswers(t *testing.T) {
	for name, content := range map[string]string{
		"not json":     "Sure! Here are the facts: CI runs Go.",
		"wrong shape":  `{"facts":"CI runs Go"}`,
		"empty answer": "",
		"no facts":     `{}`,
		"null":         `null`,
	} {
		t.Run(name, func(t *testing.T) {
			x, _ := chat(t, content)
			if _, err := x.Extract(context.Background(), "text"); err == nil {
				t.Fatal("bad answer accepted")
			}
		})
	}
}

func TestExtractNoChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	t.Cleanup(srv.Close)
	x, _ := extract.Open("openai", extract.Config{BaseURL: srv.URL, Model: "m"})
	if _, err := x.Extract(context.Background(), "text"); err == nil || !strings.Contains(err.Error(), "no choices") {
		t.Fatalf("err = %v", err)
	}
}

func TestContradicts(t *testing.T) {
	for _, want := range []bool{true, false} {
		x, s := chat(t, fmt.Sprintf(`{"contradicts":%v}`, want))
		got, err := x.Contradicts(context.Background(), "CI runs Go 1.25", "CI runs Go 1.26")
		if err != nil || got != want {
			t.Fatalf("Contradicts = %v, %v, want %v", got, err, want)
		}
		if s.body.ResponseFormat.JSONSchema.Name != "memory_contradiction" || !strings.Contains(s.body.Messages[1].Content, "CI runs Go 1.25") {
			t.Fatalf("request = %+v", s.body)
		}
	}
}

func TestContradictsNeedsTheField(t *testing.T) {
	x, _ := chat(t, `{}`)
	if _, err := x.Contradicts(context.Background(), "a", "b"); err == nil {
		t.Fatal("an answer without contradicts was taken as false")
	}
	x, _ = chat(t, `{"facts":[]}`)
	if got, err := x.Extract(context.Background(), "hello"); err != nil || len(got) != 0 {
		t.Fatalf("an empty facts list = %v, %v", got, err)
	}
}

func TestOpen(t *testing.T) {
	if _, err := extract.Open("openai", extract.Config{BaseURL: "http://x"}); err == nil {
		t.Fatal("no model accepted")
	}
	if _, err := extract.Open("nope", extract.Config{}); memory.CodeOf(err) != memory.CodeInvalidArgument {
		t.Fatalf("unknown type = %v", err)
	}
}
