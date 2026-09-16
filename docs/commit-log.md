# Commit log

This document records the purpose, scope, and verification for each Janus
repository commit. Add a new dated section before every future commit.

## 2026-09-16

### Verify HTTP/3 failure stops TCP fallback

Commit message: `test(protocol): verify HTTP/3 failure stops TCP fallback`

Scope:

- Added a real Limen integration test with an injectable `PacketConn` read
  failure after TCP/UDP startup.
- Verified that an H3 serve-loop failure causes the TCP fallback to stop
  accepting new connections, while the existing lifecycle cleanup remains
  bounded.
- Updated the delivery evidence to separate this local failure class from
  broader fault-injection, deployment and Linux interoperability qualification.

Verification:

- `gofmt` on the touched Go test
- `go test -count=10 ./internal/limen -run TestLimenHTTP3FailureStopsTCPFallback`

Scope note: this covers one injected UDP read-failure path only; it is not full
H3 fault-injection or production qualification.

### Validate TLS certificate validity before publication

Commit message: `fix(security): validate TLS certificate validity`

Scope:

- Validated every configured certificate-chain entry for parseability and
  current validity before startup or atomic rotation.
- Required the leaf certificate to support digital signatures and server
  authentication when those extensions are present, preventing an unusable
  replacement identity from being published.
- Added startup and rotation regression coverage and documented the stronger
  certificate contract.

Verification:

- `gofmt` on touched Go files
- `go test -count=5 ./internal/limen -run 'TestLimenRejectsInvalidServerCertificateValidity|TestCertificateReloaderPublishesOnlyValidatedPairs|TestHTTP3CertificateRotationKeepsExistingConnectionAndUpdatesNewHandshake'`
- `go test -count=2 ./...`
- `go vet ./...`
- `go build -o bin/janus.exe ./cmd/janus`
- `git diff --check`

Scope note: certificate validity and usage checks do not replace chain trust,
hostname coverage, dependency review or production security qualification.

### Close HTTP/3 packet on Limen serve failure

Commit message: `fix(lifecycle): close HTTP/3 packet on serve failure`

Scope:

- Protected the application-owned UDP packet connection reference with a
  lifecycle mutex and made packet release idempotent across all Limen shutdown
  paths.
- Closed the HTTP/3 packet and initiated asynchronous H3 force-close when the
  TCP or H3 serve loop exits unexpectedly, preventing leaked sockets or active
  QUIC connections for direct Limen users.
- Added a real port-rebind regression test and documented the remaining
  fault-injection and deployment qualification scope.

Verification:

- `gofmt` on touched Go files
- `go test -count=5 ./internal/limen -run TestLimenServeFailureClosesHTTP3Packet`

Scope note: this proves local serve-failure cleanup; it is not a substitute for
Linux interop, deployment, load/soak or production lifecycle qualification.

### Reject duplicate JSON configuration keys

Commit message: `fix(config): reject duplicate JSON keys`

Scope:

- Added a bounded JSON token scan before typed decoding so duplicate object
  members are rejected instead of silently using the last value.
- Applied the 1 MiB configuration bound to the public `Load` path as well as
  file loading, keeping direct and watched configuration parsing consistent.
- Added top-level and nested duplicate-key regression cases and updated the
  configuration security documentation.

Verification:

- `gofmt` on touched Go files
- `go test -count=5 ./internal/config`

Scope note: this closes one configuration ambiguity; duplicate-key rejection
does not replace the remaining security review, dependency audit or production
qualification gates.

### Verify HTTP/3 generation reload

Commit message: `test(runtime): verify HTTP/3 generation reload`

Scope:

- Added a real local UDP HTTP/3 integration test through the stable Runtime
  handler.
- Verified that a stream already using the old generation completes with the
  old route after reload, while a concurrent stream on the same QUIC connection
  uses the replacement route.
- Updated the Phase 5 evidence to distinguish this local reload guarantee from
  the remaining fault-injection, deployment and Linux interop gates.

Verification:

- `gofmt` on the new Go test
- `go test -count=3 ./internal/runtime -run TestHTTP3ReloadKeepsOldStreamOnOldGeneration`

Scope note: this proves local runtime/generation behavior over HTTP/3; it is not
production certification or Linux interoperability qualification.

### Bound HTTP/3 forced shutdown

Commit message: `fix(lifecycle): bound HTTP/3 forced shutdown`

Scope:

- Fixed Limen shutdown being able to wait indefinitely in quic-go after the
  caller's drain context expired while an H3 handler remained active.
- Added a context-bounded H3 shutdown wrapper and asynchronous force-close
  initiation; packet resources are still released and active handler code is
  not falsely claimed to be forcibly stoppable.
- Added a real local UDP regression test with a non-cooperative handler and
  documented the handler-cancellation boundary.

Verification:

- `gofmt` on touched Go files
- `go test -count=3 ./internal/limen -run TestLimenHTTP3ShutdownHonorsDrainDeadline`

Scope note: this proves the local Limen return/network-stop budget, not process
orchestrator behavior, Linux interop, load/soak or deployment qualification.

### Verify HTTP/3 certificate rotation

Commit message: `test(protocol): verify HTTP/3 certificate rotation`

Scope:

- Added real UDP HTTP/3 coverage proving an established QUIC connection remains
  usable after certificate replacement.
- Added a new-client handshake check using only the replacement certificate's
  trust root, proving future H3 handshakes observe the rotated TLS identity.
- Updated the Phase 5 delivery evidence without marking Linux interop or
  production qualification complete.

Verification:

- `gofmt` on touched Go files
- `go test -count=3 ./internal/limen -run 'HTTP3CertificateRotation|CertificateReloader'`

Scope note: local certificate-rotation evidence only; H3 reload, fault,
interop, load/soak and deployment gates remain.

### Bound HTTP/3 bidirectional streams

Commit message: `feat(protocol): bound HTTP/3 concurrent streams`

Scope:

- Added the consumed `limen.http3.max_concurrent_streams` setting with a
  default of 100 and a bounded upper limit.
- Applied the value to quic-go's inbound bidirectional stream budget while
  retaining the separate global/service request admission limits.
- Added configuration, default, upper-bound and wrong-scope validation tests;
  updated the H3 protocol and delivery documentation.

Verification:

- `gofmt` on touched Go files
- `go test -count=3 ./internal/config ./internal/limen ./internal/runtime`
- H3 integration test asserts the configured QUIC stream budget and 0-RTT is off

Scope note: this bounds per-connection bidirectional streams; it does not
complete H3 Linux interop, fault-injection, load/soak or deployment gates.

### Qualify local HTTP/3 stream behavior

Commit message: `test(protocol): qualify HTTP/3 stream lifecycle`

Scope:

- Added local real-UDP HTTP/3 regression coverage for concurrent stream
  isolation and client-cancellation propagation.
- Added direct Limen validation tests so invalid H3 bindings return errors
  instead of reaching a nil TLS configuration.
- Updated the Phase 5 plan to distinguish local evidence from the remaining
  Linux interop, fault-injection and deployment gates.

Verification:

- `gofmt` on touched Go files
- `go test -count=2 ./...`
- `go vet ./...`
- `go build -o bin/janus.exe ./cmd/janus`
- `git diff --check`

Scope note: this proves local stream/cancellation behavior only; it is not a
production-certification claim.

### Add native HTTP/3 Limen adapter

Commit message: `feat(protocol): add native HTTP/3 limen adapter`

Scope:

- Added an opt-in HTTP/3 binding backed by pinned `github.com/quic-go/quic-go`
  v0.61.0, with the existing stable runtime handler as the request entry point.
- Bound TCP and UDP sockets for one Limen, set the actual UDP port in `Alt-Svc`,
  reused the atomic TLS certificate callback, and kept HTTP/3 0-RTT disabled.
- Added validation requiring TLS and an HTTP/1 or HTTP/2 TCP fallback, plus
  coordinated Limen serving/shutdown and local real-UDP forwarding coverage.
- Updated the protocol, architecture, plan and TLS example documentation. H3
  interop, deployment, load/soak and Linux qualification remain future gates.

Verification:

- `gofmt` on touched Go files
- `go test ./...`
- real local HTTP/3 request through a UDP socket and TCP `Alt-Svc` assertion
- configuration validation tests

Scope note: this is an adapter milestone, not a production-certification claim.
HTTP/3 fault-injection, Linux interop, load/soak and deployment gates remain.

## 2026-09-16

### Add bounded admin metrics

Commit message: `feat(observability): add bounded admin metrics`

Scope:

- Added one process-owned telemetry registry for request totals/errors/duration,
  in-flight permits, admission rejections, reload outcomes and shutdown drain
  duration.
- Added `/metrics` to the optional loopback admin listener with Prometheus text
  output and GET/HEAD method handling. Active-generation backend health is
  exposed by service and target index only.
- Capped metric series and label length; raw paths, hosts, request IDs, user
  identifiers and upstream URLs are never stored or emitted. Reload and
  generation replacement do not create duplicate registries.
- Added formatter, admin endpoint, label-bound, admission and active health
  integration coverage, plus lifecycle documentation.

Verification:

- `gofmt` on touched Go files
- `go test ./...`
- `go vet ./...`
- `go build -o bin/janus.exe ./cmd/janus`
- `go run ./cmd/janus -check` for the runnable configuration examples
- `git diff --check`

Scope note: metrics are operational telemetry, not production qualification;
Linux/race/load/soak/security gates remain future work.

### Add per-limen trusted forwarded identity

Commit message: `feat(security): add trusted proxy identity policy`

Scope:

- Added strict per-limen `trusted_proxies` CIDR configuration, including legacy
  single-limen compatibility, canonicalization, duplicate rejection and a
  bounded CIDR count.
- Added right-to-left multi-hop validation from the immediate peer. Untrusted
  peers and malformed chains fall back to the peer; trusted requests emit only
  canonical X-Forwarded-For, X-Forwarded-Proto and X-Forwarded-Host values.
- Kept the policy explicit and secure by default: no trust-all mode, no PROXY
  protocol, no raw forwarding-header pass-through. Trusted HTTPS scheme/host
  propagation supports backend HTTPS-aware redirects.
- Added forwarding unit tests, real-connection integration coverage, config
  validation and runtime reload protection for listener trust policy changes.

Verification:

- `gofmt` on touched Go files
- `go test ./...`
- `go vet ./...`
- `go build -o bin/janus.exe ./cmd/janus`
- `go run ./cmd/janus -check` for the runnable configuration examples
- `git diff --check`

Scope note: metrics and overall production qualification remain future work.

### Add service active health probes

Commit message: `feat(health): add active service upstream checks`

Scope:

- Added optional service `health_check` configuration with bounded interval,
  timeout, jitter, failure/recovery thresholds and expected status.
- Added a service-owned bounded probe worker pool and synchronized health store;
  probes close/drain bounded response data and are canceled with generation
  retirement.
- Made upstream selection skip excluded targets and return 503 when every
  target is unhealthy. Initial eligibility remains healthy until probe evidence
  says otherwise; only active checks are implemented, not passive failure
  marking.
- Added configuration, threshold, cancellation, selection and gateway
  integration tests, plus a runnable health-check example.

Verification:

- `gofmt` on touched Go files
- `go test ./...`
- `go vet ./...`
- `go build -o bin/janus.exe ./cmd/janus`
- `go run ./cmd/janus -check` for the runnable configuration examples
- `git diff --check`

Scope note: trusted proxy identity, metrics and overall production
qualification remain future work.

### Stop admission before bounded shutdown removal delay

Commit message: `feat(lifecycle): add admission-aware removal delay`

Scope:

- Added `shutdown.load_balancer_removal_delay`, bounded to five minutes and to
  the total `shutdown.drain_timeout` budget.
- Stops the Runtime global admission gate before readiness removal propagation,
  so new requests on existing keep-alive/HTTP2 connections receive 503 while
  already-acquired requests continue draining.
- Runs removal delay and Limen/admin shutdown under one shared grace context,
  preserving a total shutdown bound; added stop/admission and configuration
  regression coverage.
- Updated Phase 3 status and lifecycle documentation.

Verification:

- `gofmt` on touched Go files
- `go test ./...`
- `go vet ./...`
- `go build -o bin/janus.exe ./cmd/janus`
- `go run ./cmd/janus -check` for the runnable configuration examples
- `git diff --check`

Scope note: active backend health probes, metrics and overall production
qualification remain future work.

### Make graceful-drain budget configurable

Commit message: `feat(lifecycle): configure graceful drain budget`

Scope:

- Added `settings.shutdown.drain_timeout`; omission follows the effective
  `server.write_timeout`, and values shorter than that write budget are rejected.
- Replaced the command's hard-coded 35-second shutdown context with the validated
  configured budget while preserving readiness-first drain and force-close flow.
- Added settings regression coverage and updated lifecycle/configuration docs.

Verification:

- `gofmt` on touched Go files
- `go test ./...`
- `go vet ./...`
- `go build -o bin/janus.exe ./cmd/janus`
- `go run ./cmd/janus -check` for the runnable configuration examples
- `git diff --check`

Scope note: load-balancer removal delay, health probes, metrics and overall
production qualification remain future work.

### Add private liveness and readiness listener

Commit message: `feat(admin): add loopback health endpoints`

Scope:

- Added optional loopback-only `settings.admin.address` validation and a
  separate admin HTTP server exposing `GET`/`HEAD` `/livez` and `/readyz`.
- Readiness becomes true only after all business listeners are bound and serving,
  and is cleared before graceful drain; liveness remains true while the process
  drains. Admin requests do not consume business admission permits.
- Added startup cleanup for partial listener failures and state/method/HEAD
  endpoint tests plus a runnable admin configuration example.

Verification:

- `gofmt` on touched Go files
- `go test ./...`
- `go vet ./...`
- `go build -o bin/janus.exe ./cmd/janus`
- `go run ./cmd/janus -check` for the runnable configuration examples
- `git diff --check`

Scope note: backend health probes, metrics, configurable drain and overall
production qualification remain future work.

### Add bounded global and service admission

Commit message: `feat(admission): add global and service concurrency limits`

Scope:

- Added a bounded non-waiting global admission cap with a default of 1024 and
  immediate 503 rejection when saturated.
- Added service-only typed `in_flight` middleware. Runtime owns stable service
  limiter state so routes sharing a service and old/new generations share active
  permits; lowering a cap does not create capacity for existing work.
- Applied service limit changes transactionally at generation publication, so a
  failed reload preserves the active limit. Permits release on normal return,
  cancellation and panic paths.
- Added configuration validation, a runnable admission example, concurrency,
  panic, lowered-limit, cross-generation and failed-reload regression tests.

Verification:

- `gofmt` on touched Go files
- `go test ./...`
- `go vet ./...`
- `go build -o bin/janus.exe ./cmd/janus`
- `go run ./cmd/janus -check` for the runnable configuration examples
- `git diff --check`

Scope note: admin readiness, configurable drain, health probes, metrics and
overall production qualification remain future work.

### Preserve abort semantics while finalizing access observation

Commit message: `fix(observability): preserve response abort semantics`

Scope:

- Changed the fixed observer to log `http.ErrAbortHandler` outcomes before
  re-panicking, preserving net/http's truncated-response behavior instead of
  accidentally emitting a terminating chunk after a response-copy failure.
- Added trailer, partial-write, response-copy-abort and gateway-level 404/413/
  501/502/504 access-observation regression coverage.
- Updated the Phase 2F status and current capability documentation.

Verification:

- `gofmt` on touched Go files
- `go test ./...`
- `go vet ./...`
- `go build -o bin/janus.exe ./cmd/janus`
- `go run ./cmd/janus -check` for the runnable configuration examples
- `git diff --check`

Scope note: metrics, admission, health, trusted-proxy identity and overall
production qualification remain future work.

### Add fixed request observation and response capability preservation

Commit message: `feat(observability): add request IDs and access observation`

Scope:

- Added a process-owned observer outside runtime generations. It regenerates
  request IDs, counts consumed request and written response bytes, records route
  and service metadata, and emits one access record without query strings,
  headers, cookies or bodies.
- Added synchronized request-local outcome state and explicit early-error classes
  for routing, protocol, body-limit, timeout, cancellation and upstream failures.
- Added response-writer capability preservation for `Unwrap`, supported flushing,
  HTTP/1 hijacking and HTTP/2 push, without advertising capabilities absent from
  the underlying writer; informational/final status handling is covered.
- Added observer tests for request ID replacement, field boundaries, 103/200
  handling, controller flushing, capability boundaries and WebSocket 101.

Verification:

- `gofmt` on touched Go files
- `go test ./...`

Scope note: complete response trailer/copy accounting audit, metrics export,
trusted client identity and production qualification remain future work.

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
