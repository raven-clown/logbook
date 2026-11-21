package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/raven-clown/logbook/internal/config"
)

func TestModelAndTrustDefaults(t *testing.T) {
	d := config.Default()
	if d.Embedder.Type != "" || d.Extractor.Type != "" {
		t.Fatalf("models are on by default: %+v %+v", d.Embedder, d.Extractor)
	}
	if d.Trust.VerifyPolicy.Project != "writers" || d.Trust.VerifyPolicy.Team != "writers" {
		t.Fatalf("verify_policy = %+v", d.Trust.VerifyPolicy)
	}
	if d.Trust.MinStatus.User != "unverified" || d.Trust.MinStatus.Project != "unverified" || d.Trust.MinStatus.Team != "unverified" {
		t.Fatalf("min_status = %+v", d.Trust.MinStatus)
	}
	if w := d.Trust.Weights; w.Verified != 1 || w.Unverified != 0.7 || w.Disputed != 0.3 || w.Stale != 0.5 {
		t.Fatalf("weights = %+v", w)
	}
	p := d.ReviewPeriods
	if p.Observation != 720*time.Hour || p.Fact != 4320*time.Hour || p.Procedure != 4320*time.Hour || p.Decision != 8760*time.Hour || p.Preference != 0 {
		t.Fatalf("review_periods = %+v", p)
	}
	if d.ForgetRetention != 720*time.Hour {
		t.Fatalf("forget_retention = %v", d.ForgetRetention)
	}
}

func TestEnvTypes(t *testing.T) {
	c, err := config.Load("", []string{
		"LOGBOOK_EMBEDDER__TYPE=openai",
		"LOGBOOK_EMBEDDER__BASE_URL=http://transponder:8080/v1",
		"LOGBOOK_EMBEDDER__MODEL=text-embedding-3-small",
		"LOGBOOK_EMBEDDER__DIMENSIONS=512",
		"LOGBOOK_EXTRACTOR__FORWARD_TOKEN=true",
		"LOGBOOK_TRUST__WEIGHTS__STALE=0.25",
		"LOGBOOK_TRUST__VERIFY_POLICY__TEAM=verifiers",
		"LOGBOOK_REVIEW_PERIODS__FACT=48h",
		"LOGBOOK_FORGET_RETENTION=48h",
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Embedder.Dimensions != 512 || !c.Extractor.ForwardToken || c.Trust.Weights.Stale != 0.25 ||
		c.Trust.VerifyPolicy.Team != "verifiers" || c.ReviewPeriods.Fact != 48*time.Hour || c.ForgetRetention != 48*time.Hour {
		t.Fatalf("config = %+v", c)
	}
}

func TestEnvBadValue(t *testing.T) {
	for _, env := range []string{
		"LOGBOOK_EMBEDDER__DIMENSIONS=many",
		"LOGBOOK_EXTRACTOR__FORWARD_TOKEN=maybe",
		"LOGBOOK_TRUST__WEIGHTS__STALE=half",
		"LOGBOOK_FORGET_RETENTION=30d",
	} {
		name, _, _ := strings.Cut(env, "=")
		t.Run(name, func(t *testing.T) {
			_, err := config.Load("", []string{env})
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("err = %v, want one naming %s", err, name)
			}
		})
	}
}

func TestModelsFromFile(t *testing.T) {
	c, err := config.Load(writeFile(t, `embedder:
  type: openai
  base_url: http://transponder:8080/v1
  model: text-embedding-3-small
  dimensions: 256
  api_key: sk-test
  forward_token: true
extractor:
  type: openai
  base_url: http://transponder:8080/v1
  model: memory-extract
trust:
  verify_policy: {project: writers, team: verifiers}
  min_status: {team: verified}
  weights: {verified: 1, unverified: 0.6, disputed: 0.2, stale: 0.4}
review_periods:
  observation: 168h
  preference: 0s
forget_retention: 240h
`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Embedder.APIKey != "sk-test" || c.Embedder.Dimensions != 256 || !c.Embedder.ForwardToken || c.Extractor.Model != "memory-extract" {
		t.Fatalf("models = %+v %+v", c.Embedder, c.Extractor)
	}
	if c.Trust.MinStatus.Team != "verified" || c.Trust.MinStatus.User != "unverified" || c.Trust.Weights.Unverified != 0.6 {
		t.Fatalf("trust = %+v", c.Trust)
	}
	if c.ReviewPeriods.Observation != 168*time.Hour || c.ReviewPeriods.Fact != 4320*time.Hour || c.ForgetRetention != 240*time.Hour {
		t.Fatalf("periods = %+v, retention %v", c.ReviewPeriods, c.ForgetRetention)
	}
}

func TestValidateModelsAndTrust(t *testing.T) {
	model := []string{"LOGBOOK_EMBEDDER__TYPE=openai", "LOGBOOK_EMBEDDER__BASE_URL=http://t/v1", "LOGBOOK_EMBEDDER__MODEL=m"}
	for _, tt := range []struct {
		name string
		env  []string
		want string
	}{
		{"embedder base url", []string{"LOGBOOK_EMBEDDER__TYPE=openai", "LOGBOOK_EMBEDDER__MODEL=m"}, "embedder.base_url"},
		{"embedder model", []string{"LOGBOOK_EMBEDDER__TYPE=openai", "LOGBOOK_EMBEDDER__BASE_URL=http://t/v1"}, "embedder.model"},
		{"embedder url scheme", []string{"LOGBOOK_EMBEDDER__TYPE=openai", "LOGBOOK_EMBEDDER__BASE_URL=t/v1", "LOGBOOK_EMBEDDER__MODEL=m"}, "embedder.base_url"},
		{"dimensions", append(model, "LOGBOOK_EMBEDDER__DIMENSIONS=-1"), "embedder.dimensions"},
		{"extractor model", []string{"LOGBOOK_EXTRACTOR__TYPE=openai", "LOGBOOK_EXTRACTOR__BASE_URL=http://t/v1"}, "extractor.model"},
		{"verify policy", []string{"LOGBOOK_TRUST__VERIFY_POLICY__PROJECT=everyone"}, "trust.verify_policy.project"},
		{"min status", []string{"LOGBOOK_TRUST__MIN_STATUS__USER=disputed"}, "trust.min_status.user"},
		{"weight low", []string{"LOGBOOK_TRUST__WEIGHTS__DISPUTED=-0.1"}, "trust.weights.disputed"},
		{"weight high", []string{"LOGBOOK_TRUST__WEIGHTS__VERIFIED=1.5"}, "trust.weights.verified"},
		{"period", []string{"LOGBOOK_REVIEW_PERIODS__DECISION=-1h"}, "review_periods.decision"},
		{"retention", []string{"LOGBOOK_FORGET_RETENTION=-1h"}, "forget_retention"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.Load("", tt.env)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want one naming %q", err, tt.want)
			}
		})
	}
	if _, err := config.Load("", model); err != nil {
		t.Fatalf("valid embedder rejected: %v", err)
	}
}
