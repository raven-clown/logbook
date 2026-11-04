// Command logbook is shared memory for agents: an MCP server with one-shot
// commands for people.
package main

import (
	"context"
	"os"

	_ "github.com/raven-clown/logbook/importer/jsonl"
	_ "github.com/raven-clown/logbook/store/sqlite"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Environ()))
}
