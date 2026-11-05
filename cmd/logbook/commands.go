package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/server"
	"github.com/raven-clown/logbook"
	"github.com/raven-clown/logbook/importer"
	"github.com/raven-clown/logbook/importer/jsonl"
	"github.com/raven-clown/logbook/internal/mcpserver"
	"github.com/raven-clown/logbook/memory"
)

func (a *app) mcp(args []string) error {
	fs, c := a.flags("mcp")
	if _, err := parse(fs, args, 0, "mcp [--config file] [--project name]"); err != nil {
		return err
	}
	s, err := a.open(c)
	if err != nil {
		return err
	}
	defer func() { _ = s.store.Close() }()
	ctx, stop := signal.NotifyContext(a.ctx, os.Interrupt)
	defer stop()
	srv := mcpserver.New(s.svc, func(ctx context.Context) memory.Principal {
		p := s.principal(memory.ChannelMCP)
		p.Client = mcpserver.SessionClient(ctx)
		return p
	}, logbook.Version)
	stdio := server.NewStdioServer(srv)
	stdio.SetErrorLogger(log.New(a.stderr, "", 0))
	return stdio.Listen(ctx, a.stdin, a.stdout)
}

func (a *app) remember(args []string) error {
	fs, c := a.flags("remember")
	kind := fs.String("kind", "", "fact, preference, decision, procedure or observation")
	subject := fs.String("subject", "", "topic key, such as ci.go-version")
	scope := fs.String("scope", "", "user, project, team or a full scope")
	team := fs.String("team", "", "team name for a team scope")
	corrects := fs.String("corrects", "", "id of the memory this one corrects")
	evidence := fs.String("evidence", "", "how you know")
	var tags, refs, related stringList
	fs.Var(&tags, "tag", "label, repeatable")
	fs.Var(&refs, "ref", "file path or URL, repeatable")
	fs.Var(&related, "related", "id of a related memory, repeatable")
	pos, err := parse(fs, args, 1, "remember <content> [flags]")
	if err != nil {
		return err
	}
	s, err := a.open(c)
	if err != nil {
		return err
	}
	defer func() { _ = s.store.Close() }()
	res, err := s.svc.Remember(a.ctx, s.principal(memory.ChannelCLI), memory.RememberRequest{
		Content: pos[0], Kind: memory.Kind(*kind), Subject: *subject, Scope: *scope, Team: *team,
		Corrects: *corrects, Evidence: *evidence, Tags: tags, Refs: refs, RelatedTo: related,
	})
	if err != nil {
		return err
	}
	if c.json {
		return a.printJSON(res)
	}
	for _, m := range res.Memories {
		line := m.ID
		if m.Duplicate {
			line += "  already stored"
		}
		a.println(line)
		for _, id := range m.Conflicts {
			a.println("  conflicts with " + id)
		}
	}
	return nil
}

func (a *app) recall(args []string) error {
	fs, c := a.flags("recall")
	limit := fs.Int("limit", 0, "how many results, default 8")
	verified := fs.Bool("verified-only", false, "only verified memories")
	noStale := fs.Bool("exclude-stale", false, "leave out stale memories")
	var scopes stringList
	fs.Var(&scopes, "scope", "user, project, team or a full scope, repeatable")
	pos, err := parse(fs, args, -1, "recall <query> [flags]")
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return usageError{msg: "usage: logbook recall <query> [flags]"}
	}
	s, err := a.open(c)
	if err != nil {
		return err
	}
	defer func() { _ = s.store.Close() }()
	req := memory.RecallRequest{Query: strings.Join(pos, " "), Scopes: scopes, Limit: *limit, ExcludeStale: *noStale}
	if *verified {
		req.MinStatus = memory.StatusVerified
	}
	res, err := s.svc.Recall(a.ctx, s.principal(memory.ChannelCLI), req)
	if err != nil {
		return err
	}
	if c.json {
		return a.printJSON(res)
	}
	for _, r := range res.Results {
		a.printMemory(&r.Memory, r.Stale)
		if r.CorrectedFrom != nil {
			a.println("    corrected from: " + oneLine(r.CorrectedFrom.Content))
		}
	}
	return nil
}

func (a *app) list(args []string) error {
	fs, c := a.flags("list")
	stale := fs.Bool("stale", false, "only memories past their review date")
	order := fs.String("order", "", "newest or review")
	limit := fs.Int("limit", 0, "page size, default 50")
	cursor := fs.String("cursor", "", "cursor from the previous page")
	kind := fs.String("kind", "", "only this kind")
	tag := fs.String("tag", "", "only this tag")
	var statuses, scopes stringList
	fs.Var(&statuses, "status", "only this status, repeatable")
	fs.Var(&scopes, "scope", "user, project, team or a full scope, repeatable")
	if _, err := parse(fs, args, 0, "list [flags]"); err != nil {
		return err
	}
	s, err := a.open(c)
	if err != nil {
		return err
	}
	defer func() { _ = s.store.Close() }()
	st := make([]memory.Status, len(statuses))
	for i, v := range statuses {
		st[i] = memory.Status(v)
	}
	res, err := s.svc.List(a.ctx, s.principal(memory.ChannelCLI), memory.ListRequest{
		Scopes: scopes, Statuses: st, Kind: memory.Kind(*kind), Tag: *tag, Stale: *stale,
		Order: memory.Order(*order), Limit: *limit, Cursor: *cursor,
	})
	if err != nil {
		return err
	}
	if c.json {
		return a.printJSON(res)
	}
	now := s.now()
	for _, m := range res.Items {
		a.printMemory(m, !m.ReviewAfter.IsZero() && !now.Before(m.ReviewAfter))
	}
	if res.NextCursor != "" {
		a.println("next: --cursor " + res.NextCursor)
	}
	return nil
}

func (a *app) verify(args []string) error {
	fs, c := a.flags("verify")
	note := fs.String("note", "", "how you checked")
	pos, err := parse(fs, args, 2, "verify <id> confirm|dispute [--note text]")
	if err != nil {
		return err
	}
	s, err := a.open(c)
	if err != nil {
		return err
	}
	defer func() { _ = s.store.Close() }()
	res, err := s.svc.Verify(a.ctx, s.principal(memory.ChannelCLI), memory.VerifyRequest{ID: pos[0], Verdict: memory.Verdict(pos[1]), Note: *note})
	if err != nil {
		return err
	}
	if c.json {
		return a.printJSON(res)
	}
	counted := "counted"
	if !res.Counted {
		counted = "not counted: you wrote it through this same channel"
	}
	a.println(fmt.Sprintf("%s  %s  %s", res.ID, res.Status, counted))
	return nil
}

func (a *app) forget(args []string) error {
	fs, c := a.flags("forget")
	reason := fs.String("reason", "", "why")
	purge := fs.Bool("purge", false, "erase the content now")
	pos, err := parse(fs, args, 1, "forget <id> [--reason text] [--purge]")
	if err != nil {
		return err
	}
	s, err := a.open(c)
	if err != nil {
		return err
	}
	defer func() { _ = s.store.Close() }()
	m, err := s.svc.Forget(a.ctx, s.principal(memory.ChannelCLI), memory.ForgetRequest{ID: pos[0], Reason: *reason, Purge: *purge})
	if err != nil {
		return err
	}
	if c.json {
		return a.printJSON(m)
	}
	state := "forgotten"
	if m.Purged {
		state = "purged"
	}
	a.println(m.ID + "  " + state)
	return nil
}

func (a *app) importCmd(args []string) error {
	fs, c := a.flags("import")
	scope := fs.String("scope", "", "scope for records that have none")
	pos, err := parse(fs, args, 2, "import <format> <path> [--scope scope]")
	if err != nil {
		return err
	}
	imp, err := importer.Get(pos[0])
	if err != nil {
		return err
	}
	f, err := os.Open(filepath.Clean(pos[1])) // #nosec G703 -- the path is the operator's own argument
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	s, err := a.open(c)
	if err != nil {
		return err
	}
	defer func() { _ = s.store.Close() }()
	report, err := importer.Run(a.ctx, s.svc, s.principal(memory.ChannelCLI), *scope, imp, f)
	if err != nil {
		return err
	}
	if c.json {
		return a.printJSON(report)
	}
	a.println(fmt.Sprintf("imported %d, skipped %d", report.Imported, len(report.Skipped)))
	for _, sk := range report.Skipped {
		a.println(fmt.Sprintf("  line %d: %s", sk.Line, sk.Err))
	}
	return nil
}

func (a *app) export(args []string) error {
	fs, c := a.flags("export")
	var scopes stringList
	fs.Var(&scopes, "scope", "user, project, team or a full scope, repeatable")
	if _, err := parse(fs, args, 0, "export [--scope scope]"); err != nil {
		return err
	}
	s, err := a.open(c)
	if err != nil {
		return err
	}
	defer func() { _ = s.store.Close() }()
	return s.svc.Export(a.ctx, s.principal(memory.ChannelCLI), scopes, func(r memory.Record) error {
		return jsonl.Write(a.stdout, r)
	})
}

func (a *app) migrate(args []string) error {
	fs, c := a.flags("migrate")
	if _, err := parse(fs, args, 0, "migrate [--config file]"); err != nil {
		return err
	}
	s, err := a.open(c)
	if err != nil {
		return err
	}
	a.println("schema up to date")
	return s.store.Close()
}

func (a *app) version(args []string) error {
	fs, _ := a.flags("version")
	if _, err := parse(fs, args, 0, "version"); err != nil {
		return err
	}
	a.println(logbook.Version)
	return nil
}
