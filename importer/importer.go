// Package importer brings memories in from other formats. Each format is an
// Importer registered by name, so `logbook import <format> <path>` can find it.
package importer

import (
	"context"
	"fmt"
	"io"
	"maps"
	"slices"
	"sync"

	"github.com/raven-clown/logbook/memory"
)

// Importer reads one format. It calls emit for each record and skip for each
// part of the input it could not read, both with the line number it came from.
type Importer interface {
	Import(ctx context.Context, r io.Reader, emit func(line int, rec memory.Record) error, skip func(line int, err error)) error
}

var (
	mu        sync.RWMutex
	importers = map[string]Importer{}
)

// Register makes an importer available under name. It panics if name is taken.
func Register(name string, imp Importer) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := importers[name]; dup {
		panic(fmt.Sprintf("importer: %q registered twice", name))
	}
	importers[name] = imp
}

// Get returns the importer registered as name.
func Get(name string) (Importer, error) {
	mu.RLock()
	defer mu.RUnlock()
	imp, ok := importers[name]
	if !ok {
		return nil, memory.Errorf(memory.CodeInvalidArgument, "format", "unknown import format %q, have %v", name, slices.Sorted(maps.Keys(importers)))
	}
	return imp, nil
}

// Names returns the registered formats, sorted.
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	return slices.Sorted(maps.Keys(importers))
}

// LineError is one skipped part of an import.
type LineError struct {
	Line int    `json:"line"`
	Err  string `json:"error"`
}

// Report sums up an import.
type Report struct {
	Imported int         `json:"imported"`
	Skipped  []LineError `json:"skipped,omitempty"`
}

// Run reads r with imp and stores what it finds through svc.
func Run(ctx context.Context, svc *memory.Service, p memory.Principal, scope string, imp Importer, r io.Reader) (*Report, error) {
	var recs []memory.Record
	var lines []int
	report := &Report{}
	err := imp.Import(ctx, r, func(line int, rec memory.Record) error {
		recs = append(recs, rec)
		lines = append(lines, line)
		return nil
	}, func(line int, err error) {
		report.Skipped = append(report.Skipped, LineError{Line: line, Err: err.Error()})
	})
	if err != nil {
		return nil, err
	}
	outcomes, err := svc.ImportBatch(ctx, p, scope, recs)
	if err != nil {
		return nil, err
	}
	for i, o := range outcomes {
		if o.Err != nil {
			report.Skipped = append(report.Skipped, LineError{Line: lines[i], Err: o.Err.Error()})
			continue
		}
		report.Imported++
	}
	slices.SortStableFunc(report.Skipped, func(a, b LineError) int { return a.Line - b.Line })
	return report, nil
}
