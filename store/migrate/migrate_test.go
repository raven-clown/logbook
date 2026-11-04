package migrate_test

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/raven-clown/logbook/store/migrate"
	_ "modernc.org/sqlite"
)

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func file(sql string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(sql)} }

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), "SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func TestRunAppliesInOrderOnce(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	files := fstest.MapFS{
		"0002_b.sql": file("CREATE TABLE b (id INTEGER REFERENCES a(id));"),
		"0001_a.sql": file("CREATE TABLE a (id INTEGER PRIMARY KEY);"),
		"notes.txt":  file("not a migration"),
	}
	applied, err := migrate.Run(ctx, db, files)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(applied, []string{"0001_a.sql", "0002_b.sql"}) {
		t.Fatalf("applied = %v", applied)
	}
	applied, err = migrate.Run(ctx, db, files)
	if err != nil || len(applied) != 0 {
		t.Fatalf("second run applied %v, %v", applied, err)
	}
	files["0003_c.sql"] = file("CREATE TABLE c (id INTEGER);")
	applied, err = migrate.Run(ctx, db, files)
	if err != nil || !reflect.DeepEqual(applied, []string{"0003_c.sql"}) {
		t.Fatalf("third run applied %v, %v", applied, err)
	}
}

func TestRunRejectsGap(t *testing.T) {
	_, err := migrate.Run(context.Background(), openDB(t), fstest.MapFS{
		"0001_a.sql": file("CREATE TABLE a (id INTEGER);"),
		"0003_c.sql": file("CREATE TABLE c (id INTEGER);"),
	})
	if err == nil || !strings.Contains(err.Error(), "0002") {
		t.Fatalf("err = %v, want a gap naming 0002", err)
	}
}

func TestRunRejectsChangedFile(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	files := fstest.MapFS{"0001_a.sql": file("CREATE TABLE a (id INTEGER);")}
	if _, err := migrate.Run(ctx, db, files); err != nil {
		t.Fatal(err)
	}
	files["0001_a.sql"] = file("CREATE TABLE a (id INTEGER, name TEXT);")
	_, err := migrate.Run(ctx, db, files)
	if err == nil || !strings.Contains(err.Error(), "0001_a.sql") {
		t.Fatalf("err = %v, want the changed file named", err)
	}
}

func TestRunRollsBackBrokenFile(t *testing.T) {
	db := openDB(t)
	_, err := migrate.Run(context.Background(), db, fstest.MapFS{
		"0001_a.sql": file("CREATE TABLE a (id INTEGER); CREATE TABLE oops ("),
	})
	if err == nil || !strings.Contains(err.Error(), "0001_a.sql") {
		t.Fatalf("err = %v, want failure naming the file", err)
	}
	if tableExists(t, db, "a") {
		t.Fatal("table from the failed migration survived")
	}
}

func TestRunLineEndingsDoNotChangeChecksum(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	if _, err := migrate.Run(ctx, db, fstest.MapFS{"0001_a.sql": file("CREATE TABLE a (\n  id INTEGER\n);\n")}); err != nil {
		t.Fatal(err)
	}
	applied, err := migrate.Run(ctx, db, fstest.MapFS{"0001_a.sql": file("CREATE TABLE a (\r\n  id INTEGER\r\n);\r\n")})
	if err != nil || len(applied) != 0 {
		t.Fatalf("CRLF copy: applied %v, err %v", applied, err)
	}
}

func TestRunRejectsBadName(t *testing.T) {
	_, err := migrate.Run(context.Background(), openDB(t), fstest.MapFS{"01_a.sql": file("SELECT 1;")})
	if err == nil || !strings.Contains(err.Error(), "01_a.sql") {
		t.Fatalf("err = %v, want the bad name reported", err)
	}
}
