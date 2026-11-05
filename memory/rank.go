package memory

import (
	"cmp"
	"slices"
)

// Fuse combines ranked lists with reciprocal rank fusion: each list adds
// 1/(k+rank) to a hit's score. The result is best first, ties broken by ID.
func Fuse(k float64, lists ...[]Hit) []Hit {
	scores := map[string]float64{}
	for _, l := range lists {
		for i, h := range l {
			scores[h.ID] += 1 / (k + float64(i+1))
		}
	}
	out := make([]Hit, 0, len(scores))
	for id, s := range scores {
		out = append(out, Hit{ID: id, Score: s})
	}
	slices.SortFunc(out, func(a, b Hit) int {
		if c := cmp.Compare(b.Score, a.Score); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return out
}

// ReadableScopes lists the scopes the principal reads by default: its user scope,
// the project (project, or the principal's current one), and its teams, or only
// team when one is named and the principal belongs to it. A project the
// principal cannot read is left out.
func (p Principal) ReadableScopes(project, team string) []string {
	out := []string{"user:" + p.Subject}
	if project == "" {
		project = p.Project
	}
	if project != "" && p.CanRead(Scope{Type: ScopeProject, ID: project}) {
		out = append(out, "project:"+project)
	}
	for _, t := range p.Teams {
		if team == "" || t == team {
			out = append(out, "team:"+t)
		}
	}
	return out
}

func (s *Service) weight(st Status) float64 {
	switch st {
	case StatusVerified:
		return s.weights.Verified
	case StatusDisputed:
		return s.weights.Disputed
	}
	return s.weights.Unverified
}
