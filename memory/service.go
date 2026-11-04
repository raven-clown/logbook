package memory

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

// Weights scale recall scores by trust. Stale multiplies on top of the status weight.
type Weights struct {
	Verified, Unverified, Disputed, Stale float64
}

// DefaultWeights are the trust weights from the design.
var DefaultWeights = Weights{Verified: 1.0, Unverified: 0.7, Disputed: 0.3, Stale: 0.5}

// Options configure a Service. Zero values take the defaults.
type Options struct {
	Now           func() time.Time
	NewID         func() string
	Rules         []Rule
	ReviewPeriods map[Kind]time.Duration
	Weights       Weights
}

// Service implements remember, recall, verify, forget and list on top of a Store.
type Service struct {
	store   Store
	now     func() time.Time
	newID   func() string
	rules   []Rule
	periods map[Kind]time.Duration
	weights Weights
}

// NewService returns a Service on s.
func NewService(s Store, o Options) *Service {
	svc := &Service{store: s, now: o.Now, newID: o.NewID, rules: o.Rules, periods: o.ReviewPeriods, weights: o.Weights}
	if svc.now == nil {
		svc.now = time.Now
	}
	if svc.newID == nil {
		svc.newID = func() string { return "mem_" + ulid.Make().String() }
	}
	if svc.periods == nil {
		svc.periods = DefaultReviewPeriods
	}
	if svc.weights == (Weights{}) {
		svc.weights = DefaultWeights
	}
	return svc
}

func (s *Service) clock() time.Time {
	return s.now().UTC()
}

// resolveScope turns a scope request into a scope the principal may write.
// scope is "", "user", "project", "team" or a full scope string.
func resolveScope(p Principal, scope, project, team string) (Scope, error) {
	if strings.Contains(scope, ":") {
		sc, err := ParseScope(scope)
		if err != nil {
			return Scope{}, err
		}
		return sc, nil
	}
	if project == "" {
		project = p.Project
	}
	if scope == "" {
		scope = string(ScopeUser)
		if project != "" {
			scope = string(ScopeProject)
		}
	}
	var raw string
	switch ScopeType(scope) {
	case ScopeUser:
		raw = "user:" + p.Subject
	case ScopeProject:
		if project == "" {
			return Scope{}, Errorf(CodeInvalidArgument, "project", "no project given and no default project set")
		}
		raw = "project:" + project
	case ScopeTeam:
		if team == "" {
			return Scope{}, Errorf(CodeInvalidArgument, "team", "a team scope needs a team name")
		}
		raw = "team:" + team
	default:
		return Scope{}, Errorf(CodeInvalidArgument, "scope", "scope must be user, project, team or a full scope such as team:platform, not %q", scope)
	}
	return ParseScope(raw)
}

func writableScope(p Principal, scope, project, team string) (Scope, error) {
	sc, err := resolveScope(p, scope, project, team)
	if err != nil {
		return Scope{}, err
	}
	if !p.CanWrite(sc) {
		return Scope{}, Errorf(CodePermissionDenied, "", "you cannot write to %s", sc)
	}
	return sc, nil
}

// readable returns the memory with id if the principal may read it, and
// reports one it may not read as not found, so ids in other scopes do not leak.
func (s *Service) readable(ctx context.Context, st Store, p Principal, id, field string) (*Memory, error) {
	m, err := st.Get(ctx, p.OrgID, id)
	if err == nil {
		sc, perr := ParseScope(m.Scope)
		if perr == nil && p.CanRead(sc) {
			return m, nil
		}
		err = ErrNotFound
	}
	if CodeOf(err) == CodeNotFound {
		return nil, Errorf(CodeNotFound, field, "memory %s not found", id)
	}
	return nil, err
}

func (s *Service) event(ctx context.Context, st Store, p Principal, memoryID, action string, detail map[string]any) error {
	e := Event{OrgID: p.OrgID, MemoryID: memoryID, Action: action, Actor: p.Author(), At: s.clock()}
	if len(detail) > 0 {
		b, err := json.Marshal(detail)
		if err != nil {
			return err
		}
		e.Detail = b
	}
	return st.AppendEvent(ctx, e)
}
