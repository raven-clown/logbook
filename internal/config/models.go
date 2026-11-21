package config

import (
	"fmt"
	"math"
	"time"
)

// Embedder configures the embedding endpoint. An empty type turns embeddings off.
type Embedder struct {
	Type    string `yaml:"type"`
	BaseURL string `yaml:"base_url"`
	Model   string `yaml:"model"`
	// Dimensions asks the endpoint for shorter vectors when it supports that. Zero keeps the model's size.
	Dimensions int    `yaml:"dimensions"`
	APIKey     string `yaml:"api_key"`
	// ForwardToken sends the caller's OIDC token instead of APIKey. Set it for transponder only.
	ForwardToken bool `yaml:"forward_token"`
}

// Extractor configures the fact extraction endpoint. An empty type turns extraction off.
type Extractor struct {
	Type         string `yaml:"type"`
	BaseURL      string `yaml:"base_url"`
	Model        string `yaml:"model"`
	APIKey       string `yaml:"api_key"`
	ForwardToken bool   `yaml:"forward_token"`
}

// Trust holds the verify policies, default minimum statuses and ranking weights.
type Trust struct {
	VerifyPolicy VerifyPolicy `yaml:"verify_policy"`
	MinStatus    MinStatus    `yaml:"min_status"`
	Weights      Weights      `yaml:"weights"`
}

// VerifyPolicy says whose confirms count in each scope type: writers or verifiers.
type VerifyPolicy struct {
	Project string `yaml:"project"`
	Team    string `yaml:"team"`
}

// MinStatus is the status recall requires by default in each scope type.
type MinStatus struct {
	User    string `yaml:"user"`
	Project string `yaml:"project"`
	Team    string `yaml:"team"`
}

// Weights scale recall scores by trust.
type Weights struct {
	Verified   float64 `yaml:"verified"`
	Unverified float64 `yaml:"unverified"`
	Disputed   float64 `yaml:"disputed"`
	Stale      float64 `yaml:"stale"`
}

// ReviewPeriods is how long each kind of memory stays fresh. Zero means never stale.
type ReviewPeriods struct {
	Observation time.Duration `yaml:"observation"`
	Fact        time.Duration `yaml:"fact"`
	Procedure   time.Duration `yaml:"procedure"`
	Decision    time.Duration `yaml:"decision"`
	Preference  time.Duration `yaml:"preference"`
}

func defaultTrust() Trust {
	return Trust{
		VerifyPolicy: VerifyPolicy{Project: "writers", Team: "writers"},
		MinStatus:    MinStatus{User: "unverified", Project: "unverified", Team: "unverified"},
		Weights:      Weights{Verified: 1.0, Unverified: 0.7, Disputed: 0.3, Stale: 0.5},
	}
}

func defaultReviewPeriods() ReviewPeriods {
	return ReviewPeriods{Observation: 720 * time.Hour, Fact: 4320 * time.Hour, Procedure: 4320 * time.Hour, Decision: 8760 * time.Hour}
}

func (c Config) validateModels() error {
	if err := validateEndpoint("embedder", c.Embedder.Type, c.Embedder.BaseURL, c.Embedder.Model); err != nil {
		return err
	}
	if c.Embedder.Dimensions < 0 {
		return fmt.Errorf("embedder.dimensions must not be negative, not %d", c.Embedder.Dimensions)
	}
	return validateEndpoint("extractor", c.Extractor.Type, c.Extractor.BaseURL, c.Extractor.Model)
}

func validateEndpoint(key, typ, baseURL, model string) error {
	if typ == "" {
		return nil
	}
	if !isHTTPURL(baseURL) {
		return fmt.Errorf("%s.base_url must be an http or https URL, not %q", key, baseURL)
	}
	if model == "" {
		return fmt.Errorf("%s.model is required when %s.type is set", key, key)
	}
	return nil
}

func (c Config) validateTrust() error {
	t := c.Trust
	for key, v := range map[string]string{"project": t.VerifyPolicy.Project, "team": t.VerifyPolicy.Team} {
		if v != "writers" && v != "verifiers" {
			return fmt.Errorf("trust.verify_policy.%s must be writers or verifiers, not %q", key, v)
		}
	}
	for key, v := range map[string]string{"user": t.MinStatus.User, "project": t.MinStatus.Project, "team": t.MinStatus.Team} {
		if v != "unverified" && v != "verified" {
			return fmt.Errorf("trust.min_status.%s must be unverified or verified, not %q", key, v)
		}
	}
	w := t.Weights
	for key, v := range map[string]float64{"verified": w.Verified, "unverified": w.Unverified, "disputed": w.Disputed, "stale": w.Stale} {
		if math.IsNaN(v) || v < 0 || v > 1 {
			return fmt.Errorf("trust.weights.%s must be between 0 and 1, not %v", key, v)
		}
	}
	p := c.ReviewPeriods
	for key, d := range map[string]time.Duration{
		"observation": p.Observation, "fact": p.Fact, "procedure": p.Procedure, "decision": p.Decision, "preference": p.Preference,
	} {
		if d < 0 {
			return fmt.Errorf("review_periods.%s must not be negative, not %v", key, d)
		}
	}
	if c.ForgetRetention < 0 {
		return fmt.Errorf("forget_retention must not be negative, not %v", c.ForgetRetention)
	}
	return nil
}
