# Commit log

This document records the purpose, scope, and verification for each Janus
repository commit. Add a new dated section before every future commit.

## 2026-09-16

### Add bounded request-body middleware

Commit message: `feat(policy): add route and service body limits`

Scope:

- Added typed `body_limit` definitions with strict validation and ordered
  middleware references on both routes and services.
- Rejected known oversized request bodies before backend forwarding and bounded
  chunked/unknown-length bodies while they are read; proxy errors map to 413.
- Composed route and service limits by nesting standard-library readers, giving
  the request the smallest applicable cap without adding retries.
- Added runnable configuration, unit/integration regression tests, and updated
  the Phase 2F capability documentation.

Verification:

- `gofmt` on touched Go files
- `go test ./...`

Scope note: request IDs, access observation, global/service admission, health,
metrics, and production qualification remain future work.

### Add SSE and classic HTTP/1 WebSocket routes

Commit message: `feat(protocol): support SSE and WebSocket forwarding`

Scope:

- Added explicit route protocol modes: `http`, `sse`, and `websocket`; routes
  without a mode remain ordinary HTTP routes by default.
- Added SSE streaming through the existing reverse proxy, including immediate
  event flushing and bypasses for finite API timeout, write deadline, and route
  buffering.
- Enabled classic HTTP/1 WebSocket upgrades through `ReverseProxy`, while
  continuing to reject CONNECT, arbitrary upgrades, and HTTP/2 extended CONNECT.
- Added Limen tracking and bounded shutdown handling for upgraded frontend
  connections, plus end-to-end SSE event and WebSocket handshake/frame tests.
- Added a runnable [configs/janus-streaming.example.json](../configs/janus-streaming.example.json)
  and updated protocol, architecture, plan, and README documentation.

Verification:

- `gofmt` on touched Go files
- `go test ./...`
- `go vet ./...`
- `go build -o bin/janus.exe ./cmd/janus`
- `go run ./cmd/janus -check -config configs/janus-streaming.example.json`

Scope note: HTTP/2 WebSocket extended CONNECT, gRPC, HTTP/3, per-stream idle
policies, and application-level WebSocket authentication remain future work.

## 2026-09-16

### Add dynamic routing reload and certificate rotation

Commit message: `feat(runtime): add file reload and certificate rotation`

Scope:

- Added bounded configuration snapshots with SHA-256 content hashes and strict
  parsing from the same file read used for deduplication.
- Added a polling FileReloader that serializes through Runtime publication,
  coalesces unchanged content, logs rejected candidates, and preserves the last
  good generation for malformed, missing, or startup-changing input.
- Strengthened Runtime's startup boundary to compare normalized Limen bindings,
  protocol/TLS settings, config version, and global settings before replacement.
- Added atomic TLS certificate/key publication for future handshakes and a
  separate certificate poller that retains the previous identity for invalid or
  partial pairs.
- Enabled versioned deployments to poll routes and certificates from `cmd/janus`
  while preserving legacy startup-only configuration behavior. Updated the
  architecture, plan, runtime, README, and commit documentation.

Verification:

- `gofmt` on touched Go files
- `go test ./...`
- `go vet ./...`
- `go build -o bin/janus.exe ./cmd/janus`
- `go run ./cmd/janus -check -config configs/janus.json`

Scope note: HTTP/3, unencrypted HTTP/2, long-lived protocol contracts, request
policies, health, admission, and admin endpoints remain future work.

## 2026-09-16

### Add native TLS and HTTP/2 Limens

Commit message: `feat(limen): add TLS and HTTP/2 bindings`

Scope:

- Added versioned configuration with named Limen bindings, explicit HTTP/1 and
  HTTP/2 protocol selection, TLS certificate/key loading, minimum TLS version,
  and relative certificate-path resolution.
- Extended Limen to serve plaintext HTTP/1 or TLS HTTP/1/HTTP2 with ALPN while
  keeping the handler and runtime shared across bindings.
- Added trusted Limen context scoping so routes can be attached to a specific
  inbound binding without allowing clients to select a binding themselves.
- Preserved outbound HTTP/2 when the custom transport dialer is configured and
  added negotiation coverage for inbound and outbound TLS traffic.
- Added multi-Limen startup cleanup, TLS-aware `-check`, a runnable TLS example,
  and updated the protocol, architecture, runtime, plan, and README documents.

Verification:

- `gofmt` on touched Go files
- `go test -count=3 ./internal/limen ./internal/proxy ./internal/router ./internal/config`
- `go test ./...`
- `go vet ./...`
- `go build -o bin/janus.exe ./cmd/janus`
- `go run ./cmd/janus -check -config configs/janus.json`

Scope note: HTTP/3, unencrypted HTTP/2, long-lived protocol contracts, routing
file reload, and certificate rotation remain future work.

## 2026-09-16

### Add stable runtime generations

Commit message: `feat(runtime): add stable generation dispatcher`

Scope:

- Added `internal/runtime` with a stable `http.Handler`, process-owned outbound
  transport, in-memory generation replacement, and startup-setting protection.
- Added synchronized request acquisition and retirement so old generations are
  closed only after their active requests release them.
- Added candidate cleanup on build failure, rollback preservation, panic-path
  release, shared-transport reuse, and a bounded retired-generation limit.
- Refactored Gateway construction to accept an injected `http.RoundTripper` and
  keep route/service middleware inside the generation graph.
- Moved the global protocol guard and overall timeout into the stable runtime
  chain; `cmd/janus` now installs Runtime in Protocol Limen.
- Updated the plan, architecture, runtime design, and README to mark 2B
  complete. File watching, TLS/HTTP/2, and certificate reload remain future work.

Verification:

- `go test ./...`
- `go test -count=5 ./internal/runtime ./internal/gateway ./internal/middleware`
- `go vet ./...`
- `go build -o bin/janus.exe ./cmd/janus`
- `go run ./cmd/janus -check -config configs/janus.json`

## 2026-09-16

### Qualify deadlines and extract Protocol Limen HTTP/1 lifecycle

Commit message: `feat(limen): extract HTTP/1 lifecycle and qualify deadlines`

Scope:

- Added `internal/limen` to own the current HTTP/1 listener, server budgets,
  serving, graceful shutdown, and immediate close lifecycle.
- Updated `cmd/janus` to construct and run Protocol Limen while keeping Gateway
  as an `http.Handler`; removed inbound `http.Server` construction from Gateway.
- Reworked qualification coverage so slow uploads prove server read-deadline
  termination, slow readers prove server write-deadline termination, and the
  timeout middleware proves an earlier parent deadline is preserved and cancels.
- Added bind-failure coverage and moved server-budget assertions to Limen tests.
- Updated the architecture, delivery plan, runtime design, and README to mark
  1Q and 2A complete while keeping HTTPS/HTTP/2, runtime generations, reload,
  and HTTP/3 as future work.

Verification:

- `go test ./...`
- `go test -count=5 ./internal/limen ./internal/gateway ./internal/middleware`
- `go vet ./...`
- `go build -o bin/janus.exe ./cmd/janus`
- `go run ./cmd/janus -check -config configs/janus.json`

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
