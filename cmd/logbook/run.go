package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/raven-clown/logbook/internal/config"
	"github.com/raven-clown/logbook/memory"
	"github.com/raven-clown/logbook/staleness"
	"github.com/raven-clown/logbook/store"
)

const usage = `usage: logbook <command> [flags]

commands:
  mcp                              serve the memory tools over MCP on stdin and stdout
  remember <content>               store a memory
  recall <query>                   search memories
  list                             browse memories, --order review for the review queue
  verify <id> confirm|dispute      confirm or dispute a memory
  forget <id>                      hide a memory, --purge to erase it
  import <format> <path>           import memories (formats: jsonl)
  export                           write every memory you can read as JSONL
  migrate                          bring the database schema up to date
  version                          print the version

every command takes --config <file>, --project <name> and --json`

type app struct {
	ctx     context.Context
	stdin   io.Reader
	stdout  io.Writer
	stderr  io.Writer
	environ []string
}

type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, environ []string) int {
	slog.SetDefault(slog.New(slog.NewJSONHandler(stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))
	a := &app{ctx: ctx, stdin: stdin, stdout: stdout, stderr: stderr, environ: environ}
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}
	commands := map[string]func([]string) error{
		"mcp": a.mcp, "remember": a.remember, "recall": a.recall, "list": a.list, "verify": a.verify,
		"forget": a.forget, "import": a.importCmd, "export": a.export, "migrate": a.migrate, "version": a.version,
	}
	cmd, ok := commands[args[0]]
	if !ok {
		_, _ = fmt.Fprintf(stderr, "unknown command %q\n\n%s\n", args[0], usage)
		return 2
	}
	err := cmd(args[1:])
	var ue usageError
	switch {
	case err == nil:
		return 0
	case errors.Is(err, flag.ErrHelp):
		return 0
	case errors.As(err, &ue):
		_, _ = fmt.Fprintf(stderr, "%s\n", ue.msg)
		return 2
	default:
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
}

type common struct {
	config  string
	project string
	json    bool
}

type stringList []string

func (l *stringList) String() string { return strings.Join(*l, ",") }

func (l *stringList) Set(v string) error {
	*l = append(*l, v)
	return nil
}

func (a *app) flags(name string) (*flag.FlagSet, *common) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	c := &common{}
	fs.StringVar(&c.config, "config", "", "config file")
	fs.StringVar(&c.project, "project", "", "current project")
	fs.BoolVar(&c.json, "json", false, "print JSON")
	return fs, c
}

// parse lets flags come before or after positional arguments.
func parse(fs *flag.FlagSet, args []string, positional int, use string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, err
			}
			return nil, usageError{msg: "usage: logbook " + use}
		}
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
	if positional >= 0 && len(pos) != positional {
		return nil, usageError{msg: "usage: logbook " + use}
	}
	return pos, nil
}

type session struct {
	svc     *memory.Service
	store   memory.Store
	cfg     config.Config
	project string
	now     func() time.Time
}

func (a *app) open(c *common) (*session, error) {
	cfg, err := config.Load(c.config, a.environ)
	if err != nil {
		return nil, err
	}
	now, err := a.clock()
	if err != nil {
		return nil, err
	}
	st, err := store.Open(a.ctx, cfg.Database.Driver, cfg.Database.DSN)
	if err != nil {
		return nil, err
	}
	project := c.project
	if project == "" {
		project = cfg.DefaultProject
	}
	svc := memory.NewService(st, memory.Options{Now: now, Rules: staleness.Default()})
	return &session{svc: svc, store: st, cfg: cfg, project: project, now: now}, nil
}

// clock honours LOGBOOK_NOW, which fixes the time for tests and recorded demos.
func (a *app) clock() (func() time.Time, error) {
	for _, kv := range a.environ {
		if v, ok := strings.CutPrefix(kv, "LOGBOOK_NOW="); ok && v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				return nil, fmt.Errorf("LOGBOOK_NOW must be an RFC 3339 time: %w", err)
			}
			return func() time.Time { return t }, nil
		}
	}
	return time.Now, nil
}

func (s *session) principal(ch memory.Channel) memory.Principal {
	return memory.Principal{OrgID: memory.DefaultOrg, Subject: "local", Channel: ch, Project: s.project}
}
