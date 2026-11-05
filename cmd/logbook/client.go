package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/raven-clown/logbook"
	"github.com/raven-clown/logbook/importer/jsonl"
	"github.com/raven-clown/logbook/internal/wire"
	"github.com/raven-clown/logbook/memory"
)

// backend runs the memory commands on the local database or on a server.
type backend interface {
	remember(ctx context.Context, r wire.Remember) (*memory.RememberResult, error)
	recall(ctx context.Context, r wire.Recall) (*memory.RecallResult, error)
	list(ctx context.Context, r wire.List) (*memory.ListResult, error)
	verify(ctx context.Context, r wire.Verify) (*memory.VerifyResult, error)
	forget(ctx context.Context, r wire.Forget) (*memory.Memory, error)
	export(ctx context.Context, scopes []string, w io.Writer) error
	now() time.Time
	close() error
}

type remoteFlags struct {
	server string
	token  string
}

func (a *app) addRemoteFlags(fsFlags interface {
	StringVar(p *string, name, value, usage string)
}) *remoteFlags {
	r := &remoteFlags{}
	fsFlags.StringVar(&r.server, "server", "", "call this logbook server instead of the local database")
	fsFlags.StringVar(&r.token, "token", "", "API key or token for --server, default LOGBOOK_TOKEN")
	return r
}

func (a *app) backend(c *common, rf *remoteFlags) (backend, error) {
	if rf.server == "" {
		s, err := a.open(c)
		if err != nil {
			return nil, err
		}
		return &local{s: s}, nil
	}
	u, err := url.Parse(rf.server)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("--server must be an http or https URL, not %q", rf.server)
	}
	token := rf.token
	if token == "" {
		for _, kv := range a.environ {
			if v, ok := strings.CutPrefix(kv, "LOGBOOK_TOKEN="); ok {
				token = v
			}
		}
	}
	if token == "" {
		return nil, fmt.Errorf("--server needs a token: pass --token or set LOGBOOK_TOKEN")
	}
	now, err := a.clock()
	if err != nil {
		return nil, err
	}
	return &remote{base: strings.TrimSuffix(rf.server, "/"), token: token, project: c.project, clock: now,
		client: &http.Client{Timeout: 30 * time.Second}}, nil
}

type local struct{ s *session }

func (l *local) p() memory.Principal { return l.s.principal(memory.ChannelCLI) }

func (l *local) remember(ctx context.Context, r wire.Remember) (*memory.RememberResult, error) {
	req, err := r.ToRequest()
	if err != nil {
		return nil, err
	}
	return l.s.svc.Remember(ctx, l.p(), req)
}

func (l *local) recall(ctx context.Context, r wire.Recall) (*memory.RecallResult, error) {
	req, err := r.ToRequest()
	if err != nil {
		return nil, err
	}
	return l.s.svc.Recall(ctx, l.p(), req)
}

func (l *local) list(ctx context.Context, r wire.List) (*memory.ListResult, error) {
	return l.s.svc.List(ctx, l.p(), r.ToRequest())
}

func (l *local) verify(ctx context.Context, r wire.Verify) (*memory.VerifyResult, error) {
	return l.s.svc.Verify(ctx, l.p(), r.ToRequest())
}

func (l *local) forget(ctx context.Context, r wire.Forget) (*memory.Memory, error) {
	return l.s.svc.Forget(ctx, l.p(), r.ToRequest())
}

func (l *local) export(ctx context.Context, scopes []string, w io.Writer) error {
	return l.s.svc.Export(ctx, l.p(), scopes, func(r memory.Record) error { return jsonl.Write(w, r) })
}

func (l *local) now() time.Time { return l.s.now() }

func (l *local) close() error { return l.s.store.Close() }

type remote struct {
	base, token, project string
	clock                func() time.Time
	client               *http.Client
}

func (r *remote) now() time.Time { return r.clock() }

func (r *remote) close() error { return nil }

func (r *remote) request(ctx context.Context, method, path string, q url.Values, body any) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	u := r.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	req.Header.Set("User-Agent", logbook.UserAgent())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if r.project != "" {
		req.Header.Set("X-Project", r.project)
	}
	res, err := r.client.Do(req) // #nosec G704 -- the URL is the operator's --server argument
	if err != nil {
		return nil, fmt.Errorf("calling %s: %w", r.base, err)
	}
	if res.StatusCode >= 300 {
		defer func() { _ = res.Body.Close() }()
		return nil, remoteError(res)
	}
	return res, nil
}

func remoteError(res *http.Response) error {
	var e struct {
		Error struct {
			Code    memory.Code `json:"code"`
			Message string      `json:"message"`
			Field   string      `json:"field"`
		} `json:"error"`
	}
	b, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if json.Unmarshal(b, &e) != nil || e.Error.Code == "" {
		return fmt.Errorf("the server answered %s", res.Status)
	}
	return memory.Errorf(e.Error.Code, e.Error.Field, "%s", e.Error.Message)
}

func (r *remote) call(ctx context.Context, method, path string, q url.Values, body, out any) error {
	res, err := r.request(ctx, method, path, q, body)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("reading the server's answer: %w", err)
	}
	return nil
}

func (r *remote) remember(ctx context.Context, w wire.Remember) (*memory.RememberResult, error) {
	var out memory.RememberResult
	return &out, r.call(ctx, http.MethodPost, "/v1/memories", nil, w, &out)
}

func (r *remote) recall(ctx context.Context, w wire.Recall) (*memory.RecallResult, error) {
	var out memory.RecallResult
	return &out, r.call(ctx, http.MethodPost, "/v1/recall", nil, w, &out)
}

func (r *remote) list(ctx context.Context, w wire.List) (*memory.ListResult, error) {
	q := url.Values{"scope": w.Scopes, "status": w.Status}
	for k, v := range map[string]string{"project": w.Project, "team": w.Team, "kind": w.Kind, "subject": w.Subject,
		"tag": w.Tag, "author": w.Author, "order": w.Order, "cursor": w.Cursor} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if w.Stale {
		q.Set("stale", "true")
	}
	if w.Limit != 0 {
		q.Set("limit", strconv.Itoa(w.Limit))
	}
	var out memory.ListResult
	return &out, r.call(ctx, http.MethodGet, "/v1/memories", q, nil, &out)
}

func (r *remote) verify(ctx context.Context, w wire.Verify) (*memory.VerifyResult, error) {
	var out memory.VerifyResult
	body := map[string]string{"verdict": w.Verdict, "note": w.Note}
	return &out, r.call(ctx, http.MethodPost, "/v1/memories/"+url.PathEscape(w.ID)+"/verify", nil, body, &out)
}

func (r *remote) forget(ctx context.Context, w wire.Forget) (*memory.Memory, error) {
	q := url.Values{}
	if w.Reason != "" {
		q.Set("reason", w.Reason)
	}
	if w.Purge {
		q.Set("purge", "true")
	}
	var out memory.Memory
	return &out, r.call(ctx, http.MethodDelete, "/v1/memories/"+url.PathEscape(w.ID), q, nil, &out)
}

func (r *remote) export(ctx context.Context, scopes []string, w io.Writer) error {
	res, err := r.request(ctx, http.MethodGet, "/v1/export", url.Values{"scope": scopes}, nil)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	_, err = io.Copy(w, res.Body)
	return err
}
