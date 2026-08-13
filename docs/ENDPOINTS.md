# Endpoint Manifest

The exact Memos v1 REST endpoints `memoq` targets, grouped by resource. This is
the manifest the [Upstream Sync Playbook](./UPSTREAM_SYNC.md) diffs against.
Each row is `memoq command` → `METHOD path`. Path segments in `<angle>` are
substituted from positional args; `:verb` suffixes are literal custom verbs.

Last reconciled against upstream `v0.30.0` proto files.

## memo (`cmdMemoResource`)

| memoq | Method | Path |
|---|---|---|
| `memo list` | GET | `/api/v1/memos` |
| `memo get <uid>` | GET | `/api/v1/memos/<uid>` |
| `memo create` | POST | `/api/v1/memos` |
| `memo update <uid>` | PATCH | `/api/v1/memos/<uid>` |
| `memo delete <uid>` | DELETE | `/api/v1/memos/<uid>` |
| `memo comments <uid>` | GET | `/api/v1/memos/<uid>/comments` |
| `memo comment <uid>` | POST | `/api/v1/memos/<uid>/comments` |
| `memo relations <uid>` | GET | `/api/v1/memos/<uid>/relations` |
| `memo relate <uid> <related-uid>` | GET + PATCH | `/api/v1/memos/<uid>/relations` |
| `memo set-relations <uid>` | PATCH | `/api/v1/memos/<uid>/relations` |
| `memo reactions <uid>` | GET | `/api/v1/memos/<uid>/reactions` |
| `memo react <uid>` | POST | `/api/v1/memos/<uid>/reactions` |
| `memo unreact <uid> <rid>` | DELETE | `/api/v1/memos/<uid>/reactions/<rid>` |
| `memo attachments <uid>` | GET | `/api/v1/memos/<uid>/attachments` |
| `memo set-attachments <uid>` | PATCH | `/api/v1/memos/<uid>/attachments` |
| `memo shares <uid>` | GET | `/api/v1/memos/<uid>/shares` |
| `memo share <uid>` | POST | `/api/v1/memos/<uid>/shares` |
| `memo unshare <uid> <sid>` | DELETE | `/api/v1/memos/<uid>/shares/<sid>` |
| `memo link-metadata` | GET | `/api/v1/memos/-/linkMetadata` |

`memo set-attachments` accepts repeatable
`--attachment attachments/<id>` values and wraps them in the API's required
`attachments` array. The raw `--body` form remains available, including
`--body '{"attachments":[]}'` to clear the complete set.

## attachment (`cmdAttachmentResource`)

| memoq | Method | Path |
|---|---|---|
| `attachment list` | GET | `/api/v1/attachments` |
| `attachment get <id>` | GET | `/api/v1/attachments/<id>` |
| `attachment create` | POST | `/api/v1/attachments` |
| `attachment update <id>` | PATCH | `/api/v1/attachments/<id>` |
| `attachment delete <id>` | DELETE | `/api/v1/attachments/<id>` |
| `attachment batch-delete` | POST | `/api/v1/attachments:batchDelete` |

`attachment create --file <path>` reads the local file and builds the same
JSON attachment body, including base64-encoded `content`, before calling the
listed endpoint.

> `attachment pull <memo-uid>` is not a plain JSON endpoint: it combines
> `GET /api/v1/memos/<uid>/attachments` (metadata) with the file-server route
> `GET /file/attachments/<id>/<filename>` (raw blob) to download attachments to
> `$MEMOQ_HOME/attachments/`. The blob route is served by Echo, not
> grpc-gateway, because the API's attachment `content` field is `INPUT_ONLY`.

## user (`cmdUserResource`)

| memoq | Method | Path |
|---|---|---|
| `user list` | GET | `/api/v1/users` |
| `user get <id>` | GET | `/api/v1/users/<id>` |
| `user create` | POST | `/api/v1/users` |
| `user update <id>` | PATCH | `/api/v1/users/<id>` |
| `user delete <id>` | DELETE | `/api/v1/users/<id>` |
| `user all-stats` | GET | `/api/v1/users:stats` |
| `user stats <id>` | GET | `/api/v1/users/<id>:getStats` |
| `user settings <id>` | GET | `/api/v1/users/<id>/settings` |
| `user setting <id> <key>` | GET | `/api/v1/users/<id>/settings/<key>` |
| `user update-setting <id> <key>` | PATCH | `/api/v1/users/<id>/settings/<key>` |
| `user tokens <id>` | GET | `/api/v1/users/<id>/personalAccessTokens` |
| `user create-token <id>` | POST | `/api/v1/users/<id>/personalAccessTokens` |
| `user delete-token <user> <tid>` | DELETE | `/api/v1/users/<user>/personalAccessTokens/<tid>` |
| `user webhooks <id>` | GET | `/api/v1/users/<id>/webhooks` |

## auth (`cmdAuthResource`)

| memoq | Method | Path |
|---|---|---|
| `auth me` (alias `status`) | GET | `/api/v1/auth/me` |
| `auth signin` | POST | `/api/v1/auth/signin` |
| `auth signout` | POST | `/api/v1/auth/signout` |
| `auth refresh` | POST | `/api/v1/auth/refresh` |

## shortcut (`cmdShortcutResource`)

| memoq | Method | Path |
|---|---|---|
| `shortcut list <user>` | GET | `/api/v1/users/<user>/shortcuts` |
| `shortcut get <user> <sid>` | GET | `/api/v1/users/<user>/shortcuts/<sid>` |
| `shortcut create <user>` | POST | `/api/v1/users/<user>/shortcuts` |
| `shortcut update <user> <sid>` | PATCH | `/api/v1/users/<user>/shortcuts/<sid>` |
| `shortcut delete <user> <sid>` | DELETE | `/api/v1/users/<user>/shortcuts/<sid>` |

## instance (`cmdInstanceResource`)

| memoq | Method | Path |
|---|---|---|
| `instance profile` | GET | `/api/v1/instance/profile` |
| `instance setting` | GET | `/api/v1/instance/settings` |
| `instance setting <key>` | GET | `/api/v1/instance/settings/<key>` |
| `instance update-setting <key>` | PATCH | `/api/v1/instance/settings/<key>` |
| `instance stats` | GET | `/api/v1/instance/stats` |

## ai (`cmdAIResource`)

| memoq | Method | Path |
|---|---|---|
| `ai transcribe` | POST | `/api/v1/ai:transcribe` |

## api (`cmdAPI`)

Generic escape hatch: `memoq api <METHOD> <PATH>` reaches any endpoint, current
or future. A leading `/` is added if omitted. Covers anything not listed above.
