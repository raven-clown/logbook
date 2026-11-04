package memory

import (
	"context"
	"time"
)

// RememberRequest is one memory to store. Scope is "", "user", "project", "team"
// or a full scope string; empty means the current project if there is one, else the user.
type RememberRequest struct {
	Content    string
	Kind       Kind
	Subject    string
	Scope      string
	Project    string
	Team       string
	Tags       []string
	Refs       []string
	Corrects   string
	RelatedTo  []string
	ValidUntil time.Time
	Evidence   string
	Extract    bool
	Source     Source
}

// Remembered describes one memory a remember call stored or found.
type Remembered struct {
	ID         string   `json:"id"`
	Scope      string   `json:"scope"`
	Status     Status   `json:"status"`
	Duplicate  bool     `json:"duplicate,omitempty"`
	Conflicts  []string `json:"conflicts,omitempty"`
	Superseded []string `json:"superseded,omitempty"`
}

// RememberResult is the result of Remember.
type RememberResult struct {
	Memories []Remembered `json:"memories"`
	Degraded []string     `json:"degraded,omitempty"`
}

// Remember stores a memory, or returns the existing one if the same content is
// already in the scope. Corrects supersedes an older memory; a shared Subject
// with different content marks the older memory disputed.
func (s *Service) Remember(ctx context.Context, p Principal, r RememberRequest) (*RememberResult, error) {
	if r.Extract {
		return nil, Errorf(CodeUnavailable, "", "no extractor is configured, so extract is not available")
	}
	content, err := validContent(r.Content)
	if err != nil {
		return nil, err
	}
	kind, err := ParseKind(string(r.Kind))
	if err != nil {
		return nil, err
	}
	if err := validMeta(r); err != nil {
		return nil, err
	}
	sc, err := s.targetScope(ctx, p, r)
	if err != nil {
		return nil, err
	}
	source := r.Source
	if source == "" {
		source = SourceDirect
	}
	hash := ContentHash(content)
	now := s.clock()
	m := &Memory{
		OrgID: p.OrgID, Scope: sc.String(), Content: content, Kind: kind, Subject: r.Subject,
		Tags: r.Tags, Refs: r.Refs, Status: StatusUnverified, Source: source, Evidence: r.Evidence,
		Author: p.Author(), CreatedAt: now, UpdatedAt: now, ValidUntil: r.ValidUntil.UTC(),
		ReviewAfter: ReviewAfter(now, kind, s.periods), ContentHash: hash, Version: 1,
	}
	var out Remembered
	err = s.store.Tx(ctx, func(st Store) error {
		if dup, err := st.FindByHash(ctx, p.OrgID, m.Scope, hash); err == nil {
			out = Remembered{ID: dup.ID, Scope: dup.Scope, Status: dup.Status, Duplicate: true}
			return nil
		} else if CodeOf(err) != CodeNotFound {
			return err
		}
		var old *Memory
		if r.Corrects != "" {
			if old, err = s.correctable(ctx, st, p, r.Corrects); err != nil {
				return err
			}
		}
		for _, id := range r.RelatedTo {
			if _, err := s.readable(ctx, st, p, id, "related_to"); err != nil {
				return err
			}
		}
		m.ID = s.newID()
		if err := st.Insert(ctx, m); err != nil {
			return err
		}
		if err := s.event(ctx, st, p, m.ID, "created", nil); err != nil {
			return err
		}
		out = Remembered{ID: m.ID, Scope: m.Scope, Status: m.Status}
		if old != nil {
			if err := s.supersede(ctx, st, p, old, m); err != nil {
				return err
			}
			out.Superseded = []string{old.ID}
		} else if m.Subject != "" {
			if out.Conflicts, err = s.dispute(ctx, st, p, m); err != nil {
				return err
			}
		}
		for _, id := range r.RelatedTo {
			if err := st.AddEdge(ctx, Edge{OrgID: p.OrgID, From: m.ID, To: id, Type: EdgeRelated, CreatedBy: p.Subject, CreatedAt: now}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &RememberResult{Memories: []Remembered{out}}, nil
}

// targetScope picks the scope a new memory goes to. A correction stays in the
// scope of the memory it corrects, so it cannot hide a shared memory from
// people who cannot read the new one.
func (s *Service) targetScope(ctx context.Context, p Principal, r RememberRequest) (Scope, error) {
	if r.Corrects == "" {
		return writableScope(p, r.Scope, r.Project, r.Team)
	}
	old, err := s.readable(ctx, s.store, p, r.Corrects, "corrects")
	if err != nil {
		return Scope{}, err
	}
	if r.Scope == "" && r.Project == "" && r.Team == "" {
		return writableScope(p, old.Scope, "", "")
	}
	sc, err := writableScope(p, r.Scope, r.Project, r.Team)
	if err != nil {
		return Scope{}, err
	}
	if sc.String() != old.Scope {
		return Scope{}, Errorf(CodeInvalidArgument, "corrects", "a correction must be in the same scope as the memory it corrects, %s", old.Scope)
	}
	return sc, nil
}

func (s *Service) correctable(ctx context.Context, st Store, p Principal, id string) (*Memory, error) {
	old, err := s.readable(ctx, st, p, id, "corrects")
	if err != nil {
		return nil, err
	}
	sc, err := ParseScope(old.Scope)
	if err != nil {
		return nil, err
	}
	if !p.CanWrite(sc) {
		return nil, Errorf(CodePermissionDenied, "corrects", "you cannot change memories in %s", sc)
	}
	if !old.Status.Active() {
		return nil, Errorf(CodeConflict, "corrects", "memory %s is already %s", old.ID, old.Status)
	}
	return old, nil
}

func (s *Service) supersede(ctx context.Context, st Store, p Principal, old, by *Memory) error {
	now := s.clock()
	old.Status = StatusSuperseded
	old.SupersededBy = by.ID
	old.SupersededAt = now
	old.UpdatedAt = now
	if err := st.Update(ctx, old); err != nil {
		return err
	}
	if err := st.AddEdge(ctx, Edge{OrgID: p.OrgID, From: by.ID, To: old.ID, Type: EdgeSupersedes, CreatedBy: p.Subject, CreatedAt: now}); err != nil {
		return err
	}
	return s.event(ctx, st, p, old.ID, "superseded", map[string]any{"by": by.ID})
}

func (s *Service) dispute(ctx context.Context, st Store, p Principal, m *Memory) ([]string, error) {
	others, err := st.FindBySubject(ctx, p.OrgID, m.Scope, m.Subject)
	if err != nil {
		return nil, err
	}
	now := s.clock()
	var conflicts []string
	for _, o := range others {
		if o.ID == m.ID || o.ContentHash == m.ContentHash {
			continue
		}
		if o.Status != StatusDisputed {
			o.Status = StatusDisputed
			o.UpdatedAt = now
			if err := st.Update(ctx, o); err != nil {
				return nil, err
			}
		}
		if err := st.AddEdge(ctx, Edge{OrgID: p.OrgID, From: m.ID, To: o.ID, Type: EdgeContradicts, CreatedBy: p.Subject, CreatedAt: now}); err != nil {
			return nil, err
		}
		if err := s.event(ctx, st, p, o.ID, "disputed", map[string]any{"by": m.ID}); err != nil {
			return nil, err
		}
		conflicts = append(conflicts, o.ID)
	}
	return conflicts, nil
}
