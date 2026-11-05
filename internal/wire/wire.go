// Package wire holds the JSON request bodies shared by the MCP tools and the
// REST API, so the two can't drift apart.
package wire

import (
	"time"

	"github.com/raven-clown/logbook/memory"
)

// Remember is the remember request.
type Remember struct {
	Content    string   `json:"content" jsonschema:"The memory itself, one fact or decision in plain words, up to 8 KiB."`
	Kind       string   `json:"kind,omitempty" jsonschema:"fact (default), preference, decision, procedure or observation. Sets how soon it needs review."`
	Subject    string   `json:"subject,omitempty" jsonschema:"Optional topic key such as ci.go-version. Two different memories with the same subject are flagged as a conflict."`
	Scope      string   `json:"scope,omitempty" jsonschema:"user, project or team. Default: the current project, or your user scope when there is none."`
	Project    string   `json:"project,omitempty" jsonschema:"Project name, when scope is project and it is not the current one."`
	Team       string   `json:"team,omitempty" jsonschema:"Team name, when scope is team."`
	Tags       []string `json:"tags,omitempty" jsonschema:"Free-form labels."`
	Refs       []string `json:"refs,omitempty" jsonschema:"File paths or URLs the memory is about."`
	Corrects   string   `json:"corrects,omitempty" jsonschema:"ID of a memory this one corrects. The old one is kept but marked superseded."`
	RelatedTo  []string `json:"related_to,omitempty" jsonschema:"IDs of memories this one is related to."`
	ValidUntil string   `json:"valid_until,omitempty" jsonschema:"RFC 3339 time after which the memory stops being true."`
	Evidence   string   `json:"evidence,omitempty" jsonschema:"How you know: a command output, a link, a quote."`
	Extract    bool     `json:"extract,omitempty" jsonschema:"Treat content as raw text and pull facts out of it. Needs an extractor to be configured."`
}

// ToRequest converts the body.
func (a Remember) ToRequest() (memory.RememberRequest, error) {
	until, err := parseTime("valid_until", a.ValidUntil)
	if err != nil {
		return memory.RememberRequest{}, err
	}
	return memory.RememberRequest{
		Content: a.Content, Kind: memory.Kind(a.Kind), Subject: a.Subject, Scope: a.Scope, Project: a.Project,
		Team: a.Team, Tags: a.Tags, Refs: a.Refs, Corrects: a.Corrects, RelatedTo: a.RelatedTo,
		ValidUntil: until, Evidence: a.Evidence, Extract: a.Extract,
	}, nil
}

// Recall is the recall request.
type Recall struct {
	Query        string   `json:"query" jsonschema:"What you want to know, in plain words."`
	Scopes       []string `json:"scopes,omitempty" jsonschema:"Limit to user, project, team or full scopes such as team:platform. Default: every scope you can read."`
	Project      string   `json:"project,omitempty" jsonschema:"Search this project instead of the current one."`
	Team         string   `json:"team,omitempty" jsonschema:"Search only this team among your teams."`
	Kinds        []string `json:"kinds,omitempty" jsonschema:"Only these kinds."`
	Tags         []string `json:"tags,omitempty" jsonschema:"Only memories with any of these tags."`
	Limit        int      `json:"limit,omitempty" jsonschema:"How many results, default 8, at most 50."`
	MinStatus    string   `json:"min_status,omitempty" jsonschema:"verified to return only memories someone confirmed."`
	IncludeStale *bool    `json:"include_stale,omitempty" jsonschema:"Include memories past their review date, flagged as stale. Default true."`
	Expand       int      `json:"expand,omitempty" jsonschema:"1 to include each result's links to other memories."`
	AsOf         string   `json:"as_of,omitempty" jsonschema:"RFC 3339 time: return what was believed then."`
}

// ToRequest converts the body.
func (a Recall) ToRequest() (memory.RecallRequest, error) {
	asOf, err := parseTime("as_of", a.AsOf)
	if err != nil {
		return memory.RecallRequest{}, err
	}
	var kinds []memory.Kind
	for _, k := range a.Kinds {
		kinds = append(kinds, memory.Kind(k))
	}
	return memory.RecallRequest{
		Query: a.Query, Scopes: a.Scopes, Project: a.Project, Team: a.Team, Kinds: kinds, Tags: a.Tags,
		Limit: a.Limit, MinStatus: memory.Status(a.MinStatus), ExcludeStale: a.IncludeStale != nil && !*a.IncludeStale,
		Expand: a.Expand, AsOf: asOf,
	}, nil
}

// Verify is the verify request.
type Verify struct {
	ID      string `json:"id" jsonschema:"ID of the memory."`
	Verdict string `json:"verdict" jsonschema:"confirm if you checked it and it holds, dispute if it does not."`
	Note    string `json:"note,omitempty" jsonschema:"How you checked."`
}

// ToRequest converts the body.
func (a Verify) ToRequest() memory.VerifyRequest {
	return memory.VerifyRequest{ID: a.ID, Verdict: memory.Verdict(a.Verdict), Note: a.Note}
}

// Forget is the forget request.
type Forget struct {
	ID     string `json:"id" jsonschema:"ID of the memory."`
	Reason string `json:"reason,omitempty" jsonschema:"Why it should be forgotten."`
	Purge  bool   `json:"purge,omitempty" jsonschema:"Erase the content now instead of keeping it hidden for a while."`
}

// ToRequest converts the body.
func (a Forget) ToRequest() memory.ForgetRequest {
	return memory.ForgetRequest{ID: a.ID, Reason: a.Reason, Purge: a.Purge}
}

// List is the list_memories request.
type List struct {
	Scopes  []string `json:"scopes,omitempty" jsonschema:"Limit to user, project, team or full scopes."`
	Project string   `json:"project,omitempty" jsonschema:"List this project instead of the current one."`
	Team    string   `json:"team,omitempty" jsonschema:"List only this team among your teams."`
	Status  []string `json:"status,omitempty" jsonschema:"Only these statuses: unverified, verified, disputed, superseded, forgotten."`
	Kind    string   `json:"kind,omitempty" jsonschema:"Only this kind."`
	Subject string   `json:"subject,omitempty" jsonschema:"Only this subject."`
	Tag     string   `json:"tag,omitempty" jsonschema:"Only this tag."`
	Author  string   `json:"author,omitempty" jsonschema:"Only memories written by this subject."`
	Stale   bool     `json:"stale,omitempty" jsonschema:"Only memories past their review date."`
	Order   string   `json:"order,omitempty" jsonschema:"newest (default) or review: disputed first, then stale, then unverified."`
	Limit   int      `json:"limit,omitempty" jsonschema:"Page size, default 50, at most 500."`
	Cursor  string   `json:"cursor,omitempty" jsonschema:"next_cursor from the previous page."`
}

// ToRequest converts the body.
func (a List) ToRequest() memory.ListRequest {
	var statuses []memory.Status
	for _, s := range a.Status {
		statuses = append(statuses, memory.Status(s))
	}
	return memory.ListRequest{
		Scopes: a.Scopes, Project: a.Project, Team: a.Team, Statuses: statuses, Kind: memory.Kind(a.Kind),
		Subject: a.Subject, Tag: a.Tag, Author: a.Author, Stale: a.Stale, Order: memory.Order(a.Order),
		Limit: a.Limit, Cursor: a.Cursor,
	}
}

func parseTime(field, s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, memory.Errorf(memory.CodeInvalidArgument, field, "%s must be an RFC 3339 time such as 2026-11-04T09:00:00Z", field)
	}
	return t.UTC(), nil
}
