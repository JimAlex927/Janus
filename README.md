# Janus

A small Go HTTP gateway foundation, designed to remain easy to understand and modify.

**Status: runnable architecture starter, not production-ready.** The production roadmap is
in [docs/plan.md](docs/plan.md). Start with [docs/architecture.md](docs/architecture.md)
for the design and [docs/protocols.md](docs/protocols.md) for the mechanisms to learn.
The Protocol Limen layer currently owns HTTP/1, native TLS/HTTP/2, and an
opt-in native TLS/HTTP/3 UDP listener. Versioned configurations support polled
routing reload and TLS certificate-content rotation. See
[docs/limen-runtime.md](docs/limen-runtime.md).

The first release targets ordinary HTTP APIs behind an existing TLS load balancer.
Janus accepts plaintext HTTP/1.x, and versioned configuration can enable native
TLS/HTTP/2/HTTP/3 plus explicitly scoped SSE and HTTP/1 WebSocket routes, forwarding to
configured HTTP or HTTPS origins.
HTTPS upstreams use normal certificate verification and may negotiate HTTP/2.
Public TLS termination, gRPC, HTTP/2 WebSocket extended CONNECT, and arbitrary
TCP/UDP tunnels are outside this starter's contract.

## Try it

Go 1.25+ compiles this starter. Use the latest patched, supported Go release for
deployment; the minimum language version in `go.mod` is not a security recommendation.
Runtime dependencies are pinned in `go.mod`; the HTTP/3 adapter uses quic-go.
`janus` is a local module name; replace it and internal import prefixes with your
repository's module path when publishing.

From the project root, in separate terminals:

```sh
go run ./examples/backend
go run ./cmd/janus -config configs/janus.json
```

Then visit <http://127.0.0.1:8080/api/hello> or run:

```sh
curl http://127.0.0.1:8080/api/hello
```

On Windows use PowerShell 7 or later. Stop Janus with Ctrl+C. For deployment and
signal testing, build and run the binary directly instead of using `go run`.

```sh
go run ./cmd/janus -check -config configs/janus.json
go test ./...
go test -race ./...
go vet ./...
go build -o bin/janus ./cmd/janus
```

The race detector needs a supported C toolchain. On Windows, build to
`bin/janus.exe` for a directly executable artifact.

## What exists

```text
cmd/janus/             flags, signals, startup, shutdown
internal/config/       JSON model, strict field decoding, validation
internal/gateway/      composition root and HTTP server profile
internal/limen/        protocol boundary, HTTP/TLS lifecycle and QUIC adapter
internal/middleware/   global timeout and optional route response policies
internal/router/       immutable host and path matching
internal/upstream/     concurrent selection with optional health eligibility
internal/proxy/        reverse proxy and shared outbound transport
internal/forwarding/   trusted proxy CIDRs and canonical identity headers
internal/health/       bounded active upstream probes
internal/runtime/      stable generations and versioned file reload
configs/janus.json     local example configuration
configs/janus-streaming.example.json  SSE/WebSocket route example
configs/janus-health.example.json     active upstream health-check example
examples/backend/     local test service
docs/                  architecture, delivery plan, protocol learning guide
```

Rules use exact, case-insensitive host matching with the incoming port removed.
An empty host matches any hostname. Exact-host rules take precedence over hostless
rules, then longer paths win. A prefix `/api` matches `/api` and `/api/users`, but not
`/apix`. Routing uses Go's decoded URL path; forwarding preserves the escaped path
as handled by `ReverseProxy`. No prefix stripping or slash/dot normalization is added.
Do not use path rules as an authorization boundary until normalization has been
specified and tested against your backend framework.

Upstreams are origins such as `https://service.example:8443` without a trailing
slash, credentials, or a base path. The outbound Host is the selected origin's host.
The original host is supplied as `X-Forwarded-Host`. Versioned configurations
reload routes and services by polling; listener/protocol/TLS-policy changes
still require restart. Legacy configurations remain startup-only.

## Current limits

- Optional service-owned active HTTP health checks remove failed upstreams from
  rotation and re-add them after the configured recovery threshold. Checks use
  bounded workers, per-probe timeouts, optional jitter and status-only success;
  a service with no eligible upstream returns 503. Passive failure marking and
  application-level health semantics are not enabled.
- No application retry loop. Go's transport can still retry certain replayable
  requests on connection failures; see the protocol guide.
- An optional loopback-only admin listener exposes `/livez`, `/readyz` and
  `/metrics`; readiness is cleared before graceful drain. Metrics use bounded
  route/service/error labels and target indexes, never raw paths, hosts or IDs. A fixed non-waiting global
  admission cap defaults to 1024 requests and named `in_flight` middleware adds
  a service-scoped cap; saturated requests receive 503 without queueing. Named
  `body_limit` middleware bounds request bodies at route or service scope; multiple
  applicable limits compose by the smallest cap. A fixed access observer emits
  request IDs and bounded request outcome records. Server and backend deadlines are validated
  settings; `shutdown.drain_timeout` defaults to and cannot be shorter than
  `server.write_timeout`, which is independent from the overall request
  deadline, which actively cancels backend work. An optional bounded
  `shutdown.load_balancer_removal_delay` runs after readiness is cleared and
  consumes the same total shutdown budget. Routes stream responses by
  default; an optional route-level `buffer` middleware can hold finite responses
  up to its configured maximum before committing them.
- The immediate peer determines forwarding identity by default. A versioned
  limen may explicitly configure `trusted_proxies` CIDRs; only then are valid
  X-Forwarded-For hops and trusted HTTPS scheme/host headers retained. There is
  no trust-all mode, and malformed or untrusted input falls back to the peer.
- CONNECT and non-WebSocket Upgrade requests receive 501. SSE routes stream
  `text/event-stream` responses; WebSocket routes proxy RFC 6455 upgrades over
  HTTP/1. HTTP/3 routes use the same handler and runtime generation; H3 is
  enabled only on a TLS limen with a TCP HTTP/1 or HTTP/2 fallback. Existing
  upgraded connections are tracked for bounded Limen drain, while QUIC
  connections receive the HTTP/3 server's graceful GOAWAY/close treatment.
- HTTP/3 uses a separately bound UDP socket, advertises `Alt-Svc` from the TCP
  path, disables 0-RTT, reuses the rotated TLS identity, and applies the
  bounded `limen.http3.max_concurrent_streams` setting (default 100). H3
  forwarding, local UDP behavior and serve-failure cleanup are tested; Linux
  interop, load/soak, and deployment qualification remain outstanding. A forced
  drain closes the H3 network lifecycle within its context budget; arbitrary handlers still need
  to observe request cancellation to terminate their own work.
- Unknown JSON fields, duplicate JSON object keys and duplicate route matches
  fail validation. Configuration input is bounded to 1 MiB before parsing.
- This repository contains no performance claim or completed security audit.

The development sequence and concrete release gates are in the plan. A smaller
feature set is useful only if the supported behavior is reliable under failure.

## Verification of this starter

On 2026-09-16, with Go 1.25.1 on Windows/amd64: `go test ./...`, `go vet ./...`,
the binary build, and example configuration validation passed. Tests exercise real
HTTP connections, escaped paths, bodies/trailers, forwarding-header sanitation,
HTTPS certificate trust, cancellation, route precedence, concurrent round-robin
selection, body-limit rejection for known and chunked bodies, and completion of
an accepted request during shutdown.

`go test -race ./...` could not build: the installed Go `runtime/cgo` tool exited
with status 2, including a retry with an explicit GCC path. Race-detector validation
remains outstanding. No load benchmark, production soak, or external security
review has been performed.
