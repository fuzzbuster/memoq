# Changelog

## [0.5.0] - 2026-11-19

### Breaking changes

- Require Memos **26.10.0 or later**, with 26.11.1 also verified. Earlier Memos
  versions are no longer supported.
- Migrate REST paths from `/api/v1/` to `/api/`. Update paths passed to `memoq api`.
- Replace `shortcut` commands with `view` commands. User resource IDs are
  usernames; use `auth me` for the current user.
- Replace `instance setting` without a key with `instance batch-settings`.
  All-user statistics now use `/api/users/-/stats`.

### Added

- Named commands for all 93 upstream RPCs, including spaces, members,
  invitations, memo views, identity providers, notifications, linked identities,
  usage, webhook management, shared memos, and batch link metadata.
- Persistent sign-in sessions with refresh-cookie rotation, server scoping,
  private file permissions, and explicit personal-access-token precedence.
- Bounded chunk uploads for `attachment create/upload --file` and
  `create --attach`. Attachment content replacement with `--replace --yes`
  requires Memos 26.11.1 or later.
- ZIP export and chunk import with validation plans and SKIP, REPLACE, or
  DUPLICATE conflict policies.
- Streaming GET output with `--output` and `--stream`, including SSE events.

### Fixed

- Preserve outgoing memo references without replaying incoming references or
  immutable comments, and reject pagination cycles.
- Read and send refresh cookies through the Memos REST gateway's metadata
  headers, so refresh and sign-out work across CLI invocations.
- Reject attachment replacement on older servers before uploading, preventing
  unknown fields from silently turning replacement into creation.
- Verify chunk acknowledgments and final results; commit downloads only after a
  successful transfer. Keep completed remote writes successful if cache sync fails.

### Validation

- Build, vet, unit tests, and race tests passed.
- Core workflows passed against isolated real Memos 26.10.0 and 26.11.1 servers,
  including session rotation, spaces, relations, attachments, import/export,
  link metadata, and SSE.
- Both servers passed 1 GiB upload/download with matching SHA-256 after
  temporarily raising their test upload quotas.
- Real AI, SMTP, and external SSO integrations remain unverified without provider
  credentials; the local cache remains an active, non-comment memo projection.

[0.5.0]: https://github.com/fuzzbuster/memoq/compare/v0.4.1...v0.5.0
