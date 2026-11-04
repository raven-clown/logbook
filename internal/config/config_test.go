package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/raven-clown/logbook/internal/config"
)

func writeFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "logbook.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDefault(t *testing.T) {
	d := config.Default()
	if d.ListenAddr != ":8420" || d.Database.Driver != "sqlite" || d.Database.DSN != "logbook.db" || d.DefaultProject != "" {
		t.Fatalf("Default() = %+v", d)
	}
}

func TestLoad(t *testing.T) {
	file := writeFile(t, "database:\n  dsn: /tmp/x.db\ndefault_project: mothership\n")
	tests := []struct {
		name    string
		path    string
		env     []string
		check   func(config.Config) bool
		wantErr string
	}{
		{name: "nothing", check: func(c config.Config) bool { return c == config.Default() }},
		{name: "file", path: file, check: func(c config.Config) bool {
			return c.Database.DSN == "/tmp/x.db" && c.Database.Driver == "sqlite" && c.DefaultProject == "mothership"
		}},
		{name: "env beats file", path: file, env: []string{`LOGBOOK_DATABASE__DSN=C:\data\lb.db`}, check: func(c config.Config) bool {
			return c.Database.DSN == `C:\data\lb.db`
		}},
		{name: "top level env", env: []string{"LOGBOOK_LISTEN_ADDR=:9000"}, check: func(c config.Config) bool { return c.ListenAddr == ":9000" }},
		{name: "numeric looking value stays a string", env: []string{"LOGBOOK_DEFAULT_PROJECT=2026"}, check: func(c config.Config) bool { return c.DefaultProject == "2026" }},
		{name: "other variables ignored", env: []string{"PATH=/bin", "LOGBOOKX=1", "LOGBOOK_TOKEN=secret", "LOGBOOK_NOW=2026-11-04T00:00:00Z"}, check: func(c config.Config) bool { return c == config.Default() }},
		{name: "unknown env key", env: []string{"LOGBOOK_NOPE=1"}, wantErr: "LOGBOOK_NOPE"},
		{name: "unknown nested env key", env: []string{"LOGBOOK_DATABASE__NOPE=1"}, wantErr: "LOGBOOK_DATABASE__NOPE"},
		{name: "unknown file key", path: writeFile(t, "databse:\n  dsn: x\n"), wantErr: "databse"},
		{name: "missing file", path: filepath.Join(t.TempDir(), "nope.yaml"), wantErr: "nope.yaml"},
		{name: "bad yaml", path: writeFile(t, "database: [\n"), wantErr: "logbook.yaml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := config.Load(tt.path, tt.env)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want one naming %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !tt.check(c) {
				t.Fatalf("config = %+v", c)
			}
		})
	}
}

func TestLoadSectionFromEnv(t *testing.T) {
	_, err := config.Load("", []string{"LOGBOOK_DATABASE=x"})
	if err == nil || !strings.Contains(err.Error(), "LOGBOOK_DATABASE") {
		t.Fatalf("err = %v", err)
	}
	_, err = config.Load("", []string{"LOGBOOK_LISTEN_ADDR__X=1"})
	if err == nil || !strings.Contains(err.Error(), "LOGBOOK_LISTEN_ADDR__X") {
		t.Fatalf("err = %v", err)
	}
}
