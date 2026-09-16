# Protocol Limen and dynamic configuration

Status: implementation plan reviewed against the repository on 2026-09-17.
Phase 1Q qualification, the Phase 2A HTTP/1 Limen extraction, the Phase 2B
stable runtime/generation core, and the Phase 2C TLS/HTTP/2 startup path are
shipped; the Phase 2D routing file reload and certificate rotation path is also
shipped. The first Phase 5 native HTTP/3 adapter is now shipped. Explicit SSE
and classic HTTP/1 WebSocket routes are supported, and the typed `body_limit`
middleware is available at route/service scope, and fixed global/service
admission is available. H3 interop/deployment qualification and the remaining
long-lived protocol contracts remain future work. The fixed global request observer and
admission are outside replaceable generations, so reloads do not change request
ID generation or global permit ownership. Shutdown clears readiness and stops
business admission before the optional load-balancer removal delay. That delay
and the subsequent Limen drain share one bounded context, so the configured
grace period is a total budget.
The delivery sequence and exit gates are in [plan.md](plan.md).

## Scope

Support ordinary bounded HTTP API requests over HTTP/1.1, HTTPS/HTTP/2, and
HTTPS/HTTP/3. Reuse the existing router, middleware chain, and reverse proxy.
Support file-based routing updates without restarting listeners or canceling
requests already executing. Protocol enablement and reload are separate changes.

An HTTP version describes the wire protocol, not the duration of a request.
HTTP/2 and HTTP/3 multiplex requests on persistent connections. SSE and classic
HTTP/1 WebSocket proxying are supported through explicit route protocol modes.
WebSocket extended CONNECT over HTTP/2, gRPC, CONNECT tunnels, and raw TCP/UDP
forwarding need separate contracts and remain outside this delivery. Raw TCP/UDP
cannot generally be converted into an HTTP request. Enabling HTTP/2 alone does
not establish gRPC support.

## Three responsibilities

| Component | Owns | Does not own |
| --- | --- | --- |
| `internal/limen` | Listeners, inbound TLS, protocol servers, protocol-specific limits, coordinated drain | Route matching, backend selection, configuration publication |
| `internal/runtime` | Stable dispatcher, active configuration generation, reload serialization, resource lifetime | HTTP frame parsing or middleware implementations |
| `internal/gateway` | Building a generation of routes, middleware and service handlers | Opening listeners or swapping the live generation |

`cmd/janus` composes these components and handles process signals. `config`
decodes and validates input; it does not build handlers. Phase 2C keeps the
versioned startup and routing data in one JSON document. Phase 2D can split the
routing source when it adds file watching; Docker/Kubernetes Providers and
generic manager frameworks are unnecessary for this scope.

The following is the target request path; future policy nodes are added only
when their implementation phase lands:

```text
HTTP/1.1 or HTTPS/HTTP/2 (net/http) --+
                                    +--> stable Handler
HTTP/3 (quic-go/http3) --------------+      -> fixed global chain
                                           -> acquire active generation
                                           -> router for this Limen
                                           -> route middleware
                                           -> shared service handler
                                           -> outbound transport -> backend
```

Every protocol uses the same runtime and global policies. Each request acquires
one generation at dispatcher entry, then releases it with `defer`, including
panic paths. A small per-listener handler attaches a trusted Limen ID in context;
clients cannot select it through a header. Routes may bind to named Limens.
TLS/QUIC parsing stays in protocol libraries. Protocol guards that depend on an
HTTP request remain within the observed HTTP chain.

## Code migration

1. Complete the current HTTP/1 extraction in `internal/limen`. It now provides
   `New`, `Listen`, `Serve`, `Shutdown`, and `Close`; `cmd/janus` uses it while
   the legacy configuration and HTTP/1 behavior remain unchanged. Future Limen
   adapters must bind required sockets before reporting success and close all
   partial resources if any binding fails.
2. Add a stable runtime handler. `internal/runtime` now extracts the fixed
   global timeout/guards from generation construction, so they are built once.
   Later global observation and admission also live outside generation
   replacement.
3. Refactor the gateway builder to accept an injected shared transport and
   return an immutable handler generation with explicit cleanup of resources
   it owns. The runtime now keeps the process-owned transport alive across
   replacements; standalone `gateway.New` retains an owned transport for direct
   use and tests.
4. Add TLS and HTTP/2 to Limen. The native startup path is complete; routing
   file changes and certificate rotation are wired through the runtime
   transactions in Phase 2D.
5. Add HTTP/3 to Limen only as an opt-in TLS binding with a TCP fallback. The
   first adapter now exists; interop, fault and deployment qualification remain.

Limen depends on `http.Handler`, not the gateway builder. Runtime may call the
gateway builder; gateway must not import runtime. Add files/packages only as
their functionality is implemented.

## Configuration boundaries and migration

Retain the current single JSON document as legacy startup-only mode. The
versioned format introduced in Phase 2C keeps startup and routing data in one
document, with:

- Named `limens`, fixed global settings, outbound transport settings, and drain
  budget.
- The existing `routes`, `services`, and `middlewares` model in the same
  document; route attachments may reference named Limens.
- Route protocol modes: omitted or `http` for ordinary requests, `sse` for
  EventSource responses, and `websocket` for classic HTTP/1 upgrades. A route
  must opt into the long-lived modes explicitly.
- TLS identity: one configured certificate/key pair per TLS Limen initially.
  Multi-certificate SNI selection, mTLS policy reload, and ACME come later.

The versioned startup schema and consumer are implemented in Phase 2C; the
TLS example is [configs/janus-tls.example.json](../configs/janus-tls.example.json).
Legacy `listen` normalizes to a single internal Limen. Mixed legacy/new syntax
fails rather than silently choosing one source. TLS file paths resolve relative
to the referencing configuration file. A route without a Limen reference uses
the sole configured data Limen; with multiple Limens, routes require explicit
references. Missing references and ambiguous host/path matches within one Limen
scope are rejected before startup.

| Setting | Update policy in the first implementation |
| --- | --- |
| Limen addresses, protocols, TLS enablement, socket/stream limits | Restart |
| Global timeout, observer options, global admission, admin address, drain budget | Restart |
| Outbound transport/TLS options and pool limits | Restart; shared process-owned transport |
| Routes, Limen attachments, middleware definitions/references, service targets | Transactional routing reload in Phase 2D |
| Certificate/key contents at configured paths | Separate validated pair rotation in Phase 2D; new handshakes use it |
| Certificate paths, TLS versions, trust roots, mTLS policy | Restart initially |
| New configuration fields for future middleware | Introduce only with implementation and reload/state tests |

Routing and certificate rotations are independent transactions initially; there
is no promise of an atomic change spanning both. Adding a host whose identity
is not covered by the existing certificate requires coordinating certificate
deployment first. Existing TLS connections do not renegotiate on rotation.
Do not mutate a live `tls.Config` or certificate object.

## Generation publication and lifetime

Reload means new requests use a new handler graph while old requests finish
using the graph they acquired. It is per request/stream, not per connection:
an existing HTTP/1 keepalive connection or HTTP/2 connection can use the new
generation for its next request while an earlier request still uses the old one.

Implement publication as a serialized transaction:

1. Read one bounded, complete routing document. Reject duplicate keys, unknown
   fields, extra JSON documents, and invalid policy or Limen references.
2. Build the entire candidate off the request path. Failure releases candidate
   resources only and preserves the previous active generation.
3. Under a short publication lock, install the candidate and retire the prior
   generation. Request acquisition/increment and retirement must use the same
   synchronization; a bare atomic pointer plus a later reference increment is
   insufficient. Never hold this lock during I/O or `ServeHTTP`.
4. Wait for old request references to reach zero before cleaning up old-owned
   resources. Shared transport survives generation retirement. Request contexts
   derive from client requests, not a short-lived reload context.
5. Record applied generation/hash, success or error, and retirement count without
   logging secrets. Suppress unchanged successful input; serialize/coalesce
   overlapping updates so older builds cannot overwrite newer applied versions.

The first trigger is bounded polling of the routing file, portable to Windows
and Linux. Operators publish a complete file by atomic replacement. Polling must
handle replacement, disappearance, and unreadable/invalid files while preserving
last-good state. A hash identifies input; it does not prove that an in-place
partial write was intended. Test the reload function directly before polling.
No unauthenticated network reload endpoint is needed.

Limit pending builds and retired generations. If an old handler does not return,
report the condition and reject/defer further reloads at the configured bound;
do not prematurely free resources still in use. Candidate publication failures
are retried on the next poll while unchanged failure logs are hash-deduplicated.
Shutdown has a separate bounded
drain and force-close path. A context deadline alone cannot kill arbitrary Go code.

Runtime owns counters keyed by stable service identity. Old and new generations,
and remove/re-add of the same service while an old generation is alive, share
active permit accounting. A lower limit rejects new acquisitions until usage
falls. Counter lifetime is not definition lifetime. Shutdown clears readiness and
stops business admission before the optional removal delay; the delay and Limen
drain share one total bounded context. Health workers added later must have
explicit generation ownership or reference-counted sharing.

## HTTP/2 delivery — complete

Go `net/http` handles HTTP/1.1 and HTTP/2, `crypto/tls` handles the Limen
certificate, and the versioned startup schema selects the enabled protocols.
TLS Limens advertise ALPN `h2` and `http/1.1`; a plaintext legacy HTTP/1 Limen
remains available behind an external TLS terminator. Unencrypted HTTP/2 remains
deferred. Inbound and outbound protocol selection are independent, and the
outbound transport explicitly preserves HTTP/2 after installing its custom
dialer. See [Go 1.25 HTTP protocol and server APIs](https://pkg.go.dev/net/http@go1.25.0).

The implemented 2C evidence demonstrates:

- TLS certificate verification, actual negotiated `h2`, and HTTP/1.1 fallback.
- Concurrent streams on one verified shared connection; cancellation, body
  failure or deadline in one stream leaves another stream functional.
- H1/H2 selection on the inbound Limen, H2 negotiation on the outbound
  transport, and route scoping by named Limen.
- Bind-before-serve startup cleanup and the existing server deadline behavior.

The full H1/H2 forwarding matrix, response-writer capability audit, protocol-
specific stream limits, and graceful GOAWAY/force-close qualification remain
follow-up coverage. Routing reload alone must send no GOAWAY and must not
restart a listener when Phase 2D is implemented.

Audit response wrappers as part of this work. Transparent observers should
preserve supported controller operations. Buffering deliberately suppresses
flush; blindly adding `Unwrap` may let controller operations bypass that policy.
Define and test buffer header snapshots, informational responses, trailers,
commit write errors, and deadline checks before publishing a successful result.
Do not promise every optional ResponseWriter interface on every protocol.

## HTTP/3 delivery

Pin a compatible supported Go/quic-go combination at implementation time.
Janus currently pins quic-go v0.61.0 for Go 1.25. `quic-go/http3.Server` accepts
the same HTTP handler, so the UDP/QUIC adapter stays inside Limen while keeping
the dispatcher and gateway graph. QUIC integrates TLS 1.3. Library docs show
handler reuse, Alt-Svc, and graceful shutdown:
[quic-go HTTP/3 server](https://quic-go.net/docs/http3/server/).

Enable TCP HTTPS and UDP HTTP/3 together on a named TLS Limen, normally the same
numeric port. The current adapter binds both before readiness, advertises the
actual UDP port (including `:0`), shares the certificate rotation callback,
keeps HTTP/1.1 and HTTP/2 available for fallback, and leaves 0-RTT disabled.
The validated `http3.max_concurrent_streams` setting bounds bidirectional
request streams per QUIC connection. Startup and rotated certificate chains are
parsed, checked for current validity, and checked for server authentication
before publication. Certificate rotation is covered locally:
existing QUIC connections remain usable and a new handshake observes the new
certificate. Startup cleanup and coordinated drain are implemented;
the forced-drain path returns within its caller context even when a handler is
not cooperative, while that handler may continue until it observes request
cancellation. Runtime reload on an existing QUIC connection is locally covered:
an in-flight old stream retains its old generation while a concurrent new stream
uses the replacement generation. The Limen also closes its application-owned
UDP socket and starts H3 force-close when either protocol serve loop fails.
The local fault path additionally verifies that a UDP read failure stops the
TCP fallback. Broader fault-injection and public-port deployment tests remain.

Test negotiated H3, stream cancellation isolation, stream/connection flow-control
limits, handshake/idle/drain budgets, TLS pair rotation, reload on existing QUIC
connections, UDP bind failures and TCP fallback. Inbound H3 can proxy to H1/H2
backends; outbound H3 is outside this milestone. Use actual H3 integration tests
on Linux as well as the supported development OS before claiming support.

## Qualification evidence completed in 1Q

The deadline evidence is now isolated in `internal/limen/limen_test.go` and
`internal/middleware/timeout_test.go`. A deliberately non-reading client makes
the Limen handler's response write terminate with the configured server write
deadline; an incomplete upload makes body reading terminate with the configured
server read deadline; and the parent-deadline test observes cancellation without
claiming that HTTP transmits Go context deadline timestamps to backends.
