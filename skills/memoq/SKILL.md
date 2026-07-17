---
name: memoq
description: >
  Query and manage a personal Memos knowledge base from the command line via the
  `memoq` CLI — a non-interactive, scriptable tool built for coding agents (no TUI,
  no chat/MCP/LLM subcommands). Use when the user asks to search / list / read /
  create / update / delete their notes or memos, look up something they "wrote down
  before", or when you need to persist a note for later. Every read command supports
  `--json` for machine parsing and auto-syncs from the remote Memos server so results
  are fresh. Trigger on: "search my notes", "查一下我记的笔记/备忘", "记一条备忘",
  "把这个存到 memos", "list my memos with tag X".
---

# memoq — Memos CLI for coding agents

`memoq` is a slim, non-interactive CLI over a personal [Memos](https://github.com/usememos/memos)
server. It keeps a local SQLite cache (with FTS5 full-text search) and lazily syncs
from the remote server on read, so queries are fast and always reasonably fresh.

**It is script-only.** There is no interactive prompt, no TUI, no chat/agent loop, and
no MCP server. Never wait for user input from a `memoq` process — every command runs to
completion and exits. Always add `--json` when you intend to parse the output.

## Prerequisites (one-time)

The binary must be on `PATH` (build with `go build -o memoq .` inside the project, then
move it somewhere on `PATH`). Before first use, configure the server and token:

```bash
memoq config set server_url https://memos.example.com
memoq config set token <personal-access-token>
memoq config set auto_sync_ttl_seconds 30   # optional; 0 disables read-time auto-sync
```

- `memoq config path` prints the config file and DB file locations.
- `memoq config list` dumps the current config as JSON (the token is masked).
- Data lives under `$MEMOQ_HOME` (default `~/.memoq`).

If a read command prints `memoq: auto-sync skipped (...)` to **stderr**, that is
non-fatal: the network/auth failed but the local cache is still served. Only treat a
non-zero exit code as a real failure.

## Reading notes

All read commands accept `--json` (machine output) and `--no-sync` (use local cache
as-is, skip the read-time sync). Flags may appear before, after, or between positional
args.

### Search (full-text)

```bash
memoq search "deployment checklist" --json --limit 10
```

- Uses SQLite FTS5, with a LIKE substring fallback. **CJK / Chinese** text tokenizes
  poorly under FTS5, so the LIKE fallback carries it — search with a concrete substring
  (e.g. `memoq search "同步" --json`) rather than a full sentence.
- On `(no matches; try different keywords)`, retry with fewer or different keywords, a
  shorter substring, or switch to `list --tag`.

### List (structured filters)

```bash
memoq list --json --limit 50
memoq list --tag project --from 2026-01-01 --to 2026-06-30 --json
memoq list --visibility PUBLIC --offset 50 --json
```

Filters: `--tag` (without the `#`), `--from` / `--to` (`YYYY-MM-DD`, `--to` is
inclusive to end-of-day), `--visibility` (`PUBLIC` / `PRIVATE` / `PROTECTED`),
`--limit`, `--offset`. Results are pinned-first, then newest-first.

### Get one note

```bash
memoq get <uid> --json
```

If it prints `no memo with uid ... (try 'memoq sync')`, run `memoq sync` first — the
note may be newer than the local cache.

### Stats

```bash
memoq stats --json
```

Returns totals, pinned count, newest/oldest timestamps, last sync time, and per-tag
counts. Useful to discover which tags exist before a `list --tag`.

## Writing notes

Write commands hit the remote server directly and then reflect the change into the local
cache immediately (no wait for the next sync).

### Create

```bash
memoq create --content "Ship notes: cut RC on Friday" --tag release --tag ops --visibility PRIVATE
# or pipe the body via stdin:
echo "multi-line body" | memoq create --tag inbox
```

- Body comes from `--content`, else from stdin. Empty body is rejected.
- `--tag` is repeatable; each is appended to the body as a Memos-style `#hashtag` so the
  server indexes it. `--visibility` defaults to `PRIVATE`.
- Prints `created <uid>` (or `{"uid": "..."}` with `--json`).

### Update

```bash
memoq update <uid> --content "revised body" --visibility PROTECTED
```

Pass at least one of `--content` / `--visibility`.

### Delete

```bash
memoq delete <uid>
```

## Attachments (download blobs so you can read images/files)

Memos attachments have their **metadata** in the JSON API but their **bytes**
are NOT returned there — the API's attachment `content` field is `INPUT_ONLY`.
So `memoq attachment list` and `memoq memo attachments <uid>` only give you
`name` / `filename` / `type` / `size`, never the file contents.

To actually read an attachment (e.g. to recognize an image), download it first:

```bash
memoq attachment pull <memo-uid>          # download every attachment of a memo
memoq attachment pull <memo-uid> --json   # machine output: local_path per file
memoq attachment pull <memo-uid> --force  # re-download even if a copy exists
```

`attachment pull`:
- lists the memo's attachments, then fetches each blob from the file-server
  route `/file/attachments/{id}/{filename}` (bearer-token attached, so PRIVATE /
  PROTECTED attachments work);
- writes each file to `$MEMOQ_HOME/attachments/<id>/<filename>` (default
  `~/.memoq/attachments/...`);
- caches the metadata + absolute `local_path` in SQLite (the `attachments`
  table);
- prints the absolute local path of each file (`+` downloaded, `=` already
  present, `-` external link not downloaded).

With `--json`, parse the `local_path` field and then Read that path to inspect
or recognize the image/file. Externally-hosted attachments (non-empty
`external_link`) are recorded but not downloaded — fetch those from their link.

## Force sync

```bash
memoq sync --json
```

Runs a full incremental reconcile (added / updated / deleted / unchanged) against the
remote server. Read commands auto-sync per the TTL, so an explicit `sync` is only needed
after `--no-sync` usage, when a `get` misses, or to guarantee freshness before a report.

## Full-API resource commands

Beyond the fast local-cache commands above, `memoq` exposes the **entire** Memos v1 REST
API through a lark-cli-style `resource-group + verb` surface. These commands talk to the
server directly and print the pretty-printed JSON response verbatim — use them when the
cache-backed commands don't cover what you need (comments, reactions, attachments, users,
shortcuts, instance settings, AI transcription, etc.).

Body / query flags shared by every resource verb:

- `--field k=v` — a body field, repeatable. The value is parsed as JSON when possible
  (`--field pinned=true`, `--field priority=3`) and falls back to a string otherwise
  (`--field content='hello world'`).
- `--body '{...}'` — a raw JSON body (wins over `--field`).
- `--body-file <path|->` — read the JSON body from a file, or `-` for stdin.
- `--query k=v` — a URL query parameter, repeatable (pagination, filters, updateMask).

### Resource groups and verbs

```bash
memoq memo list --query pageSize=10 --query state=NORMAL
memoq memo get <uid>
memoq memo create --field content='hi' --field visibility=PRIVATE
memoq memo update <uid> --field pinned=true --query updateMask=pinned
memoq memo delete <uid>
memoq memo comments <uid>
memoq memo comment <uid> --field content='a reply'
memoq memo relations <uid>
memoq memo reactions <uid>
memoq memo react <uid> --field reactionType=THUMBS_UP
memoq memo unreact <uid> <reactionID>
memoq memo shares <uid>
memoq memo share <uid> --body '{}'

memoq attachment list
memoq attachment get <id>
memoq attachment batch-delete --body '{"names":["attachments/1"]}'
memoq attachment pull <memo-uid>   # download blobs locally (see Attachments below)

memoq user list
memoq user get me            # 'me' resolves to the authenticated user
memoq user stats <id>        # per-user stats (custom verb :getStats)
memoq user all-stats         # stats across all users (users:stats)
memoq user settings <id>     # list a user's settings
memoq user setting <id> <key>
memoq user tokens <id>       # personal access tokens

memoq auth me                # who am I / is the token valid
memoq shortcut list <user>
memoq instance profile       # server version + config
memoq instance setting
memoq instance stats
memoq ai transcribe --field <...>
```

Run any resource group with no verb (e.g. `memoq user`) to print its verb list.

> **Endpoint reference:** the exact method+path each verb targets is documented
> in `docs/ENDPOINTS.md`; the process for tracking upstream API changes is in
> `docs/UPSTREAM_SYNC.md`.

### Generic escape hatch — guaranteed full coverage

For any endpoint not wrapped by a named verb (or added by a newer server), use `api`:

```bash
memoq api GET  /api/v1/memos --query pageSize=10
memoq api POST /api/v1/memos --field content='hello' --field visibility=PRIVATE
memoq api PATCH /api/v1/memos/<uid> --body '{"pinned":true}' --query updateMask=pinned
memoq api DELETE /api/v1/memos/<uid>
```

`api <METHOD> <PATH>` reaches every current or future endpoint. Prefer the named verbs
for readability; fall back to `api` for anything exotic.

**Important:** unlike the fast read commands, resource/`api` commands do **not** touch the
local cache — they neither auto-sync nor reflect their writes into it. After a mutating
resource command that you also want reflected locally, run `memoq sync`.

## Agent guidance

1. **Prefer `--json`** whenever you will parse the result; parse it, don't screen-scrape
   the plain-text layout.
2. **Discover before filtering**: `stats --json` reveals available tags; then `list --tag`.
3. **CJK searches**: use a short substring and lean on the LIKE fallback; if a full-text
   `search` misses, fall back to `list` + tag/date filters.
4. **No-match is normal**, not an error — retry with different keywords before giving up.
5. **Never** expect interactivity: no command prompts, so never leave one running for
   input. Chain commands normally in scripts.
6. **Freshness**: results are auto-synced; if you disabled sync with `--no-sync` or a
   `get` misses a just-created note, run `memoq sync` and retry.
7. **Pick the right tier**: for ordinary note search/list/read/write, use the fast
   local-cache commands (they auto-sync and are cache-backed). For anything the cache
   doesn't model — comments, reactions, attachments, users, shortcuts, instance settings,
   AI transcription — use the resource commands (`memoq <group> <verb>`), and for exotic or
   newer endpoints use `memoq api <METHOD> <PATH>`. Resource/`api` output is raw JSON;
   parse it directly.
