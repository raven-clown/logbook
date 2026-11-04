package memory

import (
	"context"
	"errors"
	"time"
)

// Record is a memory with its links and verifications, as exported and imported.
type Record struct {
	Memory        Memory         `json:"memory"`
	Edges         []Edge         `json:"edges,omitempty"`
	Verifications []Verification `json:"verifications,omitempty"`
}

// ImportOutcome is what happened to one record of an import. Err is set when
// the record was skipped.
type ImportOutcome struct {
	ID  string
	Err error
}

var errSkip = errors.New("skip")

// ImportBatch stores records in one transaction. A record keeps its ID when it
// is free and gets a new one otherwise; supersede links, edges and verifications
// are rewritten to the new IDs. scope is used for records without one.
// A record that fails validation is skipped and reported in its outcome.
//
// Imported trust is not taken on faith: verified memories arrive unverified, the
// importer becomes the author, the review date is worked out again from the kind
// and a creation time that is never in the future, and imported verifications
// are kept as history without counting. Lower-trust states (disputed,
// superseded, forgotten) survive.
func (s *Service) ImportBatch(ctx context.Context, p Principal, scope string, recs []Record) ([]ImportOutcome, error) {
	out := make([]ImportOutcome, len(recs))
	err := s.store.Tx(ctx, func(st Store) error {
		ids := map[string]string{}
		stored := make([]*Memory, len(recs))
		for i, rec := range recs {
			m, err := s.importMemory(ctx, st, p, scope, rec.Memory)
			if err != nil {
				if CodeOf(err) == CodeInternal {
					return err
				}
				out[i].Err = err
				continue
			}
			stored[i], out[i].ID = m, m.ID
			if rec.Memory.ID != "" {
				ids[rec.Memory.ID] = m.ID
			}
		}
		for i, rec := range recs {
			if stored[i] == nil {
				continue
			}
			if err := s.relink(ctx, st, p, stored[i], rec, ids); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) importMemory(ctx context.Context, st Store, p Principal, scope string, in Memory) (*Memory, error) {
	content, err := validContent(in.Content)
	if err != nil {
		return nil, err
	}
	kind, err := ParseKind(string(in.Kind))
	if err != nil {
		return nil, err
	}
	if err := validMeta(RememberRequest{Subject: in.Subject, Evidence: in.Evidence, Tags: in.Tags, Refs: in.Refs, Source: in.Source}); err != nil {
		return nil, err
	}
	target := in.Scope
	if target == "" {
		target = scope
	}
	sc, err := writableScope(p, target, "", "")
	if err != nil {
		return nil, err
	}
	m := in
	m.OrgID, m.Scope, m.Content, m.Kind, m.Version, m.Purged = p.OrgID, sc.String(), content, kind, 1, false
	m.ContentHash = ContentHash(content)
	if dup, err := st.FindByHash(ctx, p.OrgID, m.Scope, m.ContentHash); err == nil {
		return nil, Errorf(CodeConflict, "", "duplicate of %s", dup.ID)
	} else if CodeOf(err) != CodeNotFound {
		return nil, err
	}
	switch m.Status {
	case "", StatusUnverified, StatusVerified:
		m.Status = StatusUnverified
	case StatusDisputed, StatusSuperseded, StatusForgotten:
	default:
		return nil, Errorf(CodeInvalidArgument, "status", "unknown status %q", m.Status)
	}
	if m.Source == "" {
		m.Source = SourceImported
	}
	original := m.Author.Subject
	m.Author = p.Author()
	m.Confirmations, m.Disputes, m.LastConfirmedAt = 0, 0, time.Time{}
	now := s.clock()
	if m.CreatedAt.IsZero() || m.CreatedAt.After(now) {
		m.CreatedAt = now
	}
	m.UpdatedAt = now
	m.ReviewAfter = ReviewAfter(m.CreatedAt, kind, s.periods)
	if m.ID == "" || !scopeID.MatchString(m.ID) {
		m.ID = s.newID()
	} else if _, err := st.Get(ctx, p.OrgID, m.ID); err == nil {
		m.ID = s.newID()
	} else if CodeOf(err) != CodeNotFound {
		return nil, err
	}
	if err := st.Insert(ctx, &m); err != nil {
		return nil, err
	}
	var detail map[string]any
	if original != "" && original != p.Subject {
		detail = map[string]any{"original_author": original}
	}
	return &m, s.event(ctx, st, p, m.ID, "imported", detail)
}

func (s *Service) relink(ctx context.Context, st Store, p Principal, m *Memory, rec Record, ids map[string]string) error {
	resolve := func(id string) (string, error) {
		if to, ok := ids[id]; ok {
			return to, nil
		}
		if _, err := s.readable(ctx, st, p, id, ""); err != nil {
			if CodeOf(err) == CodeNotFound {
				return "", errSkip
			}
			return "", err
		}
		return id, nil
	}
	if m.SupersededBy != "" {
		to, err := resolve(m.SupersededBy)
		if err != nil && !errors.Is(err, errSkip) {
			return err
		}
		if to != m.SupersededBy {
			m.SupersededBy = to
			if err := st.Update(ctx, m); err != nil {
				return err
			}
		}
	}
	for _, e := range rec.Edges {
		from, err := resolve(e.From)
		if err == nil {
			e.To, err = resolve(e.To)
		}
		if errors.Is(err, errSkip) {
			continue
		}
		if err != nil {
			return err
		}
		e.From, e.OrgID = from, p.OrgID
		if e.CreatedAt.IsZero() {
			e.CreatedAt = s.clock()
		}
		if err := st.AddEdge(ctx, e); err != nil {
			return err
		}
	}
	for _, v := range rec.Verifications {
		v.OrgID, v.MemoryID, v.Counted = p.OrgID, m.ID, false
		if err := st.AddVerification(ctx, v); err != nil {
			return err
		}
	}
	return nil
}

// Export calls fn with every memory the principal can read in scopes, with its
// outgoing edges and its verifications. Purged memories are left out.
func (s *Service) Export(ctx context.Context, p Principal, scopes []string, fn func(Record) error) error {
	readable, err := requestedScopes(p, scopes, "", "")
	if err != nil {
		return err
	}
	f := Filter{
		OrgID: p.OrgID, Scopes: readable, Limit: MaxListLimit,
		Statuses: []Status{StatusUnverified, StatusVerified, StatusDisputed, StatusSuperseded, StatusForgotten},
	}
	seen := map[string]bool{}
	for {
		page, next, err := s.store.List(ctx, f)
		if err != nil {
			return err
		}
		for _, m := range page {
			if m.Purged {
				continue
			}
			rec, err := s.record(ctx, p, m, seen)
			if err != nil {
				return err
			}
			if err := fn(rec); err != nil {
				return err
			}
		}
		if next == "" {
			return nil
		}
		f.Cursor = next
	}
}

func (s *Service) record(ctx context.Context, p Principal, m *Memory, seen map[string]bool) (Record, error) {
	rec := Record{Memory: *m}
	edges, err := s.store.Edges(ctx, p.OrgID, []string{m.ID})
	if err != nil {
		return rec, err
	}
	for _, e := range edges {
		if e.From != m.ID {
			continue
		}
		ok, err := s.canSee(ctx, p, e.To, seen)
		if err != nil {
			return rec, err
		}
		if ok {
			rec.Edges = append(rec.Edges, e)
		}
	}
	rec.Verifications, err = s.store.Verifications(ctx, p.OrgID, m.ID)
	return rec, err
}
