package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode/utf8"
)

// MaxContentBytes is the largest memory logbook stores.
const MaxContentBytes = 8192

// NormalizeContent trims s and collapses each run of whitespace to one space.
func NormalizeContent(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// ContentHash identifies content regardless of case and spacing, for finding duplicates.
func ContentHash(s string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(NormalizeContent(s))))
	return hex.EncodeToString(sum[:])
}

func validContent(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", Errorf(CodeInvalidArgument, "content", "content is not valid UTF-8")
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "", Errorf(CodeInvalidArgument, "content", "content is empty")
	}
	if len(s) > MaxContentBytes {
		return "", Errorf(CodeInvalidArgument, "content", "content is %d bytes, the limit is %d", len(s), MaxContentBytes)
	}
	return s, nil
}

// Limits on the metadata a memory can carry.
const (
	MaxSubjectBytes  = 200
	MaxEvidenceBytes = 8192
	MaxListItems     = 32
	MaxTagBytes      = 64
	MaxRefBytes      = 2048
)

func validMeta(r RememberRequest) error {
	if !utf8.ValidString(r.Subject) || len(r.Subject) > MaxSubjectBytes {
		return Errorf(CodeInvalidArgument, "subject", "subject must be valid UTF-8 and at most %d bytes", MaxSubjectBytes)
	}
	if !utf8.ValidString(r.Evidence) || len(r.Evidence) > MaxEvidenceBytes {
		return Errorf(CodeInvalidArgument, "evidence", "evidence must be valid UTF-8 and at most %d bytes", MaxEvidenceBytes)
	}
	if err := validList("tags", r.Tags, MaxTagBytes); err != nil {
		return err
	}
	if err := validList("refs", r.Refs, MaxRefBytes); err != nil {
		return err
	}
	if len(r.RelatedTo) > MaxListItems {
		return Errorf(CodeInvalidArgument, "related_to", "at most %d related memories", MaxListItems)
	}
	switch r.Source {
	case "", SourceDirect, SourceExtracted, SourceImported:
		return nil
	}
	return Errorf(CodeInvalidArgument, "source", "unknown source %q", r.Source)
}

func validList(field string, items []string, maxBytes int) error {
	if len(items) > MaxListItems {
		return Errorf(CodeInvalidArgument, field, "at most %d %s", MaxListItems, field)
	}
	for _, it := range items {
		if strings.TrimSpace(it) == "" || !utf8.ValidString(it) || len(it) > maxBytes {
			return Errorf(CodeInvalidArgument, field, "each of %s must be non-empty valid UTF-8 of at most %d bytes", field, maxBytes)
		}
	}
	return nil
}
