// Command memoq is a minimal, non-interactive CLI that mirrors a remote Memos
// server into a local SQLite database (with FTS5 full-text search) so that a
// coding agent can query and mutate notes quickly, offline, and with
// token-friendly output.
//
// It intentionally has NO TUI, NO embedded MCP server, and NO embedded LLM.
// All "understanding" is delegated to the calling agent; memoq is just a fast,
// deterministic data layer driven through subcommands + a companion Skill.
package main

import (
	"fmt"
	"os"

	"github.com/example/memoq/internal/cli"
)

func main() {
	if err := cli.Run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "memoq: "+err.Error())
		os.Exit(1)
	}
}
