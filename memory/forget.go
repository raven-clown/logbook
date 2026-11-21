package memory

import (
	"context"
	"time"
)

// ForgetRequest hides a memory, or with Purge also erases its content.
type ForgetRequest struct {
	ID     string
	Reason string
	Purge  bool
}

// Forget hides a memory from recall and list. The author may always forget
// their own memory; anyone else needs write access to its scope.
func (s *Service) Forget(ctx context.Context, p Principal, r ForgetRequest) (*Memory, error) {
	var out *Memory
	err := s.store.Tx(ctx, func(st Store) error {
		m, err := st.Get(ctx, p.OrgID, r.ID)
		if CodeOf(err) == CodeNotFound {
			return Errorf(CodeNotFound, "id", "memory %s not found", r.ID)
		}
		if err != nil {
			return err
		}
		sc, err := ParseScope(m.Scope)
		if err != nil {
			return err
		}
		if m.Author.Subject != p.Subject && !p.CanWrite(sc) {
			return Errorf(CodeNotFound, "id", "memory %s not found", r.ID)
		}
		if m.Status == StatusForgotten && (!r.Purge || m.Purged) {
			out = m
			return nil
		}
		now := s.clock()
		if m.Status != StatusForgotten {
			m.Status = StatusForgotten
			m.ForgottenAt = now
		}
		action := "forgotten"
		if r.Purge {
			action = "purged"
			wipe(m)
		}
		m.UpdatedAt = now
		if err := st.Update(ctx, m); err != nil {
			return err
		}
		var detail map[string]any
		if r.Reason != "" {
			detail = map[string]any{"reason": r.Reason}
		}
		if err := s.event(ctx, st, p, m.ID, action, detail); err != nil {
			return err
		}
		out = m
		return nil
	})
	return out, err
}

func wipe(m *Memory) {
	m.Content, m.Evidence, m.Tags, m.Refs, m.Purged = "", "", nil, nil, true
}

const purgePage = 100

// jobAuthor is the actor of changes made by background jobs.
var jobAuthor = Author{Subject: "logbook", Channel: ChannelJob}

// PurgeForgotten erases the content of memories forgotten more than retention
// ago and returns how many it purged. Zero retention purges nothing.
func (s *Service) PurgeForgotten(ctx context.Context, orgID string, retention time.Duration) (int, error) {
	if retention <= 0 {
		return 0, nil
	}
	f := Filter{OrgID: orgID, AllScopes: true, ForgottenBefore: s.clock().Add(-retention), Limit: purgePage}
	total := 0
	for {
		page, _, err := s.store.List(ctx, f)
		if err != nil {
			return total, err
		}
		for _, m := range page {
			err := s.store.Tx(ctx, func(st Store) error {
				wipe(m)
				m.UpdatedAt = s.clock()
				if err := st.Update(ctx, m); err != nil {
					return err
				}
				return st.AppendEvent(ctx, Event{OrgID: orgID, MemoryID: m.ID, Action: "purged", Actor: jobAuthor, At: s.clock()})
			})
			if err != nil {
				return total, err
			}
			total++
		}
		if len(page) < purgePage {
			return total, nil
		}
	}
}

// ForgetResult is what forget returns to callers: the id and the new status,
// never the content, since the caller may no longer be allowed to read it.
type ForgetResult struct {
	ID     string `json:"id"`
	Status Status `json:"status"`
	Purged bool   `json:"purged,omitempty"`
}

// NewForgetResult returns the ForgetResult for a forgotten memory.
func NewForgetResult(m *Memory) *ForgetResult {
	return &ForgetResult{ID: m.ID, Status: m.Status, Purged: m.Purged}
}
