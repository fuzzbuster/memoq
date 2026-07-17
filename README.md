# memoq

A slim, **non-interactive** command-line client for a personal [Memos](https://github.com/usememos/memos)
server, built specifically to be driven by a **coding agent through a Skill**.

It is a deliberate slim-down of a fuller Memos client: **no TUI, no embedded MCP server,
no embedded LLM/agent loop, no vector search.** Just a scriptable CLI over a local SQLite
cache with full-text search and read-time lazy sync.

## Design

- **Local SQLite cache** (`modernc.org/sqlite`, pure Go — no CGO) with an FTS5 full-text
  index and a `LIKE` substring fallback for CJK text.
- **Read-time lazy sync**: read commands sync from the remote server if the cache is
  staler than `auto_sync_ttl_seconds`; network/auth failures are non-fatal (warn to
  stderr, serve the cache).
- **Incremental sync**: full remote fetch + content-MD5 hash diff (skip unchanged),
  plus deletion reconciliation.
- **Machine-friendly**: `--json` on every read command; deterministic plain-text
  otherwise; no prompts.

Standard library only, except the SQLite driver.

## Build

```bash
go build -o memoq .
```

Produces a single self-contained binary (~15 MB, no CGO).

## Configure

```bash
memoq config set server_url https://memos.example.com
memoq config set token <personal-access-token>
memoq config set auto_sync_ttl_seconds 30
```

Data lives under `$MEMOQ_HOME` (default `~/.memoq`). The token is stored `0600` and never
echoed back (`config get token` → `***set***`).

## Commands

memoq has two command tiers:

### Fast local-cache commands (FTS5 + read-time lazy sync)

| Command | Purpose |
|---|---|
| `memoq search <query> [--limit N] [--json]` | FTS5 full-text search (LIKE fallback) |
| `memoq list [--tag --from --to --visibility --limit --offset] [--json]` | Filtered list, pinned-first |
| `memoq get <uid> [--json]` | Show one note |
| `memoq create [--content \| stdin] [--tag ... --visibility ...]` | Create a note |
| `memoq update <uid> [--content --visibility]` | Update a note |
| `memoq delete <uid>` | Delete a note |
| `memoq stats [--json]` | Cache overview + tag counts |
| `memoq sync [--json]` | Force an incremental sync |
| `memoq config <get\|set\|path\|list>` | Manage config (token always masked) |

Read commands also accept `--no-sync` to skip the read-time auto-sync.

### Full-API resource commands (direct to server, raw JSON out)

Modeled on lark-cli's `resource-group + verb` structure. These hit the Memos v1
REST API directly and print the pretty-printed JSON response, giving **complete
API coverage** independent of the local cache.

| Group | Verbs |
|---|---|
| `memo` | `list get create update delete comments comment relations set-relations reactions react unreact attachments set-attachments shares share unshare link-metadata` |
| `attachment` | `list get create update delete batch-delete pull` |
| `user` | `list get create update delete all-stats stats settings setting update-setting tokens create-token delete-token webhooks` |
| `auth` | `me signin signout refresh` |
| `shortcut` | `list get create update delete` |
| `instance` | `profile setting update-setting stats` |
| `ai` | `transcribe` |
| `api` | `<METHOD> <PATH>` — generic escape hatch covering **any** endpoint |

Resource-command flags for supplying bodies / query params:

| Flag | Meaning |
|---|---|
| `--body '{...}'` | Raw JSON request body |
| `--body-file <path\|->` | JSON body from a file (`-` = stdin) |
| `--field k=v` | Body field, repeatable (value is JSON-parsed, string fallback) |
| `--query k=v` | Query parameter, repeatable |

Examples:

```bash
# fully typed resource verbs
memoq memo list --query pageSize=10 --query state=NORMAL
memoq memo create --field content='hi' --field visibility=PRIVATE
memoq memo update <uid> --field pinned=true
memoq user get me
memoq instance profile

# generic escape hatch — reaches any endpoint, current or future
memoq api GET  /api/v1/memos --query pageSize=10
memoq api POST /api/v1/memos --field content='hello' --field visibility=PRIVATE
memoq api PATCH /api/v1/memos/<uid> --body '{"pinned":true}' --query updateMask=pinned
```

## Attachments (local download for agents)

Attachment blob bytes are **never** returned by the JSON API — the Memos proto
marks the attachment `content` field `INPUT_ONLY`. The raw bytes are served by a
separate file-server route, `/file/attachments/{id}/{filename}`. So the plain
`attachment list` / `memo attachments <uid>` verbs only print metadata.

To let a coding agent actually read an image/file, use `attachment pull`:

```bash
memoq attachment pull <memo-uid>          # download all of a memo's attachments
memoq attachment pull <memo-uid> --json   # machine output with local_path per file
memoq attachment pull <memo-uid> --force  # re-download even if a copy exists
```

It lists the memo's attachments, downloads each blob to
`$MEMOQ_HOME/attachments/<id>/<filename>`, records the metadata + absolute local
path in the SQLite cache (`attachments` table), and prints where each file
landed. The agent can then Read that path directly (e.g. to recognize an image).
The bearer token is attached to the download so PRIVATE/PROTECTED attachments
work; externally-linked attachments are recorded but not downloaded.

## Layout

```
main.go                     entry point → cli.Run
internal/config/            config + path resolution
internal/store/             SQLite store, FTS5, migrations
internal/memos/             net/http Memos v1 API client (generic Do + typed helpers)
internal/syncer/            incremental sync engine
internal/cli/               command dispatch, output formatting
internal/cli/resource.go    lark-cli-style resource-group + verb commands (full API)
skills/memoq/SKILL.md       companion Skill for coding agents
docs/UPSTREAM_SYNC.md       playbook for tracking upstream Memos API changes
docs/ENDPOINTS.md           canonical endpoint manifest (diff target)
```
