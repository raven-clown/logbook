// Command logbook is shared memory for agents: an MCP server with one-shot
// commands for people.
package main

import (
	"context"
	"os"

	_ "github.com/raven-clown/logbook/embed/openai"
	_ "github.com/raven-clown/logbook/extract/openai"
	_ "github.com/raven-clown/logbook/importer/jsonl"
	_ "github.com/raven-clown/logbook/store/sqlite"
	_ "github.com/raven-clown/logbook/vector/memory"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Environ()))
}
