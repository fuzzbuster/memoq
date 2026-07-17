# memoq

A slim, **non-interactive** command-line client for a personal
[Memos](https://github.com/usememos/memos) server, built to be driven by a
**coding agent**. No TUI, no MCP server, no embedded LLM — just a scriptable CLI
over a local SQLite cache (FTS5 full-text search) with read-time lazy sync, plus
full coverage of the Memos v1 REST API.

Every read command supports `--json`; nothing ever prompts for input.

## Install

Requires Go 1.24+. Build a single self-contained binary (~15 MB, pure Go, no
CGO), then put it on your `PATH`:

```bash
go build -o memoq .
mv memoq ~/bin/            # any directory on your PATH
```

## Configure (one-time)

Point memoq at your server and personal access token:

```bash
memoq config set server_url https://memos.example.com
memoq config set token <personal-access-token>
memoq config set auto_sync_ttl_seconds 30   # optional; 0 disables read-time auto-sync
```

- `memoq config list` — dump the current config as JSON (the token is masked).
- `memoq config get <key>` — read one key (`server_url` / `token` / `auto_sync_ttl_seconds`).
- `memoq config path` — print the config file and DB file locations.

Data lives under `$MEMOQ_HOME` (default `~/.memoq`). The token is stored `0600`
and is never echoed back (`config get token` → `***set***`).

## Usage

memoq has two command tiers. Use the **fast local-cache commands** for everyday
search/list/read/write; drop to the **full-API resource commands** for anything
the cache doesn't model.

### Fast local-cache commands (FTS5 + read-time lazy sync)

These serve a local SQLite cache and auto-sync from the server if it is staler
than `auto_sync_ttl_seconds`. Add `--no-sync` to skip the sync and use the cache
as-is; add `--json` to parse the output. Flags may appear before, after, or
between positional args.

```bash
# search (full-text, FTS5 with a LIKE substring fallback)
memoq search "deployment checklist" --json --limit 10
memoq search "同步" --json          # CJK: use a short substring, not a full sentence

# list with filters (pinned-first, then newest-first)
memoq list --json --limit 50
memoq list --tag project --from 2026-01-01 --to 2026-06-30 --json
memoq list --visibility PUBLIC --offset 50 --json

# read one note
memoq get <uid> --json

# create (body from --content or stdin; --tag is repeatable; --visibility defaults to PRIVATE)
memoq create --content "Ship notes: cut RC on Friday" --tag release --tag ops
echo "multi-line body" | memoq create --tag inbox

# update / delete
memoq update <uid> --content "revised body" --visibility PROTECTED
memoq delete <uid>

# overview + tag counts (handy to discover which tags exist before list --tag)
memoq stats --json

# force a full incremental sync from the server
memoq sync --json
```

Notes:
- **Freshness**: reads auto-sync per the TTL. If you used `--no-sync`, or a `get`
  misses a just-created note, run `memoq sync` and retry.
- **CJK search**: FTS5 tokenizes Chinese poorly, so the LIKE fallback carries it —
  search with a concrete substring rather than a full sentence. If a search
  misses, fall back to `list --tag` / date filters.
- **No matches is normal**, not an error — retry with different keywords.
- An `auto-sync skipped (...)` line on **stderr** is non-fatal: the network/auth
  failed but the local cache is still served. Only a non-zero exit is a real
  failure.

### Full-API resource commands (direct to server, raw JSON out)

Modeled on lark-cli's `resource-group + verb` structure. These hit the Memos v1
REST API directly and print the pretty-printed JSON response, giving complete
API coverage independent of the local cache.

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

Supply request bodies / query params with:

| Flag | Meaning |
|---|---|
| `--body '{...}'` | Raw JSON request body |
| `--body-file <path\|->` | JSON body from a file (`-` = stdin) |
| `--field k=v` | Body field, repeatable (value is JSON-parsed, string fallback) |
| `--query k=v` | Query parameter, repeatable |

```bash
# typed resource verbs
memoq memo list --query pageSize=10 --query state=NORMAL
memoq memo create --field content='hi' --field visibility=PRIVATE
memoq memo update <uid> --field pinned=true --query updateMask=pinned
memoq user get me
memoq instance profile

# generic escape hatch — reaches any endpoint, current or future
memoq api GET  /api/v1/memos --query pageSize=10
memoq api POST /api/v1/memos --field content='hello' --field visibility=PRIVATE
memoq api PATCH /api/v1/memos/<uid> --body '{"pinned":true}' --query updateMask=pinned
```

Run any group with no verb (e.g. `memoq user`) to print its verb list.

> Resource / `api` commands do **not** touch the local cache — they neither
> auto-sync nor reflect their writes into it. After a mutating resource command
> you also want cached locally, run `memoq sync`.

## Attachments (local download for agents)

Attachment blob **bytes are never returned by the JSON API** — the Memos proto
marks the attachment `content` field `INPUT_ONLY`, so `attachment list` /
`memo attachments <uid>` only print metadata (`name` / `filename` / `type` /
`size`). The raw bytes are served by a separate file-server route,
`/file/attachments/{id}/{filename}`.

To let an agent actually read an image/file, use `attachment pull`:

```bash
memoq attachment pull <memo-uid>          # download all of a memo's attachments
memoq attachment pull <memo-uid> --json   # machine output with local_path per file
memoq attachment pull <memo-uid> --force  # re-download even if a local copy exists
```

It downloads each blob to `$MEMOQ_HOME/attachments/<id>/<filename>`, records the
metadata + absolute local path in the SQLite cache, and prints where each file
landed (`+` downloaded, `=` already present, `-` external link not downloaded).
With `--json`, parse the `local_path` field and Read that path directly. The
bearer token is attached to the download so PRIVATE/PROTECTED attachments work;
externally-linked attachments are recorded but not downloaded.

## Layout

```
main.go                     entry point → cli.Run
internal/config/            config + path resolution
internal/store/             SQLite store, FTS5, migrations, attachment cache
internal/memos/             net/http Memos v1 API client (generic Do + typed helpers)
internal/syncer/            incremental sync engine
internal/cli/               command dispatch, output formatting
internal/cli/resource.go    lark-cli-style resource-group + verb commands (full API)
internal/cli/attachments.go attachment blob download (attachment pull)
skills/memoq/SKILL.md       companion Skill for coding agents
docs/ENDPOINTS.md           canonical endpoint manifest (diff target)
docs/UPSTREAM_SYNC.md       playbook for tracking upstream Memos API changes
```

## Development

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./...
```

Standard library only, except the pure-Go SQLite driver (`modernc.org/sqlite`).
