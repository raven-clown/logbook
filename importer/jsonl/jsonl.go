// Package jsonl is logbook's own export format: one JSON record per line.
package jsonl

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/raven-clown/logbook/importer"
	"github.com/raven-clown/logbook/internal/safejson"
	"github.com/raven-clown/logbook/memory"
)

func init() {
	importer.Register("jsonl", Importer{})
}

// Importer reads JSONL written by Write.
type Importer struct{}

// Import reads one record per line. Blank lines are ignored.
func (Importer) Import(ctx context.Context, r io.Reader, emit func(int, memory.Record) error, skip func(int, error)) error {
	br := bufio.NewReader(r)
	for line := 1; ; line++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		raw, err := br.ReadBytes('\n')
		if len(bytes.TrimSpace(raw)) > 0 {
			var rec memory.Record
			if jerr := json.Unmarshal(raw, &rec); jerr != nil {
				skip(line, fmt.Errorf("not a JSON record: %w", jerr))
			} else if eerr := emit(line, rec); eerr != nil {
				return eerr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading line %d: %w", line, err)
		}
	}
}

// Write writes rec as one line.
func Write(w io.Writer, rec memory.Record) error {
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	_, err = w.Write(append(safejson.Escape(b), '\n'))
	return err
}
