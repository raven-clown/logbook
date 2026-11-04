package memory

import "context"

// VerifyRequest confirms or disputes one memory.
type VerifyRequest struct {
	ID      string
	Verdict Verdict
	Note    string
}

// VerifyResult is the memory's trust state after a verify call.
type VerifyResult struct {
	ID            string `json:"id"`
	Status        Status `json:"status"`
	Confirmations int    `json:"confirmations"`
	Disputes      int    `json:"disputes"`
	Counted       bool   `json:"counted"`
}

// Verify records a confirm or dispute. A confirm from the author's own subject
// over the author's own channel is recorded but does not count.
func (s *Service) Verify(ctx context.Context, p Principal, r VerifyRequest) (*VerifyResult, error) {
	var out *VerifyResult
	err := s.store.Tx(ctx, func(st Store) error {
		m, err := s.readable(ctx, st, p, r.ID, "id")
		if err != nil {
			return err
		}
		sc, err := ParseScope(m.Scope)
		if err != nil {
			return err
		}
		if !p.CanWrite(sc) {
			return Errorf(CodePermissionDenied, "", "you cannot verify memories in %s", sc)
		}
		now := s.clock()
		v := &Verification{OrgID: p.OrgID, MemoryID: m.ID, Verifier: p.Author(), Verdict: r.Verdict, Note: r.Note, At: now}
		if err := ApplyVerdict(m, v, now, s.periods[m.Kind]); err != nil {
			return err
		}
		m.UpdatedAt = now
		if err := st.AddVerification(ctx, *v); err != nil {
			return err
		}
		if err := st.Update(ctx, m); err != nil {
			return err
		}
		action := "confirmed"
		if v.Verdict == VerdictDispute {
			action = "disputed"
		}
		detail := map[string]any{"counted": v.Counted}
		if r.Note != "" {
			detail["note"] = r.Note
		}
		if err := s.event(ctx, st, p, m.ID, action, detail); err != nil {
			return err
		}
		out = &VerifyResult{ID: m.ID, Status: m.Status, Confirmations: m.Confirmations, Disputes: m.Disputes, Counted: v.Counted}
		return nil
	})
	return out, err
}
