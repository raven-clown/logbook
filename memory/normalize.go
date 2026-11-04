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
