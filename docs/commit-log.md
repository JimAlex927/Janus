# Commit log

This document records the purpose, scope, and verification for each Janus
repository commit. Add a new dated section before every future commit.

## 2026-09-18 — Nacos discovery core

Commit: `6fa8045` (`feat(discovery): add Nacos-backed dynamic services`).

Baseline committed before implementation: `705234e`
(`feat(admin): add embedded visual configuration console`).

Scope:

- Added named Nacos registry connections and per-Service discovery selection,
  including multiple namespaces, defaults, source exclusivity and validation.
- Isolated the pinned Naming SDK adapter from shared discovery lifecycle and
  local upstream selection; added reference-counted clients/subscriptions.
- Added atomic weighted endpoint snapshots, authoritative empty membership,
  bounded stale data, and no lease renewal on unchanged SDK cache reads.
- Reused subscriptions across runtime generations and released provisional or
  retired-generation leases on rollback/drain; fixed typed-nil Gateway builder
  errors causing a panic in Runtime cleanup.
- Added real HTTP forwarding/empty/recovery and generation lifecycle regression
  tests; documented the architecture and follow-up UI work in
  `docs/nacos-discovery.md`.

Verification: `go test ./...` and `go vet ./...` passed. The Windows race
runtime failed to initialize with a ThreadSanitizer allocation error (87);
race checks must be rerun on Linux. Real Nacos cluster integration remains
a separate deployment acceptance step.

## 2026-09-18 — Admin discovery status

Commit: `e7ace51` (`feat(admin): expose discovery runtime status`).

Scope:

- Added authenticated `GET /api/v1/discovery`, exposing only the Runtime's
  local discovery snapshot and active configuration revision.
- Added Admin and discovery-manager regression tests for authentication,
  invalid factory results, nil cancellation functions, and idempotent release.

Verification: targeted Admin, discovery, Runtime, and command tests plus vet
passed; the subsequent full `go test ./...` and `go vet ./...` also passed.

## 2026-09-18 — Nacos configuration examples

Commit: `8b4f58d` (`docs(config): add Nacos configuration examples`).

Scope:

- Added single-namespace, multi-namespace, and mixed static/Nacos examples.
- Updated README and Nacos documentation with the example matrix and startup
  credential/port notes.
- Each new example passes `go run ./cmd/janus -check`; `-check` intentionally
  does not connect to Nacos.

The Nacos registry also accepts a temporary plaintext `password` next to
`username`; effective-config and Admin responses redact it, while
`password_env` remains available for deployments that inject secrets.

## 2026-09-17

### Improve console editing workflow

Commit message: `refactor(console): improve draft editing and route selection`

Scope:

- Added route selection so the visual editor always operates on the chosen
  route instead of implicitly editing the first route.
- Kept incomplete JSON drafts in the editor, surfaced parse errors, and
  prevented validation or publishing until the draft becomes valid.
- Added logout behavior, live generation refresh, and cumulative request
  activity sampling for the overview chart.
- Excluded frontend dependencies and build output from version control while
  keeping the compiled assets required by Go `embed`.
- Removed an unused frontend data-fetching dependency and split the initial
  JavaScript payload into React, chart, and drag-and-drop chunks.
- Replaced the single-purpose chart dependency with a small responsive SVG
  chart, keeping the embedded console lightweight and avoiding a large chart
  bundle on every admin page.
- Corrected the chart time-window label and added explicit SVG sizing and an
  empty-state overlay for narrow or newly started consoles.
- Added `cmd/janus-hash` for interactive or stdin-based bcrypt hash generation
  without exposing the password as a command-line argument.
- Added an explicit synced/dirty draft state, disabled publish when there is
  no valid unpublished change, and reset the state after reload/publish.
- Added canvas draft undo/redo documentation and grouped rapid inspector edits
  into a single history step.
- Protected unpublished drafts from being silently overwritten by runtime
  refresh events; manual refresh now requires confirmation before discarding.
- Made canvas connections forgiving: while connecting, the whole highlighted
  target node accepts the relation, and output-port clicks no longer bubble into
  accidental node selection.
- Rendered missing Route-referenced Services as repairable nodes and rejected
  ambiguous shared-Middleware connections instead of silently editing the first
  matching Route.
- Extracted graph construction and configuration mutations into
  `frontend/src/graph-model.ts`, keeping the canvas component focused on UI
  interaction.
- Added five Node model tests and the `npm run test:model` script for connection
  semantics, missing Services, shared Middleware safety, and position bounds.
- Added the Service→Middleware model regression and clarified the graph model's
  single-source-of-truth and non-mutating failure behavior with comments.
- Replaced the permanent Inspector column with a type-specific node editor
  modal, added a Route Middleware mini-canvas with reorder controls, and made
  Match editing a larger multiline field.
- Added browser-side numeric bounds that mirror the backend validation ranges
  for middleware, health checks, actions, and HTTP/3 settings.
- Centralized the connection predicate used by both graph mutations and target
  highlighting, so valid targets are visibly distinct and unsupported targets
  are subdued while connecting.
- Marked Route-referenced Middleware definitions that are missing from the
  configuration inside the Route editor, where they can be removed or replaced.
- Preserved legacy Route Service references as visible Service edges, so
  unresolved upstream configuration remains repairable.
- Made the default graph layout wrap nodes into bounded columns and expanded
  the drawable world, preventing larger configurations from placing nodes past
  the visible canvas; added a large-graph bounds regression test.
- Removed Middleware nodes and Middleware edges from the top-level canvas. Route
  editing now owns Middleware creation, selection, and ordering, while new
  canvas Routes start without an implicit Service connection.
- Replaced free-form Limen/Route protocol inputs with fixed multi-select
  controls and added Route-scoped Middleware type and parameter editing.
- Restricted Middleware selectors to backend-supported policies by scope,
  surfaced each definition's type, and made Service Middleware use the same
  safe selector instead of free-form names.
- Added a draft-only request simulation drawer that evaluates built-in matching
  rules, accepts Host and Header inputs, reports candidates, and selects the
  winning Route without sending a request.
- Removed the duplicate publish action and retired the unused drag-and-drop
  Route editor and its frontend dependency chunks.
- Added modal focus placement, focus restoration, and keyboard Tab trapping for
  predictable keyboard editing.
- Updated `configs/janus-admin.example.json` with a bcrypt hash for the sample
  password `123456` so the example console can be entered immediately.
- Replaced the basic configuration page with a synchronized visual canvas and
  JSON editor, including node creation, graph layout, inspectors, deletion,
  and route Middleware selection.
- Added interactive port-to-port connections, automatic canvas fitting, and
  explicit Service visibility in the default graph layout.
- Added canvas drag boundaries, connection previews, reference-safe Service
  deletion, and grouped Limen/Service inspector controls for TLS, HTTP/3, and
  health checks.

Verification:

- `frontend: tsc -b`
- `frontend: vite build`
- `go build ./cmd/janus`
- `go test ./...`
- `go vet ./...`
- `git diff --check`

## 2026-09-17

### Harden and polish the embedded console

Commit message: `refactor(admin): harden console publishing and metrics`

Scope:

- Protected status, configuration, metrics, validation, and event APIs when
  administrator credentials are configured; bounded active sessions and added
  no-store API responses.
- Added real metrics summary sampling to the console, runtime generation event
  coverage, stronger bcrypt validation, and private-network admin address
  validation.
- Split frontend vendor output, refreshed embedded assets, and removed stale
  bundles from the embedded resource set.

Verification:

- `frontend: tsc -b`
- `frontend: vite build`
- `go test ./...`
- `go vet ./...`

## 2026-09-17

### Add embedded Janus administration console

Commit message: `feat(admin): add embedded configuration console`

Scope:

- Added the independent `frontend/` React/TypeScript console with overview,
  route middleware ordering, Service/Middleware lists, JSON editing, draft
  validation, and a visual style suitable for later extension.
- Embedded the production frontend build in the Admin server and added
  authenticated configuration/status APIs, revision checks, SSE generation
  events, and low-cardinality metrics snapshots.
- Added bcrypt administrator settings, loopback/private admin binding
  validation, atomic configuration persistence with runtime rollback, and
  protection against duplicate file-reloader generations.
- Redacted the administrator password hash from effective configuration output
  and added documentation in `docs/admin-console.md`.

Verification:

- `frontend: tsc -b`
- `frontend: vite build`
- `go test ./...`
- `go vet ./...`
- `go test -race` was attempted but the local Windows ThreadSanitizer failed
  to reserve its shadow memory before tests ran.

## 2026-09-17

### Add explicit cleartext HTTP/2 Limen support

Commit message: `feat(limen): support explicit h2c bindings`

Scope:

- Added the `h2c` Limen protocol, backed by Go `net/http`'s unencrypted
  HTTP/2 support and the existing raw TCP listener path.
- Kept TLS-backed `http2` separate from `h2c`; a binding cannot combine h2c
  with TLS or another HTTP/2 mode.
- Added real TCP coverage for h2c with HTTP/1.1 fallback, plus startup and
  configuration rejection tests for invalid combinations.
- Added an h2c configuration example and updated the architecture, runtime,
  protocol, and delivery-plan documentation.

Verification:

- `go test -count=1 ./...`
- `go test -race -count=1 ./...`
- `go vet ./...`
- `go run ./cmd/janus -check -config configs/janus-h2c.example.json`

## 2026-09-17

### Harden rule parsing and route matching regression coverage

Commit message: `fix(router): harden rule parser and matcher tests`

Scope:

- Fixed invalid trailing characters and unterminated literals being confused
  with end-of-expression during rule parsing.
- Added expression size, nesting-depth, and argument-count limits to bound
  configuration parsing work.
- Made matcher construction and nil request facts fail safely, including
  rejecting custom compilers that return a nil predicate without an error.
- Added coverage for all built-in predicates, path/host boundaries, complex
  expression fallback, route priority, invalid expressions, and fuzzed
  compile/match inputs.

Verification:

- `go test -count=1 ./...`
- `go test -race -count=1 ./...`
- `go test -count=20 ./internal/rules ./internal/router`
- `go vet ./...`
- `go test -fuzz=FuzzCompileAndMatchNeverPanics -fuzztime=30s ./internal/rules`

## 2026-09-17

### Migrate configuration examples to match/action routes

Commit message: `docs(config): migrate examples to current route format`

Scope:

- Rewrote every JSON file under `configs/` to use versioned Limens and the
  current `match` plus explicit `action` route format.
- Converted ordinary HTTP, SSE, WebSocket, middleware, health-check, admin,
  TLS, redirect, and direct-response examples without changing their intent.
- Added a regression test that loads every example and requires the current
  route shape.

Verification:

- All configuration examples passed `LoadFileSnapshot` validation.
- `go test -count=1 ./...`

## 2026-09-17

### Add compiled route rules, indexes, and direct actions

Commit message: `feat(router): add compiled matching and route actions`

Scope:

- Added the source-controlled rule registry and boolean matcher for `Host`,
  `Path`, `PathPrefix`, `Method`, `Header`, `Query`, and application protocol
  predicates, including `&&`, `||`, `!`, parentheses, and custom compiler
  registration.
- Reworked Router construction to compile rules into immutable Limen/Host
  indexes and a segment-aware path tree, with a safe fallback for expressions
  whose boolean structure cannot yet provide an index hint.
- Added route priority selection and preserved the existing unsupported
  protocol response for structured routes.
- Added `forward`, `redirect`, and `respond` route actions and a comprehensive
  configuration example using the new match/action form.
- Added regression tests for boolean matching, custom rules, indexed and
  fallback routes, priority selection, direct responses, and the example file.

Verification:

- `go test -count=1 ./...`

## 2026-09-17

### Stabilize stream timing regression under race instrumentation

Commit message: `test: stabilize stream timeout race coverage`

Scope:

- Increased the idle-activity test margin so scheduler and race-detector
  overhead cannot turn a valid activity sequence into a timing flake.

Verification:

- The targeted test passed five times under `go test -race`.
- `go test -race -count=1 ./...` passed on Windows/amd64.

### Repair metrics accumulation and stream request-body cancellation

Commit message: `fix: repair metrics counters and stream body cancellation`

Scope:

- Made existing request, error, rejection, and duration series accumulate
  samples while retaining the bounded metric-series limit.
- Closed stream request bodies when SSE or WebSocket lifetime cancellation
  fires, releasing blocked HTTP/2 and HTTP/3 reads and their admission permits.
- Added regression coverage for repeated metric samples and an actual HTTP/3
  SSE request with a slow request body.
- Clarified that TLS wraps the TCP listener used by `net/http`; it is not a
  second forwarding server.

Verification:

- `go test -count=1 ./...` passed on Windows/amd64.
- `go test -race ./internal/telemetry ./internal/limen ./internal/middleware ./internal/runtime` passed.
- `go vet ./...`, Windows build, Linux test cross-compilation and
  `git diff --check` passed.

Qualification: target Linux execution, capacity/soak evidence and canary/
rollback evidence remain required.

### Clarify runtime construction and protocol lifecycle

Commit message: `docs: clarify runtime and protocol construction`

Scope:

- Added explanatory comments around configuration checking, Limen protocol
  binding, TLS/HTTP/2/HTTP/3 startup, administration endpoints, Runtime
  ownership, generation replacement, shared transport, metrics, and admission
  limiters.
- Renamed Runtime fields to make the generation builder and shared service
  limiter registry explicit.
- Added comments to the example body-limit configuration and the configuration
  reload path while preserving the existing behavior.

Verification:

- `gofmt` on changed Go files.
- `go test -count=1 ./...` passed on Windows/amd64.


## 2026-09-17

### Repair lifecycle and snapshot acceptance defects

Commit message: `fix: repair stream drain and startup snapshot guarantees`

Scope:

- Distinguished graceful listener termination from sibling-protocol failure so
  TCP/QUIC drain does not prematurely close accepted requests; deadline paths
  force-close both transports.
- Interrupted in-progress SSE writes on stream expiry, joined timer cleanup,
  aborted already-committed incomplete responses and preserved 1xx semantics.
- Passed the exact startup configuration hash to its watcher. Certificate polls
  start without an assumed baseline and hash/parse/publish the same PEM bytes.
- Added client/resource regressions for graceful H3, stalled TCP writes, startup
  route/certificate updates, 103-to-504, and H1/H2/H3 SSE backend/admission release.
  Strengthened second-event, peer EOF and idle-refresh assertions.
- Added a Linux-only built-process SIGTERM regression, a production acceptance
  record and a Chinese source-reading guide. Preserved the operator's main.go
  comment separately from the implementation changes.

Verification:

- Targeted lifecycle, gateway, runtime and middleware tests on Windows/amd64.
- Linux process test cross-compilation (execution awaits the target VM).
- `GOTOOLCHAIN=go1.25.13+auto go test -count=1 ./...` passed.
- `go vet ./...`, `go build -o bin/janus.exe ./cmd/janus` and
  `git diff --check` passed.

Qualification: target Linux race/deployment execution, agreed traffic/memory
budgets, 24h soak and a real non-core canary/rollback remain unfulfilled gates.

### Deduplicate unreadable certificate reload errors

Commit message: `fix(reload): deduplicate certificate read errors`

Scope:

- Deduplicated repeated certificate/key fingerprint-read failures by their
  error identity, so a missing or temporarily unreadable asset does not emit
  the same error on every polling tick.
- Kept changed failure messages observable and preserved retry behavior; a
  readable but rejected certificate pair is still retried until rotation
  succeeds.
- Added an observer-backed regression test for repeated unreadable and changed
  certificate failures.

Verification:

- `gofmt -w internal/limen/cert_reloader.go internal/limen/cert_reloader_test.go`
- `go test -count=5 ./internal/limen`
- `git diff --check`

Scope note: this bounds duplicate logging for certificate reload failures; it
does not change certificate validation policy or complete the broader security
and production qualification gates.

### Bound SSE and WebSocket stream lifetime

Commit message: `feat(timeout): add stream lifetime and idle budgets`

Scope:

- Added validated `settings.stream.max_duration` and
  `settings.stream.idle_timeout` settings with `1h` and `5m` defaults.
- Added a stream timeout middleware for explicitly classified SSE and classic
  HTTP/1 WebSocket requests. SSE activity refreshes the idle budget and
  cancellation reaches the backend through the request context.
- Wrapped hijacked WebSocket connections so lifetime/idle expiry and parent
  cancellation close the client-side stream; ordinary API requests retain the
  finite request timeout path.
- Mapped the stream cancellation cause to 504 before response commitment and
  added unit and end-to-end regression coverage.
- Documented the stream policy, its handler-cancellation boundary and an
  explicit streaming configuration example.

Verification:

- `gofmt -w` on changed Go files
- `go test -count=3 ./internal/config ./internal/middleware ./internal/proxy ./internal/gateway`
- `go test ./...`
- `go vet ./...`
- Windows/amd64 and `CGO_ENABLED=0` Linux/amd64 builds
- `go mod verify`
- `go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...` (zero reachable vulnerabilities)
- `git diff --check`

Scope note: this covers SSE and classic HTTP/1 WebSocket lifecycle bounds. It
does not enable WebSocket extended CONNECT, gRPC streaming, or claim H3
interoperability or full production qualification.

### Verify module integrity in release CI

Commit message: `ci: verify module integrity`

Scope:

- Added `go mod verify` to the pinned Linux CI job after toolchain setup and
  before tests, so downloaded module contents are checked against go.sum.

Verification:

- `go mod verify` using Go 1.25.13
- `git diff --check`

Scope note: module checksum verification does not replace dependency license
review, vulnerability scanning or a broader supply-chain audit.

### Enforce patched Go and vulnerability scanning in CI

Commit message: `ci: add patched Go vulnerability gate`

Scope:

- Raised the module and Linux CI toolchain baseline from Go 1.25.1 to patched
  Go 1.25.13, which fixes the standard-library vulnerabilities found by the
  initial scan.
- Configured CI with `GOTOOLCHAIN=local` so the pinned runner toolchain cannot
  silently switch to another version during a release run.
- Added a fixed `govulncheck@v1.7.0` CI step.
- Recorded the local scan result: zero reachable vulnerabilities in the project
  code; four vulnerabilities remain in required modules but were reported as
  unreachable and still need dependency review.
- Updated the production qualification documentation with the new gate.

Verification:

- `go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...` using Go 1.25.13 and `GOPROXY=https://proxy.golang.org,direct`
- `git diff --check`

Scope note: hosted Linux CI must still run this job; a clean reachability scan
does not replace dependency/license review or the broader production security
audit.

### Pin GitHub Actions used by release CI

Commit message: `ci: pin GitHub Actions to release commits`

Scope:

- Replaced movable `checkout@v4` and `setup-go@v5` references with immutable
  commits for checkout v4.2.2 and setup-go v5.5.0.
- Kept the release tag in comments so deliberate dependency refreshes remain
  reviewable.
- Updated the Phase 6 plan to record that the reusable CI actions are pinned.

Verification:

- `git ls-remote https://github.com/actions/checkout refs/tags/v4.2.2 refs/tags/v4.2.2^{}`
- `git ls-remote https://github.com/actions/setup-go refs/tags/v5.5.0 refs/tags/v5.5.0^{}`
- `git diff --check`

Scope note: hosted CI still needs an actual run; pinning action commits does not
replace the Linux, security, load or soak qualification gates.

### Bound trusted forwarded-hop processing

Commit message: `fix(security): bound forwarded proxy hops`

Scope:

- Capped the accepted `X-Forwarded-For` chain at 128 addresses, including the
  immediate peer in the resulting canonical chain.
- Overlong chains now use the existing conservative direct-peer fallback rather
  than allocating and forwarding an unbounded hop list.
- Added a regression test and documented the forwarding-chain bound.

Verification:

- `gofmt -w internal/forwarding/forwarding.go internal/forwarding/forwarding_test.go`
- `go test ./internal/forwarding ./internal/gateway`
- `git diff --check`

Scope note: this bounds forwarded-hop processing; it does not add a trust-all
mode or change the configured CIDR trust boundary.

### Prevent buffered responses from committing after timeout

Commit message: `fix(timeout): guard buffered response commitment`

Scope:

- Made the buffer middleware check the request context before committing its
  delayed response.
- A deadline that expires before commitment now produces 504 even when the
  wrapped handler returns normally after observing or ignoring cancellation;
  client cancellation still abandons the buffered response.
- Added a regression test for a handler that returns a body after its deadline.

Verification:

- `gofmt -w internal/middleware/buffer.go internal/middleware/buffer_test.go`
- `go test ./internal/middleware ./internal/gateway`
- `git diff --check`

Scope note: this does not forcibly stop arbitrary handler code and does not
change already-committed streaming response semantics.

### Require GET for long-lived protocol classification

Commit message: `fix(protocol): require GET for SSE and WebSocket modes`

Scope:

- Restricted SSE and classic HTTP/1 WebSocket detection to the standard GET
  method, preventing arbitrary POST requests from bypassing finite API timeout
  and write-deadline policies.
- Added protocol unit tests for both accepted GET and rejected POST cases.
- Documented the method requirement in the protocol and Limen runtime guides.

Verification:

- `gofmt -w internal/protocol/context.go internal/protocol/context_test.go`
- `go test ./internal/protocol ./internal/router ./internal/middleware ./internal/gateway`
- `git diff --check`

Scope note: this keeps HTTP/2 extended CONNECT, gRPC and arbitrary tunnels
outside the supported protocol contract.

### Retry rejected TLS certificate fingerprints

Commit message: `fix(reload): retry rejected certificate pairs`

Scope:

- Changed certificate reload state to record a fingerprint as applied only
  after `RotateCertificate` succeeds.
- Retried unchanged rejected pairs on later polls while deduplicating repeated
  rejection logs for the same fingerprint.
- Added a regression test proving an unchanged incomplete pair is retried and
  the last valid certificate remains active.

Verification:

- `gofmt -w internal/limen/cert_reloader.go internal/limen/cert_reloader_test.go`
- `go test ./internal/limen`
- `git diff --check`
- `go test -race ./internal/limen -run 'TestCertificateReloader|TestHTTP3CertificateRotation'` (blocked by the local Windows `runtime/cgo` toolchain exit-status-2 limitation)

Scope note: reload retry is now correct for readable rejected pairs; broader
certificate policy, security review and production qualification remain open.

### Bound TLS asset reads across startup and rotation

Commit message: `fix(tls): bound certificate asset reads`

Scope:

- Added a shared 1 MiB-per-file TLS asset reader for certificate and private-key
  PEM files.
- Applied it consistently to Limen startup, certificate rotation and reload
  fingerprinting, preventing the reload path from using unbounded `ReadFile`.
- Added startup and direct-reader regression tests for oversized assets.
- Documented the TLS asset bound alongside the existing configuration bound.

Verification:

- `gofmt -w internal/config/config.go internal/limen/tls_asset.go internal/limen/tls_asset_test.go internal/limen/limen.go internal/limen/cert_reloader.go`
- `go test ./internal/config ./internal/limen`
- `git diff --check`

Scope note: this bounds file reads; certificate policy, dependency review and
full production security qualification remain separate gates.

### Bound access-log field size

Commit message: `fix(observability): bound access log fields`

Scope:

- Bounded method, path, route and service values in access logs to 1024 bytes.
- Truncated only at valid UTF-8 boundaries and added a regression test for
  oversized multibyte values.
- Closed the gap between the observer's bounded-metadata contract and its
  actual structured log fields; metric label bounds were already enforced.

Verification:

- `gofmt -w internal/middleware/observer.go internal/middleware/observer_test.go`
- `go test ./internal/middleware`
- `git diff --check`

Scope note: this bounds log field size; it does not replace the later redaction,
security review and production qualification gates.

### Bound HTTP/3 slow request bodies by timeout cancellation

Commit message: `fix(timeout): close request bodies on cancellation`

Scope:

- Updated the timeout middleware to close a finite request body when the
  derived request context is cancelled or reaches its deadline. This closes
  the underlying QUIC stream body, which does not otherwise observe a child
  context while blocked in `Read`.
- Updated proxy error mapping to classify a non-context body-read error as 504
  when the request context already expired.
- Added unit, proxy, and real HTTP/3 slow-upload regression tests.
- Updated the Phase 1 timeout contract and HTTP/3 qualification notes.

Verification:

- `gofmt -w internal/middleware/timeout.go internal/middleware/timeout_test.go internal/proxy/proxy.go internal/proxy/proxy_test.go internal/limen/protocol_test.go`
- `go test -count=5 ./internal/middleware ./internal/proxy`
- `go test -count=5 ./internal/limen -run TestLimenHTTP3TimeoutClosesSlowRequestBody`
- `git diff --check`

Scope note: this bounds request-body reads for finite requests; SSE/WebSocket
remain outside the finite timeout contract and require their own lifecycle.

## 2026-09-16

### Verify Limen HTTP/2 active-stream drain

Commit message: `test(lifecycle): verify HTTP/2 stream drain`

Scope:

- Added a real TLS/H2 Limen integration test for graceful shutdown.
- Verified an active H2 stream completes before `Limen.Shutdown` returns and
  the TCP listener rejects new connections after shutdown begins.
- Updated H2 lifecycle evidence without claiming full GOAWAY, Linux, or load
  qualification.

Verification:

- `gofmt -w internal/limen/protocol_test.go`
- `go test -count=5 ./internal/limen -run TestLimenHTTP2ShutdownDrainsActiveStream`
- `git diff --check`

Scope note: this verifies the Limen wrapper's local H2 drain path; it does not
fully qualify client-side GOAWAY behavior or production deployment semantics.

### Preserve gateway request IDs at response commit

Commit message: `fix(observability): preserve authoritative response IDs`

Scope:

- Restored the observer-generated `X-Request-ID` immediately before final
  response commitment, preventing backend or nested middleware values from
  diverging from the access log identity.
- Added direct and buffered response regression tests.
- Updated the observability contract and Phase 2F response-capability notes.

Verification:

- `gofmt -w internal/middleware/observer.go internal/middleware/observer_test.go`
- `go test ./internal/middleware`
- `git diff --check`

Scope note: this protects the request correlation header only; it is not a
general response-header transformation or authentication policy.

### Retry transient routing publication failures

Commit message: `fix(reload): retry transient candidate failures`

Scope:

- Changed the routing reloader hash state so only successfully published
  generations are treated as applied and skipped.
- Transient candidate-build/publication failures are retried on later polls
  while unchanged failure logs remain hash-deduplicated.
- Serialized concurrent `ReloadOnce` calls to prevent duplicate candidate
  generations from being published for one content hash.
- Added regression tests for retry after a transient build failure and for
  concurrent reload serialization.
- Updated reload lifecycle documentation.

Verification:

- `gofmt -w internal/runtime/reloader.go internal/runtime/reloader_test.go`
- `go test ./internal/runtime`
- `git diff --check`

Scope note: this fixes reloader retry/serialization behavior; it does not
change startup-owned configuration rejection or add a reload queue.

### Add configuration parser fuzz smoke

Commit message: `test(config): add parser fuzz smoke`

Scope:

- Added `FuzzLoadNeverPanics` with valid, versioned and duplicate-key seed
  inputs for the bounded strict configuration loader.
- Added a ten-second fuzz smoke step to the Linux CI job.
- Updated the release plan to distinguish parser fuzz coverage from broader
  malformed-framing, security-audit and long-running fuzz qualification.

Verification:

- `gofmt -w internal/config/config_test.go`
- `go test -fuzz=FuzzLoadNeverPanics -fuzztime=5s ./internal/config`
- `git diff --check`

Scope note: this checks parser panic resistance only; it is not a complete
protocol fuzzer or security review.

### Add pinned Linux CI release gates

Commit message: `ci: add pinned Linux test and build gates`

Scope:

- Added an Ubuntu 24.04 GitHub Actions workflow pinned to Go 1.25.1.
- The workflow runs full tests twice, the race detector, `go vet`, a static
  Linux amd64 build and whitespace/artifact checks.
- Documented that the workflow is a defined gate, not evidence until it has
  actually run successfully; Linux systemd, load, soak and canary checks remain
  outside this change.

Verification:

- `git diff --check`
- Local Windows `go test -count=2 ./...`
- Local Windows `go vet ./...`
- Local Windows Windows and Linux amd64 builds

Scope note: the hosted Linux workflow has not been executed from this local
turn, and its existence does not certify production behavior.

### Add safe effective configuration inspection and native systemd artifact

Commit message: `feat(ops): add effective config inspection and systemd artifact`

Scope:

- Added `-print-effective-config`, which validates startup requirements and
  prints normalized Limen bindings and defaulted settings without TLS
  certificate or private-key asset fields.
- Added config-level coverage for legacy Limen normalization, default
  expansion, and TLS asset omission.
- Added a native Linux systemd unit and deployment/rollout/rollback guide with
  dedicated non-root execution, explicit starter resource budgets and a clear
  qualification boundary.
- Documented that the artifact is not Linux/systemd-certified on the current
  Windows development host and that container packaging remains unverified.

Verification:

- `gofmt -w internal/config/effective.go internal/config/config_test.go cmd/janus/main.go`
- `go test ./internal/config ./cmd/janus`
- `go run ./cmd/janus -print-effective-config -config configs/janus.json`

Scope note: effective inspection and the systemd artifact improve operator
reviewability but do not certify Linux behavior, CA-root packaging, resource
capacity, load/soak, or production readiness.

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

## 2026-09-17

### Wait for reload workers during shutdown

Commit message: `fix: wait for reload workers during shutdown`

Scope:

- Wait for the route and certificate reloader goroutines to exit after
  cancellation, so they cannot continue polling or logging while runtime and
  logger resources are being closed.
- Clarified the HTTP/3 Alt-Svc advertisement comment and removed stale TODOs
  whose behavior is already enforced by configuration validation or whose
  implementation needs a separately defined policy.

Verification:

- `gofmt -w cmd/janus/main.go internal/limen/limen.go internal/runtime/runtime.go internal/upstream/pool.go`
- `go test -count=1 ./...`
- `go test -race -count=1 ./...`
- `go vet ./...`

Release note: local code-level verification remains green; Linux process,
systemd deployment, capacity, soak, canary, and rollback evidence are still
required before production approval.

## 2026-09-17

### Clarify runtime ownership and qualification evidence

Commit message: `docs: clarify runtime ownership and qualification evidence`

Scope:

- Replaced question-style and stale comments in the entrypoint, Limen,
  configuration loader, and Runtime with concise descriptions of protocol
  ownership, shared transport/metrics/limiters, and transactional generation
  publication.
- Recorded the repeated local test, fuzz, vulnerability, module-integrity,
  and static-build evidence in the production-readiness document.

Verification:

- `gofmt -w cmd/janus/main.go internal/config/config.go internal/limen/limen.go internal/runtime/runtime.go`
- `go test -count=1 ./...`
- `go test -count=5 ./...`
- `go test -race -count=1 ./...`
- `go vet ./...`
- `go test -fuzz=FuzzLoadNeverPanics -fuzztime=30s ./internal/config`
- `go mod verify`
- `go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...`
- static `linux/amd64` build

## 2026-09-17

### Add process-level lifecycle regression coverage

Commit message: `test(lifecycle): cover run startup reload and shutdown`

Scope:

- Added a cross-platform entrypoint test that starts the real `run` lifecycle,
  waits for readiness, validates a client response body, observes a file-based
  route replacement, and verifies bounded context-cancellation shutdown.
- Kept the Linux-specific child-process SIGTERM test as a separate deployment
  gate because an in-process test cannot prove OS signal delivery or process
  exit behavior.

Verification:

- `gofmt -w cmd/janus/main_test.go`
- `go test -count=3 ./cmd/janus`
- `go test -race -count=1 ./cmd/janus`

Follow-up stability verification:

- `go test -count=50 -run '^TestRunLifecycle$' ./cmd/janus`
- `go test -count=1 ./...`
- `go test -race -count=1 ./...`
- `go vet ./...`

## 2026-09-17

### Add Linux process-level HTTP/3 drain coverage

Commit message: `test(linux): cover process HTTP3 signal drain`

Scope:

- Added a Linux-only child-process test that binds real TCP and UDP sockets,
  sends an HTTP/3 request through the built Janus binary, sends SIGTERM,
  verifies readiness becomes unavailable, and verifies the accepted response
  and process exit complete within the drain budget.
- Added isolated test certificate generation and a shared-port reservation
  helper for the real HTTP/3 process test.

Verification:

- `gofmt -w cmd/janus/lifecycle_linux_test.go`
- `go test -c -o bin/janus-linux-tests ./cmd/janus` with
  `GOOS=linux GOARCH=amd64 CGO_ENABLED=0`
- `go test -count=1 ./...`
- `go test -race -count=1 ./...`
- `go vet ./...`

Qualification note: the new Linux-only test was cross-compiled but not run
until a Linux VM is available.

## 2026-09-17

### Add a separate capacity qualification generator

Commit message: `feat(qualification): add independent HTTP load generator`

Scope:

- Added `cmd/janus-loadtest`, a dependency-free workload generator supporting
  fixed concurrency or bounded target arrival rate, request bodies, headers,
  per-request timeout, bounded response reads, status/error counts, bytes, and
  latency mean/p50/p95/p99/max JSON output.
- Kept the generator outside Janus request code so gateway resource measurements
  are not mixed with an in-process client. It refuses URL credentials and
  redacts query strings from its report target.
- Added validation, percentile, header parsing, and statistic regression tests.

Verification:

- `go test -count=5 ./cmd/janus-loadtest`
- `go test -race -count=1 ./cmd/janus-loadtest`
- `go vet ./cmd/janus-loadtest`
- Fixed-concurrency and target-rate local backend smoke runs

Qualification note: local smoke output validates the tool only; it is not a
production capacity claim.

## 2026-09-17

### Classify measurement-window cancellations separately

Commit message: `fix(qualification): classify end-of-window cancellations`

Scope:

- Added `cancelled_at_measurement_end` to load-test output so requests stopped
  by the generator's own measurement deadline are not mixed with backend or
  transport errors.
- Added regression coverage for the classification and kept the mean-latency
  correction discovered during the first smoke run.

Verification:

- `go test -count=5 ./cmd/janus-loadtest`
- `go test -race -count=1 ./cmd/janus-loadtest`
- `go vet ./cmd/janus-loadtest`
- Direct-backend and Janus forwarding smoke runs with a 100 request/s target

## 2026-09-17

### Separate type-specific node editors from the graph canvas

Commit message: `refactor(admin-ui): extract node inspector editors`

Scope:

- Moved the Limen, Route, Middleware, Service, and Action editors into
  `frontend/src/inspector.tsx`.
- Kept `builder.tsx` focused on graph projection, canvas navigation, node
  movement, connection gestures, and draft history.
- Preserved the standalone modal editor, Route match text area, Route
  Middleware flow/reordering controls, and type-specific parameter fields.

Verification:

- `cd frontend; npm run build`
- `cd frontend; npm run test:model`
- `go test ./...`
- `go vet ./...`
- `git diff --check`

## 2026-09-17

### Add a comprehensive configuration example

Commit message: `docs(config): add comprehensive example profile`

Scope:

- Added `configs/janus-comprehensive.example.json` covering two Limens,
  plaintext HTTP/1, TLS HTTP/1.1 and HTTP/2, HTTP/3, trusted proxy CIDRs,
  request/server/backend/shutdown settings, the admin endpoint, health checks,
  service-level in-flight admission, response buffering, request body limits,
  ordinary HTTP, combined HTTP/SSE, SSE-only, and WebSocket routes.
- Documented the new example in the repository README.
- TLS files and internal upstream names are intentionally placeholders; replace
  them with deployment-specific values before starting Janus.

Verification:

- JSON syntax parsed successfully with PowerShell `ConvertFrom-Json`.
- Full repository tests and the TLS asset-dependent `-check` command remain
  separate release checks; the example cannot pass the latter until its
  certificate and key files are supplied.

## 2026-09-17

### Record the first local forwarding smoke

Commit message: `docs: record local qualification smoke`

Scope:

- Archived a direct-backend versus Janus loopback comparison using the
  independent load generator.
- Recorded the exact scenario and limitations so the result cannot be
  mistaken for production capacity or an SLO.

Verification:

- Direct backend smoke: 200 scheduled requests at a 100 requests/s target.
- Janus forwarding smoke: 200 scheduled requests at the same target.
- Both runs completed with zero ordinary transport errors; one direct-backend
  request was classified as an end-of-window cancellation.

## 2026-09-18

### Drive Middleware editors from backend capabilities

Commit message: `feat(middleware): add dynamic capabilities and policies`

Scope:

- Added an authenticated Middleware capability endpoint and a shared backend
  catalog describing supported scopes, parameter schemas, defaults, and help
  text so the console no longer hard-codes built-in Middleware types.
- Added `headers`, `add_prefix`, and `strip_prefix` policies with scope-aware
  validation, runtime construction, regression tests, examples, and
  documentation. Header mutation rejects unsafe hop-by-hop and framing fields.
- Reworked Middleware, Service, Registry, Route, and Limen editing around
  transactional dialogs with explicit confirmation, cancellation, Escape-key
  handling, and consistent layouts.
- Fixed provisional Route and Service creation, rename/cancel behavior, stale
  editor callbacks, and preservation of complex Route match expressions.
- Updated the console UI structure and styles, development proxy behavior,
  sample configurations, and generated assets embedded by the Go server.
- Stabilized the SSE resource-release regression test under loaded test hosts
  without changing production timeout behavior.

Verification:

- `go test ./...`
- `go vet ./...`
- `cd frontend; npm run build`
- Browser checks for dynamic Middleware forms, dialog rollback, create/rename
  flows, and Registry layout
- JSON syntax validation for `configs/janus-comprehensive.example.json`
- `git diff --check`

## 2026-09-18

### Preserve proxy rewrite semantics and strengthen console validation

Commit message: `fix(middleware): preserve protocol and prefix semantics`

Scope:

- Corrected `headers` response handling so generic header rules never alter a
  `101 Switching Protocols` handshake; normal and informational HTTP response
  behavior remains unchanged.
- Corrected `strip_prefix` forwarding: route rewrites now carry trusted prefix
  metadata through the handler chain, and the proxy emits a canonical,
  composable `X-Forwarded-Prefix` only after clearing all client-provided
  forwarding headers.
- Added real WebSocket and upstream forwarding regressions, including composed
  prefix rewrites and a spoofed inbound `X-Forwarded-Prefix` value.
- Made the console filter Middleware classes by an explicitly selected Scope,
  consume generic capability field constraints during local checks, and expose
  `Header` and `Query` in the visual Route matcher.
- Added a regression that validates every hashed asset referenced by the
  embedded console HTML is served, then refreshed the generated UI bundle.
- Updated architecture, admin-console, and README documentation for these
  protocol and operator-facing behaviors.

Verification:

- `go test ./...`
- `go vet ./...`
- `go test -race ./internal/forwarding ./internal/middleware ./internal/proxy ./internal/gateway ./internal/admin`
- `cd frontend; npm run build`
- Compared the frontend bundle and embedded bundle SHA-256 hashes
- `git diff --check`

## 2026-09-18

### Restore mobile console navigation

Commit message: `fix(console): restore mobile navigation access`

Scope:

- Added a compact horizontal navigation bar below the header for viewports below
  900px, where the desktop sidebar is intentionally hidden.
- Centralized page navigation state so the desktop sidebar and mobile bar use
  the same page transition behavior and always clear an open canvas editor in
  the same way.
- Refreshed the embedded JS and CSS assets and documented the responsive
  console behavior.

Verification:

- `go test ./...`
- `go vet ./...`
- `cd frontend; npm run build`
- Compared frontend and embedded JS/CSS SHA-256 hashes
- Browser smoke at narrow width: navigated Overview → Config → canvas;
  opened the Route Middleware manager; verified backend-provided Class entries,
  Route Scope type filtering, Header/Query matcher controls, and cancel rollback
- `git diff --check`
