package importer_test

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/raven-clown/logbook/importer"
	"github.com/raven-clown/logbook/memory"
	"github.com/raven-clown/logbook/store/sqlite"
)

type fakeImporter struct{}

func (fakeImporter) Import(_ context.Context, _ io.Reader, emit func(int, memory.Record) error, skip func(int, error)) error {
	skip(1, errors.New("bad line"))
	return emit(2, memory.Record{Memory: memory.Memory{Content: "x"}})
}

func TestRegistry(t *testing.T) {
	importer.Register("fake-registry", fakeImporter{})
	if _, err := importer.Get("fake-registry"); err != nil {
		t.Fatal(err)
	}
	_, err := importer.Get("nope")
	var e *memory.Error
	if !errors.As(err, &e) || e.Code != memory.CodeInvalidArgument || e.Field != "format" {
		t.Fatalf("Get(nope) = %v", err)
	}
	if names := importer.Names(); !slices.IsSorted(names) || !slices.Contains(names, "fake-registry") {
		t.Fatalf("Names() = %v", names)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("registering twice should panic")
		}
	}()
	importer.Register("fake-registry", fakeImporter{})
}

func TestRunReportsByLine(t *testing.T) {
	st, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "logbook.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	svc := memory.NewService(st, memory.Options{})
	p := memory.Principal{OrgID: memory.DefaultOrg, Subject: "alice", Channel: memory.ChannelCLI}
	report, err := importer.Run(context.Background(), svc, p, "", fakeImporter{}, strings.NewReader(""), false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Imported != 1 || len(report.Skipped) != 1 || report.Skipped[0].Line != 1 || report.Skipped[0].Err != "bad line" {
		t.Fatalf("report = %+v", report)
	}
}

func TestRunDryRunWritesNothing(t *testing.T) {
	st, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "logbook.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	svc := memory.NewService(st, memory.Options{})
	p := memory.Principal{OrgID: memory.DefaultOrg, Subject: "alice", Channel: memory.ChannelCLI}
	report, err := importer.Run(context.Background(), svc, p, "", fakeImporter{}, strings.NewReader(""), true)
	if err != nil {
		t.Fatal(err)
	}
	if report.Imported != 1 || len(report.Skipped) != 1 || report.Skipped[0].Line != 1 {
		t.Fatalf("report = %+v", report)
	}
	res, err := svc.List(context.Background(), p, memory.ListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 0 {
		t.Fatalf("dry run stored %d memories", len(res.Items))
	}
	report, err = importer.Run(context.Background(), svc, p, "", fakeImporter{}, strings.NewReader(""), false)
	if err != nil || report.Imported != 1 {
		t.Fatalf("import = %+v, %v", report, err)
	}
	res, err = svc.List(context.Background(), p, memory.ListRequest{})
	if err != nil || len(res.Items) != 1 || res.Items[0].Content != "x" {
		t.Fatalf("after import: %+v, %v", res.Items, err)
	}
}
