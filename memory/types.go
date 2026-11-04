package memory

import (
	"encoding/json"
	"time"
)

// DefaultOrg is the organisation every row belongs to until organisations ship.
const DefaultOrg = "default"

// Kind says what sort of thing a memory records. It sets the review period.
type Kind string

// The memory kinds.
const (
	KindFact        Kind = "fact"
	KindPreference  Kind = "preference"
	KindDecision    Kind = "decision"
	KindProcedure   Kind = "procedure"
	KindObservation Kind = "observation"
)

// ParseKind parses a kind name. An empty name means fact.
func ParseKind(s string) (Kind, error) {
	switch k := Kind(s); k {
	case "":
		return KindFact, nil
	case KindFact, KindPreference, KindDecision, KindProcedure, KindObservation:
		return k, nil
	}
	return "", Errorf(CodeInvalidArgument, "kind", "unknown kind %q", s)
}

// Status is a memory's trust state.
type Status string

// The trust states.
const (
	StatusUnverified Status = "unverified"
	StatusVerified   Status = "verified"
	StatusDisputed   Status = "disputed"
	StatusSuperseded Status = "superseded"
	StatusForgotten  Status = "forgotten"
)

// Active reports whether a memory in this state is still current.
func (s Status) Active() bool {
	return s == StatusUnverified || s == StatusVerified || s == StatusDisputed
}

// Source says how a memory entered logbook.
type Source string

// The memory sources.
const (
	SourceDirect    Source = "direct"
	SourceExtracted Source = "extracted"
	SourceImported  Source = "imported"
)

// Channel is the interface a call came in through.
type Channel string

// The channels.
const (
	ChannelMCP   Channel = "mcp"
	ChannelREST  Channel = "rest"
	ChannelCLI   Channel = "cli"
	ChannelAdmin Channel = "admin"
)

// EdgeType names the relation an edge records.
type EdgeType string

// The edge types.
const (
	EdgeSupersedes  EdgeType = "supersedes"
	EdgeContradicts EdgeType = "contradicts"
	EdgeRelated     EdgeType = "related"
	EdgeDerivedFrom EdgeType = "derived_from"
)

// Verdict is the outcome of a verification.
type Verdict string

// The verdicts.
const (
	VerdictConfirm Verdict = "confirm"
	VerdictDispute Verdict = "dispute"
)

// Author identifies who wrote or verified something, and through which channel.
type Author struct {
	Subject string  `json:"subject"`
	Channel Channel `json:"channel"`
	Client  string  `json:"client,omitempty"`
}

// Memory is one stored memory with its trust metadata.
type Memory struct {
	ID              string    `json:"id"`
	OrgID           string    `json:"org_id"`
	Scope           string    `json:"scope"`
	Content         string    `json:"content"`
	Kind            Kind      `json:"kind"`
	Subject         string    `json:"subject,omitempty"`
	Tags            []string  `json:"tags,omitempty"`
	Refs            []string  `json:"refs,omitempty"`
	Status          Status    `json:"status"`
	Source          Source    `json:"source"`
	Evidence        string    `json:"evidence,omitempty"`
	Author          Author    `json:"author"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	ValidUntil      time.Time `json:"valid_until,omitzero"`
	ReviewAfter     time.Time `json:"review_after,omitzero"`
	LastConfirmedAt time.Time `json:"last_confirmed_at,omitzero"`
	Confirmations   int       `json:"confirmations"`
	Disputes        int       `json:"disputes"`
	SupersededBy    string    `json:"superseded_by,omitempty"`
	SupersededAt    time.Time `json:"superseded_at,omitzero"`
	ForgottenAt     time.Time `json:"forgotten_at,omitzero"`
	Purged          bool      `json:"purged,omitempty"`
	ContentHash     string    `json:"content_hash"`
	Version         int       `json:"version"`
}

// Edge is a typed, directed link between two memories.
type Edge struct {
	OrgID     string    `json:"org_id"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Type      EdgeType  `json:"type"`
	CreatedBy string    `json:"created_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Verification records one confirm or dispute.
type Verification struct {
	OrgID    string    `json:"org_id"`
	MemoryID string    `json:"memory_id"`
	Verifier Author    `json:"verifier"`
	Verdict  Verdict   `json:"verdict"`
	Note     string    `json:"note,omitempty"`
	Counted  bool      `json:"counted"`
	At       time.Time `json:"at"`
}

// Event is one entry in a memory's append-only history.
type Event struct {
	OrgID    string          `json:"org_id"`
	MemoryID string          `json:"memory_id"`
	Action   string          `json:"action"`
	Actor    Author          `json:"actor"`
	At       time.Time       `json:"at"`
	Detail   json.RawMessage `json:"detail,omitempty"`
}

// Hit is a search result before ranking rules are applied.
type Hit struct {
	ID    string
	Score float64
}
