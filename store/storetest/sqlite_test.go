package storetest_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/raven-clown/logbook/memory"
	"github.com/raven-clown/logbook/store/sqlite"
	"github.com/raven-clown/logbook/store/storetest"
)

func TestSuiteOnSQLite(t *testing.T) {
	storetest.Run(t, func(t *testing.T) memory.Store {
		s, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "logbook.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	})
}
