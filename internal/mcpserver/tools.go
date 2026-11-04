package mcpserver

import (
	"context"
	"errors"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/raven-clown/logbook/memory"
)

func asMemoryError(err error, target **memory.Error) bool {
	return errors.As(err, target)
}

type rememberArgs struct {
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

func rememberTool() mcp.Tool {
	return mcp.NewTool("remember",
		mcp.WithDescription("Store something worth knowing later, for you and for other agents and people. Use corrects to fix a memory that turned out to be wrong."),
		mcp.WithInputSchema[rememberArgs](),
		mcp.WithOutputSchema[memory.RememberResult]())
}

func (h *handlers) remember(ctx context.Context, a rememberArgs) (any, error) {
	until, err := parseTime("valid_until", a.ValidUntil)
	if err != nil {
		return nil, err
	}
	return h.svc.Remember(ctx, h.principal(ctx), memory.RememberRequest{
		Content: a.Content, Kind: memory.Kind(a.Kind), Subject: a.Subject, Scope: a.Scope, Project: a.Project,
		Team: a.Team, Tags: a.Tags, Refs: a.Refs, Corrects: a.Corrects, RelatedTo: a.RelatedTo,
		ValidUntil: until, Evidence: a.Evidence, Extract: a.Extract,
	})
}

type recallArgs struct {
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

func recallTool() mcp.Tool {
	return mcp.NewTool("recall",
		mcp.WithDescription("Search shared memories before answering a question that may have been answered before. Results say whether each memory was verified, disputed, corrected or is stale."),
		mcp.WithInputSchema[recallArgs](),
		mcp.WithOutputSchema[memory.RecallResult]())
}

func (h *handlers) recall(ctx context.Context, a recallArgs) (any, error) {
	asOf, err := parseTime("as_of", a.AsOf)
	if err != nil {
		return nil, err
	}
	kinds := make([]memory.Kind, len(a.Kinds))
	for i, k := range a.Kinds {
		kinds[i] = memory.Kind(k)
	}
	return h.svc.Recall(ctx, h.principal(ctx), memory.RecallRequest{
		Query: a.Query, Scopes: a.Scopes, Project: a.Project, Team: a.Team, Kinds: kinds, Tags: a.Tags,
		Limit: a.Limit, MinStatus: memory.Status(a.MinStatus), ExcludeStale: a.IncludeStale != nil && !*a.IncludeStale,
		Expand: a.Expand, AsOf: asOf,
	})
}

type verifyArgs struct {
	ID      string `json:"id" jsonschema:"ID of the memory."`
	Verdict string `json:"verdict" jsonschema:"confirm if you checked it and it holds, dispute if it does not."`
	Note    string `json:"note,omitempty" jsonschema:"How you checked."`
}

func verifyTool() mcp.Tool {
	return mcp.NewTool("verify",
		mcp.WithDescription("Confirm or dispute a memory after checking it. Confirming your own memory from the same channel is recorded but does not count."),
		mcp.WithInputSchema[verifyArgs](),
		mcp.WithOutputSchema[memory.VerifyResult]())
}

func (h *handlers) verify(ctx context.Context, a verifyArgs) (any, error) {
	return h.svc.Verify(ctx, h.principal(ctx), memory.VerifyRequest{ID: a.ID, Verdict: memory.Verdict(a.Verdict), Note: a.Note})
}

type forgetArgs struct {
	ID     string `json:"id" jsonschema:"ID of the memory."`
	Reason string `json:"reason,omitempty" jsonschema:"Why it should be forgotten."`
	Purge  bool   `json:"purge,omitempty" jsonschema:"Erase the content now instead of keeping it hidden for a while."`
}

func forgetTool() mcp.Tool {
	return mcp.NewTool("forget",
		mcp.WithDescription("Hide a memory that should not be used any more. Prefer remember with corrects when the memory is wrong rather than unwanted."),
		mcp.WithInputSchema[forgetArgs](),
		mcp.WithOutputSchema[memory.Memory]())
}

func (h *handlers) forget(ctx context.Context, a forgetArgs) (any, error) {
	return h.svc.Forget(ctx, h.principal(ctx), memory.ForgetRequest{ID: a.ID, Reason: a.Reason, Purge: a.Purge})
}

type listArgs struct {
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

func listTool() mcp.Tool {
	return mcp.NewTool("list_memories",
		mcp.WithDescription("Browse memories by scope, status, kind or tag. order review gives the queue of memories that need someone to check them."),
		mcp.WithInputSchema[listArgs](),
		mcp.WithOutputSchema[memory.ListResult]())
}

func (h *handlers) list(ctx context.Context, a listArgs) (any, error) {
	statuses := make([]memory.Status, len(a.Status))
	for i, s := range a.Status {
		statuses[i] = memory.Status(s)
	}
	return h.svc.List(ctx, h.principal(ctx), memory.ListRequest{
		Scopes: a.Scopes, Project: a.Project, Team: a.Team, Statuses: statuses, Kind: memory.Kind(a.Kind),
		Subject: a.Subject, Tag: a.Tag, Author: a.Author, Stale: a.Stale, Order: memory.Order(a.Order),
		Limit: a.Limit, Cursor: a.Cursor,
	})
}
