// Package httpx holds the JSON and error helpers shared by the HTTP APIs.
package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"unicode"

	"github.com/raven-clown/logbook/memory"
)

// MaxBody is the largest request body the APIs read.
const MaxBody = 256 << 10

// Status maps an error code to its HTTP status.
func Status(c memory.Code) int {
	switch c {
	case memory.CodeInvalidArgument:
		return http.StatusBadRequest
	case memory.CodeUnauthenticated:
		return http.StatusUnauthorized
	case memory.CodePermissionDenied:
		return http.StatusForbidden
	case memory.CodeNotFound:
		return http.StatusNotFound
	case memory.CodeConflict, memory.CodeVersionConflict, "managed_by_file":
		return http.StatusConflict
	case memory.CodeUnavailable:
		return http.StatusServiceUnavailable
	}
	return http.StatusInternalServerError
}

// WriteJSON writes v with status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type errorBody struct {
	Error struct {
		Code    memory.Code `json:"code"`
		Message string      `json:"message"`
		Field   string      `json:"field,omitempty"`
	} `json:"error"`
}

// WriteError writes err in the shared error shape. Internal errors are logged
// and their message is not sent.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var b errorBody
	b.Error.Code = memory.CodeOf(err)
	b.Error.Message = err.Error()
	var e *memory.Error
	if errors.As(err, &e) {
		b.Error.Field = e.Field
		b.Error.Message = e.Message
	}
	if Status(b.Error.Code) == http.StatusInternalServerError {
		slog.ErrorContext(r.Context(), "request failed", "method", r.Method, "path", r.URL.Path, "err", err)
		b.Error.Code = memory.CodeInternal
		b.Error.Message = "internal error"
	}
	WriteJSON(w, Status(b.Error.Code), b)
}

func invalid(field, format string, args ...any) error {
	return memory.Errorf(memory.CodeInvalidArgument, field, format, args...)
}

// DecodeJSON reads one JSON value into v. Unknown fields, wrong types, trailing
// data and bodies over MaxBody are invalid_argument, naming the field when there is one.
func DecodeJSON(r *http.Request, v any) error {
	data, err := io.ReadAll(io.LimitReader(r.Body, MaxBody+1))
	if err != nil {
		return invalid("", "reading the request body: %v", err)
	}
	if len(data) > MaxBody {
		return invalid("", "the request body is larger than %d KiB", MaxBody>>10)
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var te *json.UnmarshalTypeError
		switch {
		case errors.As(err, &te):
			field, _, _ := strings.Cut(te.Field, ".")
			return invalid(field, "%s must be %s", te.Field, te.Type)
		case strings.HasPrefix(err.Error(), "json: unknown field "):
			field := strings.Trim(strings.TrimPrefix(err.Error(), "json: unknown field "), `"`)
			return invalid(field, "unknown field %q", field)
		}
		return invalid("", "the request body is not valid JSON: %v", err)
	}
	if dec.More() {
		return invalid("", "the request body holds more than one JSON value")
	}
	return nil
}

// ClientName is the User-Agent, cleaned by CleanClientName.
func ClientName(r *http.Request) string {
	return CleanClientName(r.UserAgent())
}

// CleanClientName drops control and format characters from a client name and
// cuts it to 100 bytes, since any client can send any name.
func CleanClientName(name string) string {
	name = strings.Map(func(c rune) rune {
		if unicode.IsControl(c) || unicode.Is(unicode.Cf, c) {
			return -1
		}
		return c
	}, name)
	if len(name) > 100 {
		name = strings.ToValidUTF8(name[:100], "")
	}
	return name
}

// Project returns the X-Project header, or def when it is absent.
func Project(r *http.Request, def string) (string, error) {
	p := r.Header.Get("X-Project")
	if p == "" {
		return def, nil
	}
	if _, err := memory.ParseScope("project:" + p); err != nil {
		return "", memory.Errorf(memory.CodeInvalidArgument, "X-Project", "X-Project %q is not a valid project name", p)
	}
	return p, nil
}
