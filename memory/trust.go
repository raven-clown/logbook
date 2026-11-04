package memory

import "time"

// DefaultReviewPeriods is how long each kind of memory stays fresh. Zero means never stale.
var DefaultReviewPeriods = map[Kind]time.Duration{
	KindObservation: 720 * time.Hour,
	KindFact:        4320 * time.Hour,
	KindProcedure:   4320 * time.Hour,
	KindDecision:    8760 * time.Hour,
	KindPreference:  0,
}

// ReviewAfter returns when a memory of kind k written at t becomes stale, or zero for never.
func ReviewAfter(t time.Time, k Kind, periods map[Kind]time.Duration) time.Time {
	d := periods[k]
	if d <= 0 {
		return time.Time{}
	}
	return t.Add(d)
}

// ApplyVerdict applies v to m and sets v.Counted. A confirm from the author's own
// subject over the author's own channel is kept but not counted, so an agent cannot
// vouch for its own write. Disputes always count.
func ApplyVerdict(m *Memory, v *Verification, now time.Time, period time.Duration) error {
	if v.Verdict != VerdictConfirm && v.Verdict != VerdictDispute {
		return Errorf(CodeInvalidArgument, "verdict", "verdict must be confirm or dispute, not %q", v.Verdict)
	}
	if !m.Status.Active() {
		return Errorf(CodeConflict, "", "memory %s is %s and cannot be verified", m.ID, m.Status)
	}
	if v.Verdict == VerdictDispute {
		v.Counted = true
		m.Disputes++
		m.Status = StatusDisputed
		return nil
	}
	v.Counted = v.Verifier.Subject != m.Author.Subject || v.Verifier.Channel != m.Author.Channel
	if !v.Counted {
		return nil
	}
	m.Confirmations++
	m.Status = StatusVerified
	m.LastConfirmedAt = now
	m.ReviewAfter = time.Time{}
	if period > 0 {
		m.ReviewAfter = now.Add(period)
	}
	return nil
}
