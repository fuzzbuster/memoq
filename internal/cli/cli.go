// Package cli implements memoq's non-interactive command dispatch. Every
// command is scriptable: no prompts, no TUI, deterministic output, and a
// --json flag on read commands for machine consumption by a coding agent.
package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/example/memoq/internal/config"
	"github.com/example/memoq/internal/memos"
	"github.com/example/memoq/internal/store"
	"github.com/example/memoq/internal/syncer"
	"github.com/spf13/cobra"
)

// Run dispatches a command. args excludes the program name.
func Run(args []string) error {
	root := newRootCommand()
	root.SetArgs(args)
	return root.Execute()
}

func newRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "memoq",
		Short:         "Memos CLI for coding agents",
		Long:          rootLongHelp(),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	root.SetOut(os.Stdout)
	root.SetErr(os.Stderr)

	root.AddCommand(
		legacyCommand("search <query>", "Full-text search local notes", cmdSearch),
		legacyCommand("list", "List notes with filters", cmdList),
		legacyCommand("get <uid>", "Show one note", cmdGet),
		legacyCommand("create", "Create a note", cmdCreate),
		legacyCommand("update <uid>", "Update a note", cmdUpdate),
		legacyCommand("delete <uid>", "Delete a note", cmdDelete),
		legacyCommand("stats", "Local cache overview", cmdStats),
		legacyCommand("sync", "Force an incremental sync from the server", cmdSync),
		legacyCommand("config <get|set|path|list>", "Manage local configuration", cmdConfig),
	)
	for _, spec := range resourceGroupSpecs {
		root.AddCommand(resourceCommand(spec))
	}
	return root
}

func legacyCommand(use, short string, run func([]string) error) *cobra.Command {
	return &cobra.Command{
		Use:                use,
		Short:              short,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if wantsHelp(args) {
				return cmd.Help()
			}
			return run(args)
		},
	}
}

func resourceCommand(spec resourceGroupSpec) *cobra.Command {
	long := fmt.Sprintf("%s\n\nVerbs: %s", spec.Short, strings.Join(spec.Verbs, " "))
	if spec.Name != "api" {
		long += "\n\nFlags: --body, --body-file, --field, --query"
	}
	return &cobra.Command{
		Use:                spec.Use,
		Short:              spec.Short,
		Long:               long,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if wantsHelp(args) {
				return cmd.Help()
			}
			return dispatchResource(spec.Name, args)
		},
	}
}

func wantsHelp(args []string) bool {
	return len(args) == 1 && (args[0] == "-h" || args[0] == "--help")
}

func rootLongHelp() string {
	return `memoq is a minimal, non-interactive Memos CLI.

Fast local-cache commands:
  search, list, get, create, update, delete, stats, sync, config

Full-API resource commands:
  memo, attachment, user, auth, shortcut, instance, ai, api

Resource commands follow the lark-cli-style shape:
  memoq <resource> <verb> [positional] [flags]

Shared resource flags:
  --body, --body-file, --field, --query

Data lives under $MEMOQ_HOME (default ~/.memoq).`
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
