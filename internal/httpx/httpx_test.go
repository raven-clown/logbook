package httpx_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/raven-clown/logbook/internal/httpx"
	"github.com/raven-clown/logbook/memory"
)

func TestStatus(t *testing.T) {
	for code, want := range map[memory.Code]int{
		memory.CodeInvalidArgument: 400, memory.CodeUnauthenticated: 401, memory.CodePermissionDenied: 403,
		memory.CodeNotFound: 404, memory.CodeConflict: 409, memory.CodeVersionConflict: 409,
		memory.CodeUnavailable: 503, memory.CodeInternal: 500, "weird": 500,
	} {
		if got := httpx.Status(code); got != want {
			t.Fatalf("Status(%s) = %d, want %d", code, got, want)
		}
	}
}

type body struct {
	Name  string   `json:"name"`
	Count int      `json:"count"`
	Tags  []string `json:"tags"`
}

func TestDecodeJSON(t *testing.T) {
	for _, tt := range []struct {
		name, in, field string
		ok              bool
	}{
		{"good", `{"name":"a","count":2}`, "", true},
		{"unknown field", `{"name":"a","label":"b"}`, "label", false},
		{"wrong type", `{"count":"two"}`, "count", false},
		{"nested wrong type", `{"tags":[1]}`, "tags", false},
		{"bad json", `{"name":`, "", false},
		{"empty", ``, "", false},
		{"trailing data", `{"name":"a"} {"name":"b"}`, "", false},
		{"too big", `{"name":"` + strings.Repeat("a", 300<<10) + `"}`, "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", strings.NewReader(tt.in))
			var b body
			err := httpx.DecodeJSON(req, &b)
			if tt.ok {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var e *memory.Error
			if !errors.As(err, &e) || e.Code != memory.CodeInvalidArgument || e.Field != tt.field {
				t.Fatalf("err = %v, want invalid_argument on %q", err, tt.field)
			}
		})
	}
}

func TestWriteError(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	httpx.WriteError(rec, req, memory.Errorf(memory.CodeNotFound, "id", "memory mem_1 not found"))
	var got map[string]map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 404 || got["error"]["code"] != "not_found" || got["error"]["field"] != "id" || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("%d %v", rec.Code, got)
	}
	rec = httptest.NewRecorder()
	httpx.WriteError(rec, req, errors.New("disk on fire at /var/lib/secret"))
	if rec.Code != 500 || bytes.Contains(rec.Body.Bytes(), []byte("secret")) || !bytes.Contains(rec.Body.Bytes(), []byte(`"internal"`)) {
		t.Fatalf("internal error = %d %s", rec.Code, rec.Body)
	}
}

func TestDecodeJSONTooBig(t *testing.T) {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", strings.NewReader(`{"name":"`+strings.Repeat("a", 300<<10)+`"}`))
	var b body
	if err := httpx.DecodeJSON(req, &b); err == nil || !strings.Contains(err.Error(), "256 KiB") {
		t.Fatalf("err = %v, want the size limit named", err)
	}
}
