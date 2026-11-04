package memory_test

import (
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/raven-clown/logbook/memory"
)

func asError(err error, target **memory.Error) bool {
	return errors.As(err, target)
}

func TestCodeOf(t *testing.T) {
	conflict := memory.Errorf(memory.CodeConflict, "", "already %s", "superseded")
	tests := []struct {
		name string
		err  error
		want memory.Code
	}{
		{"nil", nil, ""},
		{"direct", conflict, memory.CodeConflict},
		{"wrapped", fmt.Errorf("saving: %w", conflict), memory.CodeConflict},
		{"foreign", io.EOF, memory.CodeInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := memory.CodeOf(tt.err); got != tt.want {
				t.Fatalf("CodeOf = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestErrorIsMatchesCode(t *testing.T) {
	if !errors.Is(memory.Errorf(memory.CodeNotFound, "", "gone"), memory.ErrNotFound) {
		t.Fatal("an error with code not_found should match ErrNotFound")
	}
	if errors.Is(memory.Errorf(memory.CodeConflict, "", "x"), memory.ErrNotFound) {
		t.Fatal("a conflict should not match ErrNotFound")
	}
	if !errors.Is(fmt.Errorf("ctx: %w", memory.ErrVersionConflict), memory.ErrVersionConflict) {
		t.Fatal("wrapped version conflict should match")
	}
}

func TestErrorMessage(t *testing.T) {
	e := memory.Errorf(memory.CodeInvalidArgument, "content", "content is %d bytes, the limit is %d", 9000, 8192)
	if e.Error() != "content is 9000 bytes, the limit is 8192" {
		t.Fatalf("Error() = %q", e.Error())
	}
	if e.Field != "content" {
		t.Fatalf("Field = %q", e.Field)
	}
}
