# Commit log

This document records the purpose, scope, and verification for each Janus
repository commit. Add a new dated section before every future commit.

## 2026-09-16

### Complete Phase 0 contract and Phase 1 qualification

Commit message: `test(phase1): complete phase0 and phase1 qualification`

Scope:

- Completed the Phase 0 repository contract with explicit deployment boundary,
  workload scope, routing precedence, timeout semantics, identity trust,
  middleware ownership, and failure behavior.
- Added the Phase 0 protocol support matrix for path boundaries, escaped paths,
  forwarding headers, unsupported upgrades, unmatched routes, and committed
  versus uncommitted timeout responses.
- Added real-listener coverage for incomplete slow uploads and slow response
  readers, and retained the shorter-parent-deadline middleware contract test.
- Updated the plan to mark Phase 0 and the Phase 1 middleware foundation as
  complete while keeping deployment-specific production sizing as a later
  qualification input.

Verification:

- `go test ./...`
- `go test -count=5 ./internal/gateway ./internal/middleware`
- `go vet ./...`
- `go build -o bin/janus.exe ./cmd/janus`
- `go run ./cmd/janus -check -config configs/janus.json`

Scope note: body-limit, request observation, admission, service middleware,
health, admin, reload, and production qualification remain in later phases.

## 2026-09-16

### Add route-level response buffering

Commit message: `feat(middleware): add route-level response buffering`

Scope:

- Added the reusable middleware chain and the global context-based timeout
  middleware. Routes without an extra policy continue to stream responses.
- Separated the HTTP server `write_timeout` from the overall request deadline,
  preserving write headroom so a timeout response can be emitted before the
  server write budget expires.
- Added named route-level `buffer` middleware with a bounded in-memory
  response body. A buffered route commits its status and body only after the
  backend handler returns; a timed-out backend response becomes `504` before
  commitment.
- Added configuration validation, an example `/buffered` route, unit tests,
  and real-listener tests for streaming, cancellation, partial committed
  responses, and buffered timeout behavior.
- Updated the architecture, protocol, plan, and README documentation.

Verification:

- `go test ./...`
- `go test -count=5 ./internal/gateway ./internal/middleware`
- `go vet ./...`
- `go build -o bin/janus.exe ./cmd/janus`
- `go run ./cmd/janus -check -config configs/janus.json`

Scope note: slow-upload, slow-response-reader, and shorter-parent-deadline
qualification tests remain in Phase 1. Service-level middleware, body-limit
middleware, and full response-writer capability preservation remain planned
for Phase 2.

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
