package jsonl_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/raven-clown/logbook/importer/jsonl"
	"github.com/raven-clown/logbook/memory"
)

// FuzzImport reads any input as an export. Each record it accepts must be written by Write and read back unchanged.
func FuzzImport(f *testing.F) {
	f.Add([]byte(`{"memory":{"content":"a good one","kind":"decision","tags":["go"],"scope":"team:platform"}}` + "\n\n" + `not json` + "\n"))
	f.Add([]byte(`{"memory":{"content":"x` + string(rune(0x202e)) + `\u0085y"},"edges":[{"from":"a","to":"b","type":"relates_to"}],"verifications":[{"verdict":"confirmed"}]}`))
	f.Add([]byte(`{"memory":{"content":"` + strings.Repeat("a", 100) + `","created_at":"2026-11-04T00:00:00+07:00"}}` + "\r\n"))
	f.Add([]byte("{\"memory\":{\"content\":\"\xff\"}}\n[]\nnull\n"))
	f.Fuzz(func(t *testing.T, in []byte) {
		var recs []memory.Record
		err := jsonl.Importer{}.Import(context.Background(), bytes.NewReader(in),
			func(_ int, r memory.Record) error { recs = append(recs, r); return nil },
			func(int, error) {})
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
		for _, rec := range recs {
			var buf bytes.Buffer
			if err := jsonl.Write(&buf, rec); err != nil {
				t.Fatalf("Write(%+v): %v", rec, err)
			}
			var back []memory.Record
			skipped := 0
			if err := (jsonl.Importer{}).Import(context.Background(), &buf,
				func(_ int, r memory.Record) error { back = append(back, r); return nil },
				func(int, error) { skipped++ }); err != nil || skipped > 0 || len(back) != 1 {
				t.Fatalf("reading back %q: %d records, %d skipped, %v", buf.String(), len(back), skipped, err)
			}
			want, _ := json.Marshal(rec)
			got, _ := json.Marshal(back[0])
			if !bytes.Equal(want, got) {
				t.Fatalf("round trip changed the record:\n%s\n%s", want, got)
			}
		}
	})
}
