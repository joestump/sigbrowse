// Command msgbrowse is a self-hosted, local-only browser, search engine, and
// MCP server over a signal-export archive.
//
// It exposes several subcommands:
//
//	msgbrowse ingest   scan the archive and (incrementally) populate the database
//	msgbrowse serve    run the local HTMX web UI
//	msgbrowse mcp      run the Model Context Protocol server
//	msgbrowse watch    re-ingest automatically when the archive changes
//	msgbrowse journal  (re)build the day-by-day journal and optional LLM digests
//
// Everything runs against an on-disk, already-decrypted signal-export tree that
// is treated as strictly read-only. See README.md and ARCHITECTURE.md for the
// full design.
package main

import (
	"fmt"
	"os"

	"github.com/joestump/msgbrowse/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "msgbrowse:", err)
		os.Exit(1)
	}
}
