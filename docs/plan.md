# Delivery plan for Protocol Limen, runtime reload, and HTTP policies

## Objective

A small, auditable HTTP gateway that an individual can maintain.
The current deployment profile supports private HTTP/1.x, native HTTPS/HTTP/2,
and opt-in native HTTPS/HTTP/3 listeners through Protocol Limen. The stable
runtime dispatcher and versioned file-based routing reload are implemented;
HTTP/3 adapter work is in progress. Backend apps retain
their business authorization responsibility.
Support remains scoped to ordinary bounded APIs plus explicit SSE and classic
HTTP/1 WebSocket routes; adding an HTTP version does not add streaming RPC or
tunnel support. Concrete ownership and update rules are in
[limen-runtime.md](limen-runtime.md).

Adopt built-in HTTP middleware composed at startup, with named policy definitions
and ordered route/service references introduced alongside their implementations.
The composition contract, package ownership, and proposed configuration are in
[architecture.md](architecture.md). These are Janus design decisions inspired by
[Traefik middleware composition](https://doc.traefik.io/traefik/reference/routing-configuration/http/middlewares/overview/).
They do not imply configuration compatibility with Traefik.

This document separates implemented work from future phases. The accepted
configuration now includes consumed timeout, admission, admin, named middleware,
service health-check, trusted-proxy and metrics fields; remaining phases add deployment
qualification rather than speculative unused settings.

## Current baseline and the original item1

As of 2026-09-16, the baseline has host/path routing, round-robin services,
streaming reverse proxying, HTTPS certificate verification, typed server and
transport settings, a startup-built middleware chain, request-context
cancellation, the `internal/limen` HTTP/1 lifecycle, and native TLS/HTTP/2/HTTP/3
bindings. Shutdown uses a validated configurable drain budget. Body limits,
admission, access observation, optional admin endpoints and service-owned active
health checks, trusted forwarding and metrics are implemented. Versioned
routing reload and TLS certificate rotation are
now implemented; legacy single-file mode remains startup-only.

The original item1 was settings/deadline configuration, not all of Phase 1.
Its main implementation exists; Phase 1 below completes its migration and
deadline contract. The handler context deadline now comes from the startup-built
timeout middleware, while `server.write_timeout` supplies the independent socket
write deadline. The default write budget is 5 seconds longer than
`request.maximum_duration`; an omitted write setting derives the same headroom
from a customized overall budget. Server write deadlines allow the same 5-second
headroom above the 24-hour overall maximum. Real-socket coverage demonstrates
504s before commitment and incomplete responses after commitment. Slow-upload
and slow-reader tests exist but do not establish all claimed deadline behavior;
1Q qualification now separately verifies the server read deadline, server write
deadline, and preservation of an earlier parent deadline.

Keep only settings with runtime consumers. Previously removed body-limit,
capacity, SLO, and trusted-proxy fields return only with their implementations.
Workload assumptions and latency targets belong in deployment documentation.

## Decisions to record before sizing

Specify deployment platform, peak and sustained requests/second, concurrency,
payload distribution, maximum upload size, normal/maximum request duration, route
count, number of backends, availability objective, and acceptable added p99 latency.
Also record TLS ownership, whether backends require mTLS, and whether authentication
lives at the gateway or in services. Choose numbers from real workloads; there is
no defensible universal RPS promise for a Go gateway.

## Milestones and exit criteria

Work sequentially. Each phase must produce working behavior and its failure tests,
not just configuration types. Size effort after each phase's exit review.

| Phase | Goal | Exit criteria |
| --- | --- | --- |
| 0: contract | Adopt the architecture and record deployment assumptions | Ordering, scopes, timeout semantics, unsupported protocols, and state ownership are explicit |
| 1: middleware foundation | Implemented chain, startup assembly and timeout migration | Qualification reopened as 1Q; implementation checkpoint is retained |
| 1Q: deadline qualification | Correct slow-upload/reader and parent-deadline evidence | Complete: isolated deadlines, cancellation and response outcomes verified |
| 2A: Protocol Limen | Extract existing HTTP/1 listener and lifecycle | Complete: legacy config/forwarding retained; Limen owns bind, serve and drain |
| 2B: runtime generations | Stable dispatcher and explicit resource ownership | Complete: old requests retain old handlers; new requests use new handlers; retirement is bounded and race-safe |
| 2C: HTTPS and HTTP/2 | TLS Limens, protocol configuration, response capability audit | Complete: actual H2 negotiation, sibling-stream isolation, H1 fallback and TLS startup checks pass |
| 2D: dynamic files and certificates | Serialized validated routing reload; independent certificate rotation | Complete: invalid updates preserve last-good state; runtime and certificate resources remain bounded |
| 2E: long-lived HTTP protocols | Explicit SSE and classic HTTP/1 WebSocket route modes | Complete: event flush, upgrade/frame forwarding, timeout bypass, and bounded upgraded-connection drain pass |
| 2F: usable request policies | Named body-limit policies, request IDs, access observation | Policies compose in order; early rejection, upload limits, trailers, and incomplete responses covered on H1/H2 |
| 3: bounded operation | Global/service admission, admin readiness, configurable drain | Complete: overload, shared service limits, readiness-first stop-admission, bounded removal delay and total shutdown budget |
| 4: backend and trust policy | Active health checks, trusted forwarding identity, metrics | Complete for the bounded HTTP metrics/trust scope; production qualification remains Phase 6 |
| 5: HTTP/3 and deployment lifecycle | QUIC adapter reusing runtime; deployment artifacts | H3 forwarding, reload, cancellation, TLS rotation, UDP failure/fallback and coordinated drain tests pass |
| 6: production qualification | Linux CI, security review, realistic load/soak tests, canary | All release gates pass for a named build and environment |

Completed order is 1Q -> 2A -> 2B -> 2C -> 2D -> 2E -> 2F -> 3 -> 4 bounded scope.
Phase 2C delivers the first native H1/H2 milestone; Phase 2D adds dynamic-file
updates and certificate rotation. Phase 5.1 now adds the native H3 adapter;
remaining Phase 5 work is socket-failure, interop, deployment and qualification
coverage. All production claims still require Phase 6 qualification for
the enabled protocol set; neither development milestone is production certification.

## Phase 0 decisions — complete

The initial HTTP/1 baseline contract is recorded below. The expanded target
uses the Limen and reload contracts in [limen-runtime.md](limen-runtime.md).
The deployment boundary and workload decisions below describe the original
Phase 0 profile; Phase 2C now adds native TLS/H2 startup support.

| Area | Decision |
| --- | --- |
| Deployment boundary | Janus listens on a private HTTP/1.x interface behind an existing TLS load balancer; public TLS termination is outside Janus. |
| Workload | Ordinary bounded-duration HTTP APIs; streaming is the default response behavior, while SSE, WebSocket, gRPC, HTTP/2 listener mode, HTTP/3, and TCP/UDP tunnels are outside this contract. |
| Identity and authorization | The backend owns business authorization. Forwarded identity is trusted only from per-limen CIDRs and a validated multi-hop chain; otherwise Janus rewrites headers from the immediate peer. |
| Routing | Exact case-insensitive host rules without ports take precedence over hostless rules; the longest segment-bounded path prefix wins. No prefix stripping or path normalization is performed. |
| Time budgets | The starter profile uses 5s header read, 30s request read, 30s overall context, 35s server write, and 10s backend response-header budgets. `server.write_timeout` stays greater than the overall budget. |
| Size and overload | Header/body size bounds, fixed/service admission and active health-based removal are implemented; drain is configurable and bounded. |
| Composition and ownership | A fixed global chain wraps the router; matched routes use ordered route middleware; each service owns one shared proxy/pool; the transport is shared by services with the same policy. |
| Failure semantics | Unmatched requests are 404, unsupported CONNECT/Upgrade requests are 501, upstream failures are 502, and pre-commitment overall timeouts are 504 when the socket is writable. Committed responses are never rewritten. |

The concrete examples reviewed for this contract are: `/api` matches `/api`
and `/api/users` but not `/apix`; an exact `example.com` host beats a hostless
route; `/api/a%2Fb` routes using the decoded path while forwarding the escaped
path; client forwarding-identity hints are removed; and incomplete or
unsupported protocol requests do not bypass routing policy. These cases are
covered by router and real-listener tests.

Phase 0 is complete for the repository contract. Actual production RPS,
concurrency, payload distribution, availability target, and added-p99 target
remain deployment-owner inputs for Phase 6 qualification; Janus does not claim
universal values for them.

## Phase 1 tasks and remaining item1 work

1. Add `internal/middleware` with `Middleware func(http.Handler) http.Handler`
   and an ordered `Chain`. The chain now has entry/exit-order, empty-chain,
   nil-entry, and cancellation tests. Middleware calls its next handler at most
   once; retry is outside this initial contract.
2. Move the existing overall context deadline from `Gateway.ServeHTTP` into a
   synchronous timeout middleware. The chain is built once in `gateway.New`,
   while protocol rejection and router matching remain unchanged.
3. Keep the current JSON keys for this extraction. Document durations (`250ms`,
   `5s`, `2m`), the current 1ms–24h bounds, and the existing rule that omitted or
   zero numeric settings select defaults. Do not silently reinterpret zero as
   disabling a timeout. Document header-size and connection-count bounds too.
4. Separate the server write deadline from the overall context budget with the
   consumed `server.write_timeout` field. The default and omission fallback use
   five seconds of response-write headroom, and validation rejects
   `write_timeout <= maximum_duration`. The write setting may extend to 24h +
   5s to preserve the default headroom. The overall budget starts at handler
   entry; header reading has its own earlier deadline. It cancels outbound work
   but does not forcibly stop arbitrary application code.
5. Real-listener tests cover backend header stall, stalled response body,
   client cancellation, pre/post-commitment deadline behavior, server-side slow
   upload termination, and server-side slow-reader termination. Timeout itself
   does not buffer responses.

Phase 1 implementation is present; its qualification is reopened as 1Q after
reviewing the existing test assertions. It introduces the named route policy `buffer` to prove route
handler assembly. It is optional and bounded by `max_response_body_bytes`;
routes without it retain streaming behavior. HTTP server settings and backend
transport settings remain infrastructure configuration. Broader policy types,
service attachments, and the complete response-capability contract remain in
Phase 2.

## Phase 1Q: repair qualification evidence — complete

The qualification tests now deliberately isolate each deadline. The slow-reader
test does not read the response and observes the handler's socket write timeout;
the slow-upload test reads an incomplete body and observes the server read
timeout; the middleware test confirms an earlier parent deadline is preserved
and still cancels the request. HTTP does not transmit a Go deadline timestamp to
a remote backend, so parent-deadline evidence is cancellation-based.

## Phase 2A: Protocol Limen extraction — complete

`internal/limen` now owns the current HTTP/1 listener and `net/http` server
lifecycle. `cmd/janus` constructs it, binds through `Limen.Listen`, serves
through `Limen.Serve`, and drains through `Limen.Shutdown`. `gateway.Gateway`
remains an `http.Handler` and no longer constructs or owns the inbound server.
The legacy JSON configuration, route middleware assembly, forwarding behavior,
and fixed 35-second process drain budget are unchanged. Native TLS/HTTP/2 is
now delivered by Phase 2C; runtime generations are delivered by Phase 2B and
file reload remains in Phase 2D.

## Phase 2B: stable runtime and generations — complete

`internal/runtime` now provides the stable HTTP dispatcher and in-memory
generation replacement. The fixed protocol guard and overall-timeout chain is
constructed once outside the replaceable Gateway route graph. Runtime owns one
process-level outbound transport and injects it into each generation; a
generation never closes that shared transport.

Request acquisition and retirement use one lock, so a generation cannot be
closed between selecting it and incrementing its reference count. Old
generations close only after their active requests release them. Replacement
build failures close any candidate generation and retain the previous one;
panic paths release references; and eight concurrently retired generations are
bounded. Listener/server settings remain startup-owned. Versioned file watching
and certificate rotation are delivered in Phase 2D.

## Phase 2C: HTTPS and HTTP/2 — complete

Versioned startup configuration now supports named Limen bindings with explicit
`http1`/`http2` selection and certificate/key files. TLS Limens use Go
`net/http` with ALPN `h2` and `http/1.1`; plaintext legacy HTTP/1 remains
available, while unencrypted H2 is deferred. Runtime and Gateway are shared by
all bindings, and route references can scope a route to a named Limen.

The implementation verifies certificate loading, real H2 negotiation, HTTP/1.1
fallback, concurrent stream isolation, named route scope, graceful startup
cleanup, active H2 stream drain through Limen shutdown, and outbound HTTP/2
enablement. Long-lived protocols still require a
separate timeout, buffering, upgrade, and drain contract.

## Phase 2D: file reload and certificate rotation — complete

Read bounded, strict routing documents, build a full candidate, then publish
through Phase 2B. Add a portable polling trigger with hash-based deduplication
and serialized/coalesced reloads. Atomic file replacement is the supported write
workflow. Invalid, missing or unreadable input retains the previous generation.
Expose generation and failure information in logs initially. Reload routing
without restarting sockets, closing connections, or issuing GOAWAY.

The implementation polls versioned configuration with a bounded read and
content-hash deduplication. It serializes reload attempts and publication
through Runtime, retains the last good generation for invalid/missing/
startup-changing input, and keeps
legacy single-file startup-only mode unchanged. TLS certificate/key contents
are polled independently; a complete validated pair is atomically published to
new handshakes while established connections remain intact. Listener addresses,
protocol sets, global limits, TLS policy and outbound transport settings still
require restart.

Coverage includes invalid and partial config updates, startup-setting rejection,
generation replacement, malformed certificate-pair retention, successful
certificate rotation, certificate validity rejection, and bounded configuration
input. Full H1/H2 response
capability and GOAWAY qualification remain in later protocol work.

## Phase 2E: long-lived HTTP protocols — complete

Routes can explicitly declare `sse` or `websocket` in their `protocols` list.
SSE uses the normal HTTP response path with immediate event flushing. Classic
HTTP/1 WebSocket upgrades are forwarded through `ReverseProxy`; arbitrary
upgrades, CONNECT, and HTTP/2 extended CONNECT remain rejected. Long-lived
requests bypass the bounded API timeout and finite write deadline, while Limen
tracks upgraded connections and force-closes them when the drain budget expires.
The route buffer middleware bypasses both modes.

## Phase 2F: request policies (in progress)

1. Add request ID and a single access observer in the fixed global chain. Generate
   IDs by default; define validation and trust before accepting client-supplied
   IDs. Record route/service IDs, final status, duration, consumed request bytes,
   written response bytes, and error class, including early 404/413/501/502/504.
   Keep authorization, cookies, bodies, and raw queries out of logs. The fixed
   observer, regenerated IDs, route/service metadata, bounded fields, early
   error classification path and 404/413/501/502/504 integration coverage are
   implemented.
2. Extend the Phase 2C response capability tests through observation: `Unwrap`, flushing,
   trailers, informational responses, implicit 200, and copy/error accounting.
   Test `ResponseController`; do not advertise unsupported optional interfaces.
   The observer preserves `Unwrap`, supported Flusher/Hijacker/Pusher
   capabilities, informational/final status handling, controller flushing,
   trailers, partial writes, response-copy abort accounting and authoritative
   gateway response IDs. The current
   wrapper intentionally does not expose `ReaderFrom`; normal `Write` paths
   remain counted.
3. Extend typed named middleware definitions and ordered route/service references.
   `body_limit` is implemented at route and service scope. Missing references,
   unknown types/options, multiple types per definition, and duplicate references
   within a list fail validation before opening listeners. Scope compatibility is
   currently explicit: the built-in policies are valid at both scopes.
4. Enforce known-length limits before forwarding; enforce chunked and unknown-length
   limits while streaming. Return 413 when still possible; terminate an already-
   started response otherwise. A backend may have received a prefix of an oversized
   body. Do not add retries. Multiple route/service body caps use the smallest cap.
5. Preserve configurations without middleware references. A runnable body-limit
   example and end-to-end tests for short-circuit behavior and chunked forwarding
   are now present. The current 2F request-policy scope is complete; later
   admission, metrics, health and trusted-proxy policies remain separate phases.

The first 2F delivery is the bounded request-body policy. It is intentionally a
request-size guard, not an upload-duration or global admission policy: server read
deadlines and the later Phase 3 admission controls remain independent.

Phase 2F is complete for the currently defined request-policy scope. Its release
evidence includes route/service body limits, fixed access observation, response
capability tests, early-error integration coverage, configuration examples and
the full repository test/vet/build gates. This does not certify the overall
gateway for production; Phase 6 qualification remains required.

## Phase 3: admission and lifecycle — complete

1. Fixed global admission and a service-scoped `in_flight` policy are implemented.
   Both reject saturation immediately with 503 and have no waiting queue. The API
   profile defaults to a bounded global cap of 1024. Permits release on normal
   return, cancellation and panic unwinding; connection-pool limits are not
   admission.
2. Runtime owns one service limiter per stable service identity. All routes
   targeting it share the cap, identical policy names on different services have
   independent counters, and old/new generations share the counter during reload.
   Candidate limit changes are applied only at publication, so failed reloads do
   not mutate the active policy. Cross-generation and lowered-limit tests cover
   this behavior.
3. The separate loopback/private admin listener with `/livez` and `/readyz` is
   implemented. Readiness becomes true only after all configured business
   listeners bind and start, and is cleared before graceful drain. Admin health
   requests bypass business admission; service health affects target selection,
   not process readiness, so one unhealthy service does not make the whole
   gateway unready.
4. Configurable drain budget is implemented through
   `settings.shutdown.drain_timeout`; omitted values follow `server.write_timeout`
   and shorter values are rejected. Shutdown marks readiness false and stops
   business admission first, optionally waits the bounded
   `shutdown.load_balancer_removal_delay`, then allows accepted work to finish
   within the same total budget before Limen force-closes remaining connections.

## Phase 4 tasks

1. Complete: service-owned active probes use interval, timeout, jitter,
   consecutive failure/recovery thresholds, and all-unhealthy 503 behavior.
   Response bodies are closed after a bounded drain, worker count is capped,
   and probe lifecycle is independent of requests. Candidate generations start
   probes before publication and stop them when their resources retire.
2. Complete: trusted-proxy CIDRs are configured per limen with no insecure
   trust-all mode. Identity is derived from the immediate peer and a validated
   right-to-left multi-hop chain; proxy rewrite emits canonical XFF/XFP/XFH.
   Untrusted spoofing, malformed chains, trusted HTTPS scheme propagation and
   reload-safe limen policy comparisons are covered.
3. Complete: `/metrics` on the private admin listener reports requests, errors,
   duration, in-flight, rejections, backend health, drain duration and reload
   outcomes. Labels use bounded route/service/error identifiers and target
   indexes; raw paths, hosts, user IDs and request IDs are excluded. It reuses
   the access observation outcome model.

## Phase 5: HTTP/3 and deployment lifecycle

The former Phase 5 reload work moves to 2B/2D. Admission added in Phase 3 must
retain active service permit accounting across generations, including service
remove/re-add; lowering a cap cannot create fresh capacity. Health workers added
in Phase 4 also need retirement/reload tests when introduced.

1. Complete for the first adapter milestone: pin quic-go v0.61.0 for Go 1.25,
   add the HTTP/3 adapter inside Limen, and reuse its `http.Handler` integration
   with the existing dispatcher. Inbound H3 may proxy to H1/H2; outbound H3
   remains deferred.
2. Partially complete: coordinate TCP HTTPS and UDP/QUIC sockets, TLS identity,
   `Alt-Svc` advertisement and TCP fallback. The current adapter binds both
   sockets, disables 0-RTT, handles `:0` UDP advertisement, and cleans up
   startup and serve-failure paths. A local injected UDP read failure also
   stops the TCP fallback; broader coordinated fault and deployment tests remain.
3. Partially complete: local H3 stream isolation, client cancellation, and
   forwarding pass on the supported development platform. The bounded H3
   bidirectional stream limit is now wired and configuration-tested; H3
   certificate rotation is also covered for existing and new connections.
   Reload across concurrent streams on an existing QUIC connection and
   coordinated bounded shutdown are covered by forced local-drain tests.
   H3 buffer/stream response semantics, broader fault-injection, deployment and
   Linux interop remain.
4. Partially complete: ship a minimal native systemd artifact with non-root
   execution, explicit starter resource budgets, restart/drain settings,
   rollout/rollback instructions, and `-print-effective-config` inspection that
   normalizes defaults while excluding TLS asset paths. Docker/container images,
   verified CA-root packaging, Linux execution, measured resource budgets and
   canary rollback evidence remain. Add per-service transport/TLS policy only
   when required.

## Phase 6 tasks

The repository now defines a pinned Ubuntu CI job for the release gates: full
tests, the race detector, vet, and a static Linux build. A real CI run is still
required before treating those checks as release evidence. Add Linux
signal/socket tests, malformed framing, ambiguous paths, middleware composition,
and repeated failed/successful reload coverage. A configuration-parser fuzz
target and a short CI fuzz smoke are now present; longer fuzz campaigns remain
required. Perform representative load, a 24-hour soak, and a canary with
rollback criteria.

## Suggested implementation commits

Start with 1Q qualification fixes, then separate commits for Limen extraction,
runtime generation ownership, HTTPS/H2, file reload, and TLS pair rotation.
Follow with the observer/body-limit work in 2F and the existing operation phases.
HTTP/3 lands as its own adapter and integration-test changes in Phase 5.
Each implementation commit updates [commit-log.md](commit-log.md) with actual
changes and verification. Do not describe planned capabilities as shipped.

## Release gates

| Area | Required evidence |
| --- | --- |
| Protocol correctness | A published support matrix, malformed framing tests, header/trailer tests, path ambiguity tests, HTTPS identity verification and fuzzing |
| Resource bounds | Measured heap, goroutine, socket and queue bounds under overload, idle-connection pressure, slow uploads and slow readers |
| Failure semantics | Dead/refusing/slow/flapping backends; cancellation propagation; healthy failover policy; stable 502/503/504 behavior |
| Configuration | Typo/reference/duplicate rejection; invalid reload preserves old state; repeated reloads do not leak workers or transports |
| Lifecycle | Readiness drops before drain; accepted bounded requests finish; process exits within the orchestrator grace period |
| Security | Supported patched Go toolchain, `govulncheck`, dependency/license review, reviewed trust boundaries, tested TLS settings, operator access restrictions |
| Operations | Actionable dashboards/alerts, redacted logs, rollout/rollback procedure and an incident drill |
| Performance | Reproducible same-hardware comparison plus a 24-hour representative soak without unexplained resource growth |

`go test`, the race detector and `go vet` are necessary checks, not production
certification. Add automated Linux CI because production behavior, signal handling
and socket limits may differ from development on Windows. Pin tools/build inputs
and deliberately refresh them for security fixes.

## Performance methodology

Measure a direct backend baseline, Janus, NGINX and Traefik with equivalent routes,
TLS placement, protocols, headers, buffering, logging and limits. Record versions,
configuration, hardware, OS, CPU quotas and load-generator location. Run generators
outside the gateway process and ensure they are not the bottleneck.

Use small and large bodies, downloads and uploads, connection reuse and churn,
1/100/1,000 routes, varying concurrency, delayed backends and a failing backend.
Run HTTP/1 and HTTP/2 scenarios separately when supported. Include arrival-rate
tests so overload latency is not hidden by clients slowing their request rate.

Report throughput **alongside** p50/p95/p99 latency, error rate, CPU, RSS/heap,
allocations, goroutines, live connections and rejection count. Measure latency
added over the direct baseline. A high RPS result with excessive errors is a failure.
Use multiple runs after warmup and retain raw results. Choose pass/fail thresholds
from phase 0, including headroom above measured peak traffic.

Use CPU/heap profiles and traces to decide whether routing lookup, copies, logging,
allocation or backend waiting dominates. Replace the simple O(number of routes)
matcher with host buckets and a prefix tree only if realistic profiles justify it.
Avoid custom allocators, speculative `sync.Pool` use or `fasthttp` migration before
the standard-library implementation has a measured limitation.

## Deliberately deferred

Authentication middleware, client quotas, retries, circuit breakers, and reusable
named chains follow a concrete use case after the core lifecycle is reliable.
Health checks and circuit breakers solve different problems; implement health
selection plus admission first. Authentication needs its own identity/trust and
failure contract, and retry needs bounded replay and idempotency rules.

Plugin marketplace, scripting, dashboard, Kubernetes/Docker discovery, distributed
configuration store, caching, WAF rules, transformation language, ACME, outbound
HTTP/3, unencrypted HTTP/2, HTTP/2 WebSocket extended CONNECT, gRPC, arbitrary
TCP/UDP proxying, and a general policy engine. Each expands the security
and operational contract substantially. Add one only after defining its owner,
tests and failure behavior.
