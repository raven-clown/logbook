package safejson_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/raven-clown/logbook/internal/safejson"
)

func TestEscape(t *testing.T) {
	raw := "a" + string([]rune{0x7f, 0x9b, 0x202e, 0x2066, 0x200b}) + "b ok é"
	in, _ := json.Marshal(map[string]string{"content": raw})
	out := safejson.Escape(in)
	for _, r := range []rune{0x7f, 0x9b, 0x202e, 0x2066, 0x200b} {
		if strings.ContainsRune(string(out), r) {
			t.Fatalf("rune %U left raw in %s", r, out)
		}
	}
	var back map[string]string
	if err := json.Unmarshal(out, &back); err != nil || back["content"] != raw {
		t.Fatalf("round trip = %q, %v", back["content"], err)
	}
	if !strings.Contains(string(out), "é") {
		t.Fatalf("plain letters escaped too: %s", out)
	}
}
