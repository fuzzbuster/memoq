# Upstream API Sync Playbook

How to keep `memoq` in lockstep with the upstream Memos server API
([github.com/usememos/memos](https://github.com/usememos/memos)) with minimal
effort and no guesswork.

This doc exists because the Memos v1 API **does drift between releases** — we
already absorbed one round of renames (see [History](#history)). The workflow
below turns "did upstream change something?" from an archaeology project into a
mechanical, ~15-minute diff.

---

## 1. Source of truth: the `.proto` files, not the docs

Memos serves its REST API through a **grpc-gateway** generated from the protobuf
service definitions in [`proto/api/v1/`](https://github.com/usememos/memos/tree/main/proto/api/v1).
Every HTTP method + path is declared inline via `google.api.http` annotations:

```proto
rpc ListMemos(ListMemosRequest) returns (ListMemosResponse) {
  option (google.api.http) = {
    get: "/api/v1/memos"
  };
}

rpc DeleteMemoReaction(DeleteMemoReactionRequest) returns (google.protobuf.Empty) {
  option (google.api.http) = {
    delete: "/api/v1/{name=memos/*/reactions/*}"
  };
}
```

**Rules that follow from this:**

- The proto annotations are authoritative. Prose docs, blog posts, and the
  OpenAPI JSON lag behind and are sometimes wrong. Diff the proto.
- Path templates like `{name=memos/*/reactions/*}` mean the resource name is
  substituted into the path. In `memoq` we build the concrete path from the
  positional args (e.g. `unreact <memoUID> <reactionID>` →
  `/api/v1/memos/<uid>/reactions/<rid>`).
- **Custom verbs** appear as a `:` suffix (`/api/v1/users:stats`,
  `/api/v1/{name=users/*}:getStats`, `/api/v1/ai:transcribe`). These are NOT a
  slash path — the colon is literal. Preserve it exactly.

---

## 2. The relevant service files

Each Memos service maps to one `memoq` resource group. Watch these files:

| Proto file (`proto/api/v1/`)   | memoq resource group | Handler in `internal/cli/resource.go` |
|--------------------------------|----------------------|----------------------------------------|
| `memo_service.proto`           | `memo`               | `cmdMemoResource`        |
| `attachment_service.proto`     | `attachment`         | `cmdAttachmentResource`  |
| `user_service.proto`           | `user`               | `cmdUserResource`        |
| `auth_service.proto`           | `auth`               | `cmdAuthResource`        |
| `shortcut_service.proto`       | `shortcut`           | `cmdShortcutResource`    |
| `instance_service.proto` / `workspace_*` | `instance` | `cmdInstanceResource`    |
| `markdown_service.proto` / AI  | `ai`                 | `cmdAIResource`          |

> The client's typed hot-path (`internal/memos/client.go`) only depends on the
> **memo** endpoints (list/get/create/update/delete) plus `auth/me` for `Ping`.
> Everything else rides on the generic `api`/resource layer, so most upstream
> churn touches only `resource.go`.

The canonical endpoint list `memoq` currently targets lives in
[`ENDPOINTS.md`](./ENDPOINTS.md) — that is the manifest you diff against.

---

## 3. The sync workflow (do this each upstream release)

### Step 0 — Pin the upstream ref you're comparing to
```bash
UPSTREAM=https://raw.githubusercontent.com/usememos/memos/main/proto/api/v1
# or pin a tag, e.g. .../refs/tags/v0.24.0/proto/api/v1
```

### Step 1 — Fetch the proto files
```bash
mkdir -p /tmp/memos-proto && cd /tmp/memos-proto
for f in memo_service attachment_service user_service auth_service \
         shortcut_service instance_service markdown_service; do
  curl -fsSL "$UPSTREAM/$f.proto" -o "$f.proto" || echo "MISSING: $f (renamed?)"
done
```
A `MISSING` line is itself a signal: a service file was **renamed or split**
(this is exactly how `workspace_service` → `instance_service` surfaced).

### Step 2 — Extract every HTTP mapping
Pull the method + path from every `google.api.http` annotation:
```bash
grep -nE '(get|post|put|patch|delete):[[:space:]]*"' *.proto \
  | sed -E 's/.*(get|post|put|patch|delete):[[:space:]]*"([^"]+)".*/\U\1\E \2/' \
  | sort -u
```
This prints a clean `METHOD /api/v1/...` list — the current upstream surface.

### Step 3 — Diff against our manifest
```bash
# ENDPOINTS.md keeps one `METHOD /path` per row in a fenced block; extract & diff
diff <(sort -u our_endpoints.txt) <(sort -u upstream_endpoints.txt)
```
- Lines only in **upstream** → new/renamed endpoints to add.
- Lines only in **ours** → removed/renamed endpoints to drop or remap.

### Step 4 — Reconcile the code
For each delta:
1. Update the verb's path in the matching `cmd*Resource` handler in
   `internal/cli/resource.go`.
2. Update the verb list in that handler's `resourceUsage(...)` call **and** the
   help block in `internal/cli/cli.go`.
3. If a typed client method (`internal/memos/client.go`) is affected (memo CRUD
   or `Ping`), update it too.
4. Update [`ENDPOINTS.md`](./ENDPOINTS.md), `README.md`, and
   `skills/memoq/SKILL.md` command tables.

### Step 5 — Prove it with the path tests
`internal/cli/resource_test.go` has a table asserting the exact
`METHOD + path` each verb constructs (via a stubbed `runAPI`). **Add or edit a
row there for every change**, then:
```bash
go test ./... && go vet ./... && gofmt -l .
```
The path tests are the safety net: they fail loudly if a rename regresses, with
no live server required.

---

## 4. Why we rarely have to do more than edit paths

`memoq` is deliberately built so upstream drift has a small blast radius:

- **One generic primitive.** Every resource verb funnels through
  `Client.Do(ctx, method, path, body) → json.RawMessage`. New fields in a
  request/response body need *no* code change — `--field`, `--body`, and raw
  JSON output pass them through untouched.
- **The `api` escape hatch.** `memoq api <METHOD> <PATH>` reaches any endpoint,
  current or future, even before we add a named verb. Users are never blocked
  waiting on a release.
- **Typed layer is minimal.** Only the memo hot-path is typed; that surface is
  the most stable part of the Memos API.

So in practice a sync is: diff proto → fix a handful of path strings → update a
few test rows and doc tables. New *capabilities* (new services) are the only
thing that need genuinely new handlers.

---

## 5. Optional: make it a CI guard

Add a scheduled job (weekly / on release tag) that runs Steps 1–3 and fails if
the diff is non-empty, posting the delta. Sketch:

```bash
#!/usr/bin/env bash
set -euo pipefail
UPSTREAM=https://raw.githubusercontent.com/usememos/memos/main/proto/api/v1
# ... fetch + extract (Steps 1-2) into upstream_endpoints.txt ...
if ! diff -u docs/endpoints.generated.txt upstream_endpoints.txt; then
  echo "::warning::Memos upstream API drifted — run the sync playbook (docs/UPSTREAM_SYNC.md)"
  exit 1
fi
```

Keep `docs/endpoints.generated.txt` as the committed snapshot the job diffs
against; update it in the same PR that reconciles the code.

> Note: this repo's sandbox must never bind a network port. The CI guard is
> pull-only (`curl` + `diff`); it does not start a server.

---

## History

The first sync after adopting this workflow corrected a batch of upstream
renames that had accumulated:

| Was (older API)                     | Now (`main`)                                  |
|-------------------------------------|-----------------------------------------------|
| `/api/v1/resources`                 | `/api/v1/attachments`                         |
| `/api/v1/workspace/profile`         | `/api/v1/instance/profile`                    |
| `/api/v1/workspace/settings/*`      | `/api/v1/instance/settings/*`                 |
| `/api/v1/auth/status`               | `/api/v1/auth/me`                             |
| user `accessTokens`                 | user `personalAccessTokens`                   |
| flat user settings                  | nested `users/*/settings/{key}`               |
| user stats (ad hoc)                 | custom verbs `users:stats` + `users/*:getStats` |
| `/api/v1/ai/transcribe`             | `/api/v1/ai:transcribe` (custom verb)         |

New endpoints added in the same pass: memo `shares`/`share`/`unshare`,
`link-metadata`; attachment `batch-delete`; auth `refresh`; instance `stats`.
