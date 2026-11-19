# Upstream Sync Playbook

Compatibility baseline: Memos **26.10.0**; latest reviewed: **26.11.1**.
Use pinned release tags, without a `v` prefix for 26.xx.

## Source of truth

Inspect `proto/api/*_service.proto` at the minimum and latest refs, together
with the corresponding server handlers and route registration. REST paths
start with `/api`, independent of protobuf package naming. The old
`proto/api/v1` directory and `/api/v1` URLs describe earlier releases.

Eight services currently expose 93 HTTP bindings:

| Service | RPCs | CLI groups |
|---|---:|---|
| MemoService | 20 | memo |
| AttachmentService | 7 | attachment |
| UserService | 33 | user, view |
| AuthService | 4 | auth |
| InstanceService | 7 | instance |
| SpaceService | 16 | space, user invitations |
| IdentityProviderService | 5 | idp |
| AIService | 1 | ai |

Also inspect non-proto routes: SSE `/api/sse`, attachment/avatar file downloads,
and the admin-only ZIP export `/file/users/<username>/export`.

## Release workflow

1. Pin the minimum and newest supported upstream tags. Retrieve their service
   directory listings and sources using `gh api`, so newly added services are
   discovered rather than omitted by a fixed old list.
2. Extract every `google.api.http` binding, including additional bindings.
   Compare RPC names, methods, expanded paths, JSON body selectors, request and
   response fields, enums, resource identifiers, and pagination behavior.
3. Inspect implementation semantics where they affect client workflows:
   authorization, quotas, destructive replacement, comments, upload
   acknowledgments, and binary/streaming routes. Matching endpoint counts alone
   do not establish compatibility.
4. Update the table in `internal/cli/resource.go`, typed helpers in
   `internal/memos`, and the cache behavior only where the new contract requires
   it. Keep raw JSON available for resource fields outside the cache projection.
5. Update the independent pinned HTTP snapshot in `TestResourcePaths` from
   upstream sources. Reconcile [ENDPOINTS.md](ENDPOINTS.md), README, the bundled
   memoq Skill, and [CHANGELOG.md](../CHANGELOG.md).
6. Run build, vet, tests, and race tests. Start isolated real server binaries
   for both minimum and newest versions; verify actual bodies, auth refresh,
   lifecycle transitions, import/export, and file integrity. Record which
   optional external integrations were exercised and which were not.

```bash
gh api 'repos/usememos/memos/contents/proto/api?ref=26.11.1' \
  --jq '.[] | select(.name | endswith("_service.proto")) | .name'
gh api 'repos/usememos/memos/contents/proto/api/auth_service.proto?ref=26.11.1' \
  --jq '.content' | base64 --decode
go build ./...
go vet ./...
go test ./...
go test -race ./...
git diff --check
```

## Migration lessons

- **Paths:** 26.10.0 and 26.11.1 reject `/api/v1` with HTTP 410. Views replace
  shortcuts; user identifiers are usernames; all-user stats use `users/-/stats`;
  instance settings collection GET is replaced by `settings:batchGet`.
- **Bodies:** the HTTP `body` selector controls whether to send resource fields
  directly or a request envelope. Reactions need `reaction`, while comments
  send a Memo directly. Link metadata uses the `url` query field.
- **Relations:** listing returns incoming/outgoing references and immutable
  comments. Additive writes preserve only outgoing REFERENCE entries.
- **Sessions:** these REST gateways expose cookies through
  `Grpc-Metadata-Set-Cookie` and read them through `Grpc-Metadata-Cookie`.
  Support those headers alongside standard cookies; validate rotation against
  real servers. Scope saved credentials to the server and let an explicit PAT
  take precedence.
- **Transfers:** chunk RPCs use a spec, upload ID, offset, bounded base64 data,
  and a finishing call. Verify upload ID and committed size on each response.
  Downloads/export/SSE stream without buffering the entire response.
- **Version gates:** `replaceAttachment` exists only in 26.11.1+. Older servers
  discard unknown fields, so replacement must be gated before starting an
  upload. `MEMO_RELATED` is removed in 26.11.1.
- **Cache scope:** cache active non-comment memos; use direct resource commands
  for archived memos, comments, space/creator metadata, and server filters.
  Warn when a completed remote write cannot refresh the cache, so callers
  do not retry a successful write.
