package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/raven-clown/logbook/embed"
	"github.com/raven-clown/logbook/extract"
	"github.com/raven-clown/logbook/internal/config"
	"github.com/raven-clown/logbook/memory"
	"github.com/raven-clown/logbook/staleness"
	"github.com/raven-clown/logbook/vector"
)

const (
	backfillEvery = 30 * time.Second
	purgeEvery    = time.Hour
	backfillBatch = 64
)

// serviceOptions turns the config into service options, opening the embedder,
// the vector index and the extractor when they are configured.
func serviceOptions(cfg config.Config, st memory.Store) (memory.Options, error) {
	p := cfg.ReviewPeriods
	t := cfg.Trust
	o := memory.Options{
		Rules: staleness.Default(),
		ReviewPeriods: map[memory.Kind]time.Duration{
			memory.KindObservation: p.Observation, memory.KindFact: p.Fact, memory.KindProcedure: p.Procedure,
			memory.KindDecision: p.Decision, memory.KindPreference: p.Preference,
		},
		Weights: memory.Weights{Verified: t.Weights.Verified, Unverified: t.Weights.Unverified, Disputed: t.Weights.Disputed, Stale: t.Weights.Stale},
		VerifyPolicy: map[memory.ScopeType]memory.Policy{
			memory.ScopeProject: memory.Policy(t.VerifyPolicy.Project), memory.ScopeTeam: memory.Policy(t.VerifyPolicy.Team),
		},
		MinStatus: map[memory.ScopeType]memory.Status{
			memory.ScopeUser: memory.Status(t.MinStatus.User), memory.ScopeProject: memory.Status(t.MinStatus.Project),
			memory.ScopeTeam: memory.Status(t.MinStatus.Team),
		},
	}
	if e := cfg.Embedder; e.Type != "" {
		if _, ok := st.(memory.EmbeddingStore); !ok {
			return o, fmt.Errorf("embedder: the %s storage driver does not store embeddings", cfg.Database.Driver)
		}
		emb, err := embed.Open(e.Type, embed.Config{BaseURL: e.BaseURL, Model: e.Model, APIKey: e.APIKey, Dimensions: e.Dimensions, ForwardToken: e.ForwardToken})
		if err != nil {
			return o, fmt.Errorf("embedder: %w", err)
		}
		idx, err := vector.Open("memory", memory.VectorLoader(st, emb.Model()))
		if err != nil {
			return o, fmt.Errorf("vector index: %w", err)
		}
		o.Embedder, o.Index = emb, idx
	}
	if x := cfg.Extractor; x.Type != "" {
		ext, err := extract.Open(x.Type, extract.Config{BaseURL: x.BaseURL, Model: x.Model, APIKey: x.APIKey, ForwardToken: x.ForwardToken})
		if err != nil {
			return o, fmt.Errorf("extractor: %w", err)
		}
		o.Extractor = ext
	}
	return o, nil
}

// startWorkers runs the purge loop and, with an embedder, the backfill loop
// until ctx ends. The returned function waits for them to stop.
func startWorkers(ctx context.Context, s *session) func() {
	var wg sync.WaitGroup
	wg.Go(func() {
		every(ctx, purgeEvery, func() {
			n, err := s.svc.PurgeForgotten(ctx, memory.DefaultOrg, s.cfg.ForgetRetention)
			switch {
			case err != nil && !errors.Is(err, context.Canceled):
				slog.WarnContext(ctx, "purging forgotten memories failed", "err", err)
			case n > 0:
				slog.InfoContext(ctx, "purged forgotten memories", "count", n)
			}
		})
	})
	if s.cfg.Embedder.Type != "" {
		wg.Go(func() {
			every(ctx, backfillEvery, func() {
				n, err := s.svc.Backfill(ctx, memory.DefaultOrg, backfillBatch)
				if n > 0 {
					slog.InfoContext(ctx, "embedded memories without a vector", "count", n)
				}
				if err != nil && !errors.Is(err, context.Canceled) {
					slog.WarnContext(ctx, "embedding backfill failed, retrying later", "err", err)
				}
			})
		})
	}
	return wg.Wait
}

// every runs fn now and then once per interval until ctx ends.
func every(ctx context.Context, interval time.Duration, fn func()) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		fn()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (a *app) reindex(args []string) error {
	fs, c := a.flags("reindex")
	all := fs.Bool("all", false, "embed every memory again, not only those without a vector for the current model")
	if _, err := parse(fs, args, 0, "reindex [--all] [--config file]"); err != nil {
		return err
	}
	s, err := a.open(c)
	if err != nil {
		return err
	}
	defer func() { _ = s.store.Close() }()
	dropped, embedded, err := s.svc.Reindex(a.ctx, memory.DefaultOrg, *all, backfillBatch)
	if err != nil {
		return err
	}
	if c.json {
		return a.printJSON(map[string]int{"dropped": dropped, "embedded": embedded})
	}
	a.println(fmt.Sprintf("dropped %d vectors, embedded %d memories", dropped, embedded))
	return nil
}
