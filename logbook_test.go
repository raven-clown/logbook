package logbook

import "testing"

func TestUserAgent(t *testing.T) {
	tests := []struct {
		name    string
		version string
		want    string
	}{
		{name: "development build", version: "dev", want: "logbook/dev"},
		{name: "release build", version: "0.1.0", want: "logbook/0.1.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old := Version
			t.Cleanup(func() { Version = old })
			Version = tt.version
			if got := UserAgent(); got != tt.want {
				t.Errorf("UserAgent() = %q, want %q", got, tt.want)
			}
		})
	}
}
