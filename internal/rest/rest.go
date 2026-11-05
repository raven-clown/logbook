// Package rest serves the memory operations under /v1/.
package rest

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/raven-clown/logbook/importer/jsonl"
	"github.com/raven-clown/logbook/internal/httpx"
	"github.com/raven-clown/logbook/internal/identity"
	"github.com/raven-clown/logbook/internal/wire"
	"github.com/raven-clown/logbook/memory"
)

// Options configure the handlers.
type Options struct {
	Service        *memory.Service
	DefaultProject string
}

type api struct{ Options }

// Register adds the /v1/ routes to mux. Requests must carry a caller from
// identity in their context.
func Register(mux *http.ServeMux, o Options) {
	a := &api{o}
	mux.HandleFunc("POST /v1/memories", a.with(a.remember))
	mux.HandleFunc("POST /v1/recall", a.with(a.recall))
	mux.HandleFunc("GET /v1/memories", a.with(a.list))
	mux.HandleFunc("GET /v1/memories/{id}", a.with(a.detail))
	mux.HandleFunc("DELETE /v1/memories/{id}", a.with(a.forget))
	mux.HandleFunc("POST /v1/memories/{id}/verify", a.with(a.verify))
	mux.HandleFunc("POST /v1/memories/{id}/links", a.with(a.link))
	mux.HandleFunc("GET /v1/export", a.with(a.export))
}

type handler func(w http.ResponseWriter, r *http.Request, p memory.Principal) error

func (a *api) with(h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := a.principal(r)
		if err == nil {
			err = h(w, r, p)
		}
		if err != nil {
			httpx.WriteError(w, r, err)
		}
	}
}

func (a *api) principal(r *http.Request) (memory.Principal, error) {
	c, ok := identity.FromContext(r.Context())
	if !ok {
		return memory.Principal{}, memory.Errorf(memory.CodeUnauthenticated, "", "send Authorization: Bearer <token>")
	}
	p := c.Principal
	p.Channel = memory.ChannelREST
	p.Client = httpx.ClientName(r)
	var err error
	p.Project, err = httpx.Project(r, a.DefaultProject)
	return p, err
}

func (a *api) remember(w http.ResponseWriter, r *http.Request, p memory.Principal) error {
	var body wire.Remember
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	req, err := body.ToRequest()
	if err != nil {
		return err
	}
	res, err := a.Service.Remember(r.Context(), p, req)
	if err != nil {
		return err
	}
	status := http.StatusOK
	for _, m := range res.Memories {
		if !m.Duplicate {
			status = http.StatusCreated
		}
	}
	httpx.WriteJSON(w, status, res)
	return nil
}

func (a *api) recall(w http.ResponseWriter, r *http.Request, p memory.Principal) error {
	var body wire.Recall
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	req, err := body.ToRequest()
	if err != nil {
		return err
	}
	res, err := a.Service.Recall(r.Context(), p, req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, res)
	return nil
}

func boolParam(q url.Values, name string) (bool, error) {
	v := q.Get(name)
	if v == "" {
		return false, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, memory.Errorf(memory.CodeInvalidArgument, name, "%s must be true or false", name)
	}
	return b, nil
}

func (a *api) list(w http.ResponseWriter, r *http.Request, p memory.Principal) error {
	q := r.URL.Query()
	body := wire.List{
		Scopes: q["scope"], Project: q.Get("project"), Team: q.Get("team"), Status: q["status"],
		Kind: q.Get("kind"), Subject: q.Get("subject"), Tag: q.Get("tag"), Author: q.Get("author"),
		Order: q.Get("order"), Cursor: q.Get("cursor"),
	}
	var err error
	if body.Stale, err = boolParam(q, "stale"); err != nil {
		return err
	}
	if v := q.Get("limit"); v != "" {
		if body.Limit, err = strconv.Atoi(v); err != nil {
			return memory.Errorf(memory.CodeInvalidArgument, "limit", "limit must be a whole number")
		}
	}
	res, err := a.Service.List(r.Context(), p, body.ToRequest())
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, res)
	return nil
}

func (a *api) detail(w http.ResponseWriter, r *http.Request, p memory.Principal) error {
	d, err := a.Service.Detail(r.Context(), p, r.PathValue("id"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, d)
	return nil
}

func (a *api) forget(w http.ResponseWriter, r *http.Request, p memory.Principal) error {
	q := r.URL.Query()
	purge, err := boolParam(q, "purge")
	if err != nil {
		return err
	}
	req := wire.Forget{ID: r.PathValue("id"), Reason: q.Get("reason"), Purge: purge}.ToRequest()
	m, err := a.Service.Forget(r.Context(), p, req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, memory.NewForgetResult(m))
	return nil
}

func (a *api) verify(w http.ResponseWriter, r *http.Request, p memory.Principal) error {
	var body struct {
		Verdict string `json:"verdict"`
		Note    string `json:"note"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	res, err := a.Service.Verify(r.Context(), p, wire.Verify{ID: r.PathValue("id"), Verdict: body.Verdict, Note: body.Note}.ToRequest())
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, res)
	return nil
}

func (a *api) link(w http.ResponseWriter, r *http.Request, p memory.Principal) error {
	var body struct {
		To string `json:"to"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	e, err := a.Service.Link(r.Context(), p, r.PathValue("id"), body.To)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, e)
	return nil
}

// export streams JSONL. An error before the first record still gets an error
// response; after that the stream just ends.
func (a *api) export(w http.ResponseWriter, r *http.Request, p memory.Principal) error {
	started := false
	err := a.Service.Export(r.Context(), p, r.URL.Query()["scope"], func(rec memory.Record) error {
		if !started {
			w.Header().Set("Content-Type", "application/x-ndjson")
			started = true
		}
		return jsonl.Write(w, rec)
	})
	if err != nil && started {
		return nil
	}
	if err == nil && !started {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
	}
	return err
}
