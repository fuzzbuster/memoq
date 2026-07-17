// Package cli implements memoq's non-interactive command dispatch. Every
// command is scriptable: no prompts, no TUI, deterministic output, and a
// --json flag on read commands for machine consumption by a coding agent.
package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/example/memoq/internal/config"
	"github.com/example/memoq/internal/memos"
	"github.com/example/memoq/internal/store"
	"github.com/example/memoq/internal/syncer"
)

const usage = `memoq - Memos CLI for coding agents (no TUI / no MCP / no LLM)

USAGE:
  memoq <command> [flags]

FAST LOCAL-CACHE COMMANDS (FTS5 + read-time lazy sync):
  search  <query>        Full-text search local notes (FTS5, LIKE fallback)
  list                   List notes with filters (--tag --from --to --limit)
  get     <uid>          Show one note
  create                 Create a note (--content or stdin, --tag, --visibility)
  update  <uid>          Update a note (--content / --visibility)
  delete  <uid>          Delete a note
  stats                  Local cache overview
  sync                   Force an incremental sync from the server
  config  <get|set|path> Manage server_url / token / auto_sync_ttl_seconds

FULL-API RESOURCE COMMANDS (direct to server, raw JSON out):
  memo       <list|get|create|update|delete|comments|comment|relations|
              set-relations|reactions|react|unreact|attachments|set-attachments|
              shares|share|unshare|link-metadata>
  attachment <list|get|create|update|delete|batch-delete|pull>
  user       <list|get|create|update|delete|all-stats|stats|settings|setting|
              update-setting|tokens|create-token|delete-token|webhooks>
  auth       <me|signin|signout|refresh>
  shortcut   <list|get|create|update|delete>
  instance   <profile|setting|update-setting|stats>
  ai         <transcribe>
  api        <METHOD> <PATH>   Generic escape hatch (covers any endpoint)

RESOURCE-COMMAND FLAGS:
  --body '{...}'         Raw JSON request body
  --body-file <path|->   JSON body from a file ('-' = stdin)
  --field k=v            Body field (repeatable; value JSON-parsed, string fallback)
  --query k=v            Query parameter (repeatable)

GLOBAL FLAGS (fast read commands):
  --json                 Emit JSON instead of plain text
  --no-sync              Skip read-time auto-sync (use local cache as-is)

Run 'memoq <resource>' with no verb to see its verbs.

CONFIG:
  memoq config set server_url https://memos.example.com
  memoq config set token <personal-access-token>
  memoq config set auto_sync_ttl_seconds 30
Data lives under $MEMOQ_HOME (default ~/.memoq).
`

// Run dispatches a command. args excludes the program name.
func Run(args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return nil
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "-h", "--help", "help":
		fmt.Print(usage)
		return nil
	case "search":
		return cmdSearch(rest)
	case "list":
		return cmdList(rest)
	case "get":
		return cmdGet(rest)
	case "create":
		return cmdCreate(rest)
	case "update":
		return cmdUpdate(rest)
	case "delete":
		return cmdDelete(rest)
	case "stats":
		return cmdStats(rest)
	case "sync":
		return cmdSync(rest)
	case "config":
		return cmdConfig(rest)
	default:
		if isResourceGroup(cmd) {
			return dispatchResource(cmd, rest)
		}
		return fmt.Errorf("unknown command %q (run 'memoq help')", cmd)
	}
}

// app bundles the resolved runtime dependencies shared by commands.
type app struct {
	paths *config.Paths
	cfg   *config.Config
	store *store.Store
}

// openApp resolves paths, loads config, and opens the store. Commands that do
// not need the network still get a working local store.
func openApp() (*app, error) {
	paths, err := config.ResolvePaths()
	if err != nil {
		return nil, err
	}
	if err := paths.EnsureHome(); err != nil {
		return nil, err
	}
	cfg, err := config.Load(paths)
	if err != nil {
		return nil, err
	}
	st, err := store.Open(paths.DBFile)
	if err != nil {
		return nil, err
	}
	return &app{paths: paths, cfg: cfg, store: st}, nil
}

func (a *app) close() {
	if a.store != nil {
		_ = a.store.Close()
	}
}

// client builds an authenticated Memos client, validating config first.
func (a *app) client() (*memos.Client, error) {
	if err := a.cfg.Validate(); err != nil {
		return nil, err
	}
	return memos.New(a.cfg.ServerURL, a.cfg.Token), nil
}

// autoSync runs a read-time lazy sync unless disabled by the flag or config.
// Network/auth failures are non-fatal for read commands: the agent can still
// use the local cache, so we warn to stderr and continue.
func (a *app) autoSync(noSync bool) {
	if noSync || a.cfg.AutoSyncTTLSeconds <= 0 {
		return
	}
	if err := a.cfg.Validate(); err != nil {
		return // not configured yet; nothing to sync
	}
	cl := memos.New(a.cfg.ServerURL, a.cfg.Token)
	sy := syncer.New(cl, a.store)
	if _, err := sy.MaybeAutoSync(context.Background(), a.cfg.AutoSyncTTLSeconds); err != nil {
		fmt.Fprintln(os.Stderr, "memoq: auto-sync skipped ("+err.Error()+"); using local cache")
	}
}
