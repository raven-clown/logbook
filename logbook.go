// Package logbook is a shared memory for agents, served over MCP and REST.
package logbook

// Version is the release version, set at build time with -ldflags.
var Version = "dev"

// UserAgent is sent on outgoing HTTP calls, such as those to transponder.
func UserAgent() string {
	return "logbook/" + Version
}
