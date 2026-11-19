# Endpoint Manifest

Reconciled against Memos **26.10.0** and **26.11.1** on 2026-11-19.
All 93 proto HTTP bindings have named commands. `<user>` is a username.
`body` below describes the JSON sent on the HTTP wire: a resource means send
its fields directly; `request` means send the full request envelope. Query
fields such as `updateMask`, `pageSize`, and `filter` use `--query`.

Source: upstream [`proto/api/` at 26.11.1](https://github.com/usememos/memos/tree/26.11.1/proto/api),
also compared with [26.10.0](https://github.com/usememos/memos/tree/26.10.0/proto/api).
Compatibility and validation limits are documented in [README.md](../README.md#memos-compatibility).
Regression guard: `TestResourcePaths` holds an independent upstream snapshot.

## Memo

| Command | Method | Path | Body |
|---|---|---|---|
| `memo list` | GET | `/api/memos` | — |
| `memo get <uid>` | GET | `/api/memos/<uid>` | — |
| `memo create` | POST | `/api/memos` | Memo |
| `memo update <uid>` | PATCH | `/api/memos/<uid>` | Memo |
| `memo delete <uid>` | DELETE | `/api/memos/<uid>` | — |
| `memo comments <uid>` | GET | `/api/memos/<uid>/comments` | — |
| `memo comment <uid>` | POST | `/api/memos/<uid>/comments` | Memo |
| `memo relations <uid>` | GET | `/api/memos/<uid>/relations` | — |
| `memo set-relations <uid>` | PATCH | `/api/memos/<uid>/relations` | request |
| `memo reactions <uid>` | GET | `/api/memos/<uid>/reactions` | — |
| `memo react <uid>` | POST | `/api/memos/<uid>/reactions` | `{"reaction":{"reactionType":"👍"}}` |
| `memo unreact <uid> <rid>` | DELETE | `/api/memos/<uid>/reactions/<rid>` | — |
| `memo attachments <uid>` | GET | `/api/memos/<uid>/attachments` | — |
| `memo set-attachments <uid>` | PATCH | `/api/memos/<uid>/attachments` | `{"attachments":[{"name":"attachments/<id>"}]}` |
| `memo shares <uid>` | GET | `/api/memos/<uid>/shares` | — |
| `memo share <uid>` | POST | `/api/memos/<uid>/shares` | MemoShare |
| `memo unshare <uid> <sid>` | DELETE | `/api/memos/<uid>/shares/<sid>` | — |
| `memo shared <token>` | GET | `/api/shares/<token>/memo` | — |
| `memo link-metadata` | GET | `/api/memos/-/linkMetadata` | query `url` |
| `memo batch-link-metadata` | POST | `/api/memos/-/linkMetadata:batchGet` | `{"urls":["https://example.com"]}` |

`memo relate <uid> <related-uid>` combines relation GET/PATCH, preserving outgoing
references and excluding incoming references and immutable comments.
`set-relations` replaces outgoing references; `set-attachments` replaces the
complete attachment set. Repeated `--attachment attachments/<id>` builds that
envelope. An empty array clears the set.

## Attachment

| Command | Method | Path | Body |
|---|---|---|---|
| `attachment list` | GET | `/api/attachments` | — |
| `attachment get <id>` | GET | `/api/attachments/<id>` | — |
| `attachment create` | POST | `/api/attachments` | Attachment (inline base64 or external link) |
| `attachment update <id>` | PATCH | `/api/attachments/<id>` | Attachment |
| `attachment delete <id>` | DELETE | `/api/attachments/<id>` | — |
| `attachment batch-delete` | POST | `/api/attachments:batchDelete` | `{"names":["attachments/<id>"]}` |
| `attachment upload` | POST | `/api/attachments:upload` | request |

`attachment create --file <path>` and `attachment upload --file <path>` use
the chunk-upload RPC, with optional `--memo <uid>`. `--replace <id> --yes`
requires 26.11.1+; the client checks the profile before uploading.
`attachment pull <memo-uid>` combines attachment metadata with streaming
`GET /file/attachments/<id>/<filename>` and records local paths in the cache.

## User

| Command | Method | Path | Body |
|---|---|---|---|
| `user list` | GET | `/api/users` | — |
| `user batch-get` | POST | `/api/users:batchGet` | `{"names":["users/alice"]}` |
| `user get <user>` | GET | `/api/users/<user>` | — |
| `user create` | POST | `/api/users` | User |
| `user update <user>` | PATCH | `/api/users/<user>` | User |
| `user delete <user>` | DELETE | `/api/users/<user>` | — |
| `user list-stats <user>` | GET | `/api/users/<user>/stats` | — |
| `user stats <user>` | GET | `/api/users/<user>:getStats` | — |
| `user usage <user>` | GET | `/api/users/<user>/usage` | — |
| `user settings <user>` | GET | `/api/users/<user>/settings` | — |
| `user setting <user> <key>` | GET | `/api/users/<user>/settings/<key>` | — |
| `user update-setting <user> <key>` | PATCH | `/api/users/<user>/settings/<key>` | UserSetting |
| `user tokens <user>` | GET | `/api/users/<user>/personalAccessTokens` | — |
| `user create-token <user>` | POST | `/api/users/<user>/personalAccessTokens` | request |
| `user delete-token <user> <id>` | DELETE | `/api/users/<user>/personalAccessTokens/<id>` | — |
| `user webhooks <user>` | GET | `/api/users/<user>/webhooks` | — |
| `user create-webhook <user>` | POST | `/api/users/<user>/webhooks` | UserWebhook |
| `user update-webhook <user> <id>` | PATCH | `/api/users/<user>/webhooks/<id>` | UserWebhook |
| `user delete-webhook <user> <id>` | DELETE | `/api/users/<user>/webhooks/<id>` | — |
| `user webhook-secret <user> <id>` | GET | `/api/users/<user>/webhooks/<id>:getSigningSecret` | — |
| `user notifications <user>` | GET | `/api/users/<user>/notifications` | — |
| `user update-notification <user> <id>` | PATCH | `/api/users/<user>/notifications/<id>` | UserNotification |
| `user delete-notification <user> <id>` | DELETE | `/api/users/<user>/notifications/<id>` | — |
| `user identities <user>` | GET | `/api/users/<user>/linkedIdentities` | — |
| `user identity <user> <idp>` | GET | `/api/users/<user>/linkedIdentities/<idp>` | — |
| `user link-identity <user>` | POST | `/api/users/<user>/linkedIdentities` | request |
| `user unlink-identity <user> <idp>` | DELETE | `/api/users/<user>/linkedIdentities/<idp>` | — |
| `user import-memos <user>` | POST | `/api/users/<user>:importMemos` | request |

`user all-stats` uses the `ListUserStats` wildcard `/api/users/-/stats`.
`user invitations <user>` is listed under Space below.
`user export-memos <user> --output <zip>` streams the non-proto, admin-only
`GET /file/users/<user>/export`. `import-memos --file <zip>` performs the chunk
protocol; `--validate-only` returns a plan, otherwise `--yes` is required.
`--conflict-policy` accepts SKIP (default), REPLACE, or DUPLICATE. Inspect the
returned report's `failed` and `failures` fields for partial imports.

## View

| Command | Method | Path | Body |
|---|---|---|---|
| `view list <user>` | GET | `/api/users/<user>/views` | — |
| `view get <user> <id>` | GET | `/api/users/<user>/views/<id>` | — |
| `view create <user>` | POST | `/api/users/<user>/views` | MemoView |
| `view update <user> <id>` | PATCH | `/api/users/<user>/views/<id>` | MemoView |
| `view delete <user> <id>` | DELETE | `/api/users/<user>/views/<id>` | — |

## Space

| Command | Method | Path | Body |
|---|---|---|---|
| `space list` | GET | `/api/spaces` | — |
| `space get <space>` | GET | `/api/spaces/<space>` | — |
| `space create` | POST | `/api/spaces` | Space |
| `space update <space>` | PATCH | `/api/spaces/<space>` | Space |
| `space delete <space>` | DELETE | `/api/spaces/<space>` | — |
| `space members <space>` | GET | `/api/spaces/<space>/members` | — |
| `space member <space> <user>` | GET | `/api/spaces/<space>/members/<user>` | — |
| `space update-member <space> <user>` | PATCH | `/api/spaces/<space>/members/<user>` | SpaceMember |
| `space remove-member <space> <user>` | DELETE | `/api/spaces/<space>/members/<user>` | — |
| `space invitations <space>` | GET | `/api/spaces/<space>/invitations` | — |
| `user invitations <user>` | GET | `/api/users/<user>/spaceInvitations` | — |
| `space invitation <space> <user>` | GET | `/api/spaces/<space>/invitations/<user>` | — |
| `space invite <space>` | POST | `/api/spaces/<space>/invitations` | SpaceInvitation |
| `space revoke-invitation <space> <user>` | DELETE | `/api/spaces/<space>/invitations/<user>` | — |
| `space accept-invitation <space> <user>` | POST | `/api/spaces/<space>/invitations/<user>:accept` | request |
| `space decline-invitation <space> <user>` | POST | `/api/spaces/<space>/invitations/<user>:decline` | request |

## Auth, Identity Provider, Instance, AI

| Command | Method | Path | Body |
|---|---|---|---|
| `auth me` (alias `status`) | GET | `/api/auth/me` | — |
| `auth signin` | POST | `/api/auth/signin` | `{"passwordCredentials":{"username":"alice","password":"..."}}` or `ssoCredentials` |
| `auth refresh` | POST | `/api/auth/refresh` | request (refresh cookie supplied automatically) |
| `auth signout` | POST | `/api/auth/signout` | — |
| `idp list` | GET | `/api/identity-providers` | — |
| `idp get <id>` | GET | `/api/identity-providers/<id>` | — |
| `idp create` | POST | `/api/identity-providers` | IdentityProvider |
| `idp update <id>` | PATCH | `/api/identity-providers/<id>` | IdentityProvider |
| `idp delete <id>` | DELETE | `/api/identity-providers/<id>` | — |
| `instance profile` | GET | `/api/instance/profile` | — |
| `instance setting <key>` | GET | `/api/instance/settings/<key>` | — |
| `instance batch-settings` | POST | `/api/instance/settings:batchGet` | `{"names":["instance/settings/GENERAL"]}` |
| `instance update-setting <key>` | PATCH | `/api/instance/settings/<key>` | InstanceSetting |
| `instance test-email` | POST | `/api/instance/settings/notification:testEmail` | request |
| `instance stats` | GET | `/api/instance/stats` | — |
| `instance configuration` | GET | `/api/instance/configuration` | — |
| `ai transcribe` | POST | `/api/ai:transcribe` | request (inline base64 audio) |

`MEMO_RELATED` is removed in 26.11.1. Declarative configuration validation,
planning, and application use the server's `memos config` CLI, not a remote RPC.

## Generic HTTP and SSE

`memoq api <METHOD> <PATH>` sends arbitrary JSON requests under the configured
server root. `--output <path>` and `--stream` require GET. Downloads are written
through a temporary file and renamed only after a successful transfer.
`memoq api GET /api/sse --stream` streams events until interrupted.
