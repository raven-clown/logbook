package memory

import (
	"context"
	"encoding/json"
)

// Detail is one memory with everything recorded about it.
type Detail struct {
	Memory        *Memory        `json:"memory"`
	Edges         []Edge         `json:"edges"`
	Verifications []Verification `json:"verifications"`
	History       []Event        `json:"history"`
}

// Detail returns a memory with its edges, verifications and history. Edges
// whose other end the caller can't read are left out, and a forgotten memory
// is not found.
func (s *Service) Detail(ctx context.Context, p Principal, id string) (*Detail, error) {
	m, err := s.active(ctx, s.store, p, id, "id")
	if err != nil {
		return nil, err
	}
	d := &Detail{Memory: m, Edges: []Edge{}, Verifications: []Verification{}, History: []Event{}}
	edges, err := s.store.Edges(ctx, p.OrgID, []string{id})
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{id: true}
	for _, e := range edges {
		other := e.To
		if other == id {
			other = e.From
		}
		ok, err := s.canSee(ctx, p, other, seen)
		if err != nil {
			return nil, err
		}
		if ok {
			d.Edges = append(d.Edges, e)
		}
	}
	vs, err := s.store.Verifications(ctx, p.OrgID, id)
	if err != nil {
		return nil, err
	}
	d.Verifications = append(d.Verifications, vs...)
	hist, err := s.store.Events(ctx, p.OrgID, id)
	if err != nil {
		return nil, err
	}
	for _, e := range hist {
		if e.Detail, err = s.redact(ctx, p, e.Detail, seen); err != nil {
			return nil, err
		}
		d.History = append(d.History, e)
	}
	return d, nil
}

// eventRefs are the event detail keys that hold another memory's id.
var eventRefs = []string{"to", "by"}

// redact drops ids of memories the caller can't read from an event's detail.
func (s *Service) redact(ctx context.Context, p Principal, detail json.RawMessage, seen map[string]bool) (json.RawMessage, error) {
	if len(detail) == 0 {
		return detail, nil
	}
	var m map[string]any
	if err := json.Unmarshal(detail, &m); err != nil {
		return nil, err
	}
	changed := false
	for _, k := range eventRefs {
		id, ok := m[k].(string)
		if !ok {
			continue
		}
		visible, err := s.canSee(ctx, p, id, seen)
		if err != nil {
			return nil, err
		}
		if !visible {
			delete(m, k)
			changed = true
		}
	}
	if !changed {
		return detail, nil
	}
	if len(m) == 0 {
		return nil, nil
	}
	return json.Marshal(m)
}

// Link adds a related edge from one memory to another. The caller needs write
// access to from and read access to to. Linking twice changes nothing.
func (s *Service) Link(ctx context.Context, p Principal, from, to string) (*Edge, error) {
	if to == "" {
		return nil, Errorf(CodeInvalidArgument, "to", "to is required")
	}
	if to == from {
		return nil, Errorf(CodeInvalidArgument, "to", "a memory can't be linked to itself")
	}
	var edge *Edge
	err := s.store.Tx(ctx, func(st Store) error {
		src, err := s.active(ctx, st, p, from, "id")
		if err != nil {
			return err
		}
		sc, err := ParseScope(src.Scope)
		if err != nil {
			return err
		}
		if !p.CanWrite(sc) {
			return Errorf(CodePermissionDenied, "id", "you cannot write to %s", sc)
		}
		if _, err := s.active(ctx, st, p, to, "to"); err != nil {
			return err
		}
		e := Edge{OrgID: p.OrgID, From: from, To: to, Type: EdgeRelated, CreatedBy: p.Subject, CreatedAt: s.clock()}
		if err := st.AddEdge(ctx, e); err != nil {
			return err
		}
		edge = &e
		return s.event(ctx, st, p, from, "linked", map[string]any{"to": to})
	})
	return edge, err
}

// active is readable, with forgotten memories reported as not found.
func (s *Service) active(ctx context.Context, st Store, p Principal, id, field string) (*Memory, error) {
	m, err := s.readable(ctx, st, p, id, field)
	if err != nil {
		return nil, err
	}
	if m.Status == StatusForgotten {
		return nil, Errorf(CodeNotFound, field, "memory %s not found", id)
	}
	return m, nil
}
