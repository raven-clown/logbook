// Package config loads logbook's YAML config and applies LOGBOOK_* environment
// overrides, with __ between nested keys: LOGBOOK_DATABASE__DSN sets database.dsn.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"reflect"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the file and environment configuration.
type Config struct {
	ListenAddr string `yaml:"listen_addr"`
	// PublicURL is the address clients use, for the OAuth protected resource metadata.
	PublicURL      string    `yaml:"public_url"`
	Database       Database  `yaml:"database"`
	DefaultProject string    `yaml:"default_project"`
	Auth           Auth      `yaml:"auth"`
	Telemetry      Telemetry `yaml:"telemetry"`
}

// Auth says how callers are identified.
type Auth struct {
	// BootstrapAdmins are binding principals that always hold the admin role.
	BootstrapAdmins []string `yaml:"bootstrap_admins"`
	OIDC            OIDC     `yaml:"oidc"`
	// ProjectMode is open (any caller reads every project) or restricted.
	ProjectMode string `yaml:"project_mode"`
}

// OIDC configures JWT checks. An empty issuer turns JWTs off.
type OIDC struct {
	Issuer        string `yaml:"issuer"`
	Audience      string `yaml:"audience"`
	TeamsClaim    string `yaml:"teams_claim"`
	ProjectsClaim string `yaml:"projects_claim"`
}

// Telemetry configures trace export.
type Telemetry struct {
	OTLPEndpoint string `yaml:"otlp_endpoint"`
}

// Database says where memories are stored.
type Database struct {
	Driver string `yaml:"driver"`
	DSN    string `yaml:"dsn"`
}

// Default is the configuration with no file and no environment: SQLite in the
// working directory.
func Default() Config {
	return Config{
		ListenAddr: ":8420",
		Database:   Database{Driver: "sqlite", DSN: "logbook.db"},
		Auth:       Auth{ProjectMode: "open", OIDC: OIDC{TeamsClaim: "groups"}},
	}
}

const prefix = "LOGBOOK_"

// reserved variables belong to the command line, not to the config.
var reserved = []string{"LOGBOOK_TOKEN", "LOGBOOK_NOW", "LOGBOOK_SECRET_KEY", "LOGBOOK_TEST_POSTGRES_DSN"}

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
	return c, c.Validate()
}

// Validate checks values that have a fixed form.
func (c Config) Validate() error {
	if m := c.Auth.ProjectMode; m != "open" && m != "restricted" {
		return fmt.Errorf("auth.project_mode must be open or restricted, not %q", m)
	}
	if c.Auth.OIDC.Issuer != "" {
		if !isHTTPURL(c.Auth.OIDC.Issuer) {
			return fmt.Errorf("auth.oidc.issuer must be an http or https URL, not %q", c.Auth.OIDC.Issuer)
		}
		if c.Auth.OIDC.Audience == "" {
			return errors.New("auth.oidc.audience is required when auth.oidc.issuer is set")
		}
	}
	for _, a := range c.Auth.BootstrapAdmins {
		typ, id, ok := strings.Cut(a, ":")
		if !ok || id == "" || (typ != "user" && typ != "group" && typ != "key") {
			return fmt.Errorf("auth.bootstrap_admins entry %q must be user:<subject>, group:<name> or key:<name>", a)
		}
	}
	if c.PublicURL != "" && !isHTTPURL(c.PublicURL) {
		return fmt.Errorf("public_url must be an http or https URL, not %q", c.PublicURL)
	}
	return nil
}

func isHTTPURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
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
	switch {
	case f.Kind() == reflect.String:
		f.SetString(value)
	case f.Kind() == reflect.Slice && f.Type().Elem().Kind() == reflect.String:
		var list []string
		for v := range strings.SplitSeq(value, ",") {
			if v = strings.TrimSpace(v); v != "" {
				list = append(list, v)
			}
		}
		f.Set(reflect.ValueOf(list))
	default:
		return fmt.Errorf("is a section, not a value")
	}
	return nil
}
