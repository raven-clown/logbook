package memory

import (
	"errors"
	"fmt"
)

// Code is a stable error code shared by every interface: MCP, REST and admin.
type Code string

// The error codes.
const (
	CodeInvalidArgument  Code = "invalid_argument"
	CodeUnauthenticated  Code = "unauthenticated"
	CodePermissionDenied Code = "permission_denied"
	CodeNotFound         Code = "not_found"
	CodeConflict         Code = "conflict"
	CodeVersionConflict  Code = "version_conflict"
	CodeUnavailable      Code = "unavailable"
	CodeInternal         Code = "internal"
)

// Error is an error with a code and, for validation errors, the field at fault.
type Error struct {
	Code    Code
	Message string
	Field   string
}

// Sentinel errors that callers check with errors.Is.
var (
	ErrNotFound        = &Error{Code: CodeNotFound, Message: "memory not found"}
	ErrVersionConflict = &Error{Code: CodeVersionConflict, Message: "the memory changed since it was read"}
)

// Errorf returns an Error with a formatted message.
func Errorf(code Code, field, format string, args ...any) *Error {
	return &Error{Code: code, Field: field, Message: fmt.Sprintf(format, args...)}
}

func (e *Error) Error() string {
	return e.Message
}

// Is matches any Error with the same code, so errors.Is works against the sentinels.
func (e *Error) Is(target error) bool {
	var t *Error
	return errors.As(target, &t) && t.Code == e.Code
}

// CodeOf returns the code of err, CodeInternal for errors without one, and "" for nil.
func CodeOf(err error) Code {
	if err == nil {
		return ""
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return CodeInternal
}
