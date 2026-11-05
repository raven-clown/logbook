package obs

import "testing"

func TestTraceURL(t *testing.T) {
	for in, want := range map[string]string{
		"http://otel:4318":            "http://otel:4318/v1/traces",
		"http://otel:4318/":           "http://otel:4318/v1/traces",
		"https://otel.example.com/x":  "https://otel.example.com/x",
		"http://otel:4318/v1/traces/": "http://otel:4318/v1/traces/",
	} {
		got, err := traceURL(in)
		if err != nil || got != want {
			t.Fatalf("traceURL(%q) = %q, %v, want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"otel:4318", "ftp://otel", "::"} {
		if _, err := traceURL(bad); err == nil {
			t.Fatalf("traceURL(%q) accepted", bad)
		}
	}
}
