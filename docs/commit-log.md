# Commit log

This document records the purpose, scope, and verification for each Janus
repository commit. Add a new dated section before every future commit.

## 2026-09-16

### Make server and transport budgets configurable

Commit message: `feat(config): make server and transport budgets configurable`

Scope:

- Added typed `Settings` configuration with defaults, duration-string parsing,
  and minimum/maximum validation for request, server, backend, capacity, SLO,
  and trust settings.
- Replaced hard-coded HTTP server and backend transport constants with values
  from validated settings. Existing configurations without a `settings` block
  continue to use the starter defaults.
- Separated inbound header and request-read budgets from the active overall
  request deadline. The overall deadline is attached to the request context and
  cancels backend work when it expires.
- Added a complete settings example in `configs/janus.json`.
- Added tests for settings defaults, duration parsing, validation boundaries,
  server and transport wiring, and backend cancellation.
- Updated the plan, protocol guide, and README to describe the new behavior.

Verification:

- `go test ./...`
- `go vet ./...`
- `go build -o bin/janus.exe ./cmd/janus`
- `go run ./cmd/janus -check -config configs/janus.json`

Scope note: `max_body_bytes` is now validated and documented, but request-body
size enforcement remains the next implementation task in `docs/plan.md`.

## 2026-09-16

### Remove configuration without runtime consumers

Commit message: `refactor(config): remove unused settings`

Scope:

- Removed body-limit, capacity-planning, SLO-planning, and trusted-proxy fields
  that were declared but not consumed by the current runtime.
- Kept only request/server/backend settings that currently configure HTTP server
  deadlines or the outbound transport and connection pool.
- Updated the sample configuration, configuration tests, and delivery plan to
  match the reduced schema.
- Added explanatory Mandarin notes for the current Go transport and server
  timeout behavior.

Verification:

- `go test ./...`
- `go vet ./...`
- `go build -o bin/janus.exe ./cmd/janus`
- `go run ./cmd/janus -check -config configs/janus.json`

Design note: future middleware composition in the style of Traefik is recorded
as a direction for a later change; this commit does not implement it.
