// Package safejson escapes characters that encoding/json leaves raw but a
// terminal acts on: DEL, C1 controls and Unicode format characters such as
// bidi overrides. The JSON means the same after escaping.
package safejson

import (
	"bytes"
	"fmt"
	"unicode"
	"unicode/utf8"
)

// Escape returns b with those characters written as \uXXXX. b must be JSON
// from encoding/json, where they can only appear inside strings.
func Escape(b []byte) []byte {
	var out bytes.Buffer
	for len(b) > 0 {
		r, n := utf8.DecodeRune(b)
		if r >= 0x7f && (unicode.IsControl(r) || unicode.Is(unicode.Cf, r)) && r <= 0xffff {
			fmt.Fprintf(&out, `\u%04x`, r)
		} else {
			out.Write(b[:n])
		}
		b = b[n:]
	}
	return out.Bytes()
}
