// Package config loads logbook's YAML config and applies LOGBOOK_* environment
// overrides, with __ between nested keys: LOGBOOK_DATABASE__DSN sets database.dsn.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the file and environment configuration.
type Config struct {
	ListenAddr     string   `yaml:"listen_addr"`
	Database       Database `yaml:"database"`
	DefaultProject string   `yaml:"default_project"`
}

// Database says where memories are stored.
type Database struct {
	Driver string `yaml:"driver"`
	DSN    string `yaml:"dsn"`
}

// Default is the configuration with no file and no environment: SQLite in the
// working directory.
func Default() Config {
	return Config{ListenAddr: ":8420", Database: Database{Driver: "sqlite", DSN: "logbook.db"}}
}

const prefix = "LOGBOOK_"

// reserved variables belong to the command line, not to the config.
var reserved = []string{"LOGBOOK_TOKEN", "LOGBOOK_NOW"}

// Load reads path, when set, over the defaults and then applies the LOGBOOK_*
// variables in environ. Unknown keys in either are an error.
func Load(path string, environ []string) (Config, error) {
	c := Default()
	if path != "" {
		data, err := os.ReadFile(path) // #nosec G304 -- the config path is given by the operator
		if err != nil {
			return c, fmt.Errorf("reading config: %w", err)
		}
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) {
			return c, fmt.Errorf("config %s: %w", path, err)
		}
	}
	for _, kv := range environ {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(name, prefix) || slices.Contains(reserved, name) {
			continue
		}
		keys := strings.Split(strings.ToLower(strings.TrimPrefix(name, prefix)), "__")
		if err := set(reflect.ValueOf(&c).Elem(), keys, value); err != nil {
			return c, fmt.Errorf("environment variable %s: %w", name, err)
		}
	}
	return c, nil
}

func set(v reflect.Value, keys []string, value string) error {
	for i := range v.NumField() {
		tag, _, _ := strings.Cut(v.Type().Field(i).Tag.Get("yaml"), ",")
		if tag != keys[0] {
			continue
		}
		f := v.Field(i)
		if len(keys) > 1 {
			if f.Kind() != reflect.Struct {
				return fmt.Errorf("%s has no nested keys", keys[0])
			}
			return set(f, keys[1:], value)
		}
		return assign(f, value)
	}
	return fmt.Errorf("no config key %q", keys[0])
}

func assign(f reflect.Value, value string) error {
	if f.Kind() != reflect.String {
		return fmt.Errorf("is a section, not a value")
	}
	f.SetString(value)
	return nil
}
