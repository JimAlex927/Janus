# Janus

A small Go HTTP gateway foundation, designed to remain easy to understand and modify.

**Status: runnable architecture starter, not production-ready.** The production roadmap is
in [docs/plan.md](docs/plan.md). Start with [docs/architecture.md](docs/architecture.md)
for the design and [docs/protocols.md](docs/protocols.md) for the mechanisms to learn.
The Protocol Limen layer currently owns the HTTP/1 listener lifecycle. Native
HTTP/2/HTTP/3 support and file-based configuration reload are described as later
work in [docs/limen-runtime.md](docs/limen-runtime.md).

The first release targets ordinary HTTP APIs behind an existing TLS load balancer.
Janus accepts plaintext HTTP/1.x and forwards to configured HTTP or HTTPS origins.
HTTPS upstreams use normal certificate verification and may negotiate HTTP/2.
Public TLS termination, gRPC, WebSockets, SSE, TCP/UDP, and HTTP/3 are outside this starter's contract.

## Try it

Go 1.25+ compiles this starter. Use the latest patched, supported Go release for
deployment; the minimum language version in `go.mod` is not a security recommendation.
There are no third-party dependencies. `janus` is a local module name; replace it and
internal import prefixes with your repository's module path when publishing.

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
internal/middleware/   global timeout and optional route response policies
internal/router/       immutable host and path matching
internal/upstream/     concurrent round-robin selection
internal/proxy/        reverse proxy and shared outbound transport
configs/janus.json     local example configuration
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
The original host is supplied as `X-Forwarded-Host`. Config updates require restart.

## Current limits

- No health-based removal: failed backends remain in round-robin rotation.
- No application retry loop. Go's transport can still retry certain replayable
  requests on connection failures; see the protocol guide.
- No request-body size enforcement, global concurrency limit, metrics, access log,
  or separate readiness listener yet. Server and backend deadlines are validated
  settings; `server.write_timeout` is independent from the overall request
  deadline, which actively cancels backend work. Routes stream responses by
  default; an optional route-level `buffer` middleware can hold finite responses
  up to its configured maximum before committing them.
- The immediate peer determines `X-Forwarded-For` and `X-Forwarded-Proto`.
  Behind a TLS load balancer these describe that load balancer and the internal
  HTTP hop. Original client IP/HTTPS identity needs the planned trusted-proxy policy.
- No endpoint is a tunnel: CONNECT and Upgrade requests receive 501.
- Unknown JSON fields and duplicate route matches fail validation. Go's JSON
  decoder still accepts duplicate object keys using its normal semantics; a
  stricter duplicate-key policy is a production configuration task.
- This repository contains no performance claim or completed security audit.

The development sequence and concrete release gates are in the plan. A smaller
feature set is useful only if the supported behavior is reliable under failure.

## Verification of this starter

On 2026-09-16, with Go 1.25.1 on Windows/amd64: `go test ./...`, `go vet ./...`,
the binary build, and example configuration validation passed. Tests exercise real
HTTP connections, escaped paths, bodies/trailers, forwarding-header sanitation,
HTTPS certificate trust, cancellation, route precedence, concurrent round-robin
selection, and completion of an accepted request during shutdown.

`go test -race ./...` could not build: the installed Go `runtime/cgo` tool exited
with status 2, including a retry with an explicit GCC path. Race-detector validation
remains outstanding. No load benchmark, production soak, or external security
review has been performed.
