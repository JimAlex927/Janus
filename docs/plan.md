# Delivery plan for a middleware-based API gateway

## Objective

A small, auditable HTTP API reverse proxy that an individual can maintain. First
deployment: private HTTP listener behind a TLS load balancer, statically configured
HTTP/HTTPS backends, bounded-duration API calls. Public clients must not bypass
the load balancer. Backend apps retain their business authorization responsibility.

Adopt built-in HTTP middleware composed at startup, with named policy definitions
and ordered route/service references introduced alongside their implementations.
The composition contract, package ownership, and proposed configuration are in
[architecture.md](architecture.md). These are Janus design decisions inspired by
[Traefik middleware composition](https://doc.traefik.io/traefik/reference/routing-configuration/http/middlewares/overview/).
They do not imply configuration compatibility with Traefik.

This document separates implemented work from future phases. Phase 1 now
changes the accepted configuration schema by adding the consumed
`server.write_timeout` field and the first named route policy, `buffer`;
broader middleware policy types and service attachments remain future work.

## Current baseline and the original item1

As of 2026-09-16, the baseline has host/path routing, round-robin services,
streaming reverse proxying, HTTPS certificate verification, typed server and
transport settings, a startup-built middleware chain, and request-context
cancellation. Shutdown exists, with a fixed 35-second drain budget. Body limits,
admission, access observation, admin endpoints, health checks, and reload are
not implemented.

The original item1 was settings/deadline configuration, not all of Phase 1.
Its main implementation exists; Phase 1 below completes its migration and
deadline contract. The handler context deadline now comes from the startup-built
timeout middleware, while `server.write_timeout` supplies the independent socket
write deadline. The default write budget is 5 seconds longer than
`request.maximum_duration`; an omitted write setting derives the same headroom
from a customized overall budget. Server write deadlines allow the same 5-second
headroom above the 24-hour overall maximum. Real-socket coverage now proves clean 504s
before commitment and incomplete responses after commitment.

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
| 1: middleware foundation | Chain utility, startup assembly, overall-timeout migration | Existing forwarding contract passes; real-socket cancellation and deadline tests pass |
| 2: usable request policies | Named body-limit policies, request IDs, access observation | Policies compose in order; early rejection, chunked uploads, trailers, and incomplete responses are covered |
| 3: bounded operation | Global/service admission, admin readiness, configurable drain | Overload rejects promptly; shared service limits hold across routes; shutdown meets its budget |
| 4: backend and trust policy | Active health checks, trusted forwarding identity, metrics | Backend failure/recovery and spoofing tests pass; telemetry explains each failure |
| 5: safe reconfiguration | Snapshot reload and deployment lifecycle | Invalid reload preserves traffic; repeated reload/drain cycles do not leak resources or reset active limits |
| 6: production qualification | Linux CI, security review, realistic load/soak tests, canary | All release gates pass for a named build and environment |

The minimum production candidate completes phases 0–6. Phase 2 is a useful
development milestone, not a production-readiness claim.

## Phase 0 decisions

Record the deployment assumptions above and review concrete allowed/rejected
request examples: host precedence, segment prefixes, escaped paths, forwarding
identity, large bodies, and unsupported upgrades. Agree on the fixed global
chain, ordered route/service lists, and per-service state sharing documented in
the architecture. Select workload-specific limits and pass/fail latency targets
before qualification; they are not universal defaults promised by the framework.

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
5. Test using a real Janus listener: backend header stall, stalled response body,
   client cancellation, and the pre/post-commitment deadline behavior are now
   covered. Slow upload, slow response-reader, and shorter-parent-deadline
   cases remain qualification work for the Phase 1 exit review. Do not buffer
   whole responses for timeout.

Phase 1 introduces the first named route policy, `buffer`, to prove route
handler assembly. It is optional and bounded by `max_response_body_bytes`;
routes without it retain streaming behavior. HTTP server settings and backend
transport settings remain infrastructure configuration. Broader policy types,
service attachments, and the complete response-capability contract remain in
Phase 2.

## Phase 2 tasks

1. Add request ID and a single access observer in the fixed global chain. Generate
   IDs by default; define validation and trust before accepting client-supplied
   IDs. Record route/service IDs, final status, duration, consumed request bytes,
   written response bytes, and error class, including early 404/413/501/502/504.
   Keep authorization, cookies, bodies, and raw queries out of logs.
2. Preserve response capabilities through observation: `Unwrap`, flushing,
   trailers, informational responses, implicit 200, and copy/error accounting.
   Test `ResponseController`; do not advertise unsupported optional interfaces.
3. Extend typed named middleware definitions and ordered route/service references.
   Implement `body_limit` as the next named policy and add service attachments.
   Missing references, unknown types/options, multiple types per definition,
   unsupported scopes, and duplicate references within a list fail validation
   before opening listeners.
4. Enforce known-length limits before forwarding; enforce chunked limits while
   streaming. Return 413 when still possible; terminate an already-started
   response otherwise. A backend may have received a prefix of an oversized body.
   Do not add retries. Multiple route/service body caps use the smallest cap.
5. Preserve configurations without middleware references. Include runnable
   examples and end-to-end tests for two routes sharing one service, short-circuit
   behavior, and ordered policies. Add policy fields only when they take effect.

## Phase 3 tasks

1. Add fixed global admission and a service-scoped `in_flight` policy. Both reject
   saturation immediately with 503 and have no waiting queue. Define explicit
   bounded defaults for the API profile. Release permits on every exit, including
   cancellation and panic unwinding. Connection-pool limits are not admission.
2. Build one service handler/limiter per service; all routes targeting it share
   its limit. Identical policy names on different services share configuration
   but have independent counters. Test this distinction under concurrency.
3. Add a separate loopback/private admin listener with `/livez` and `/readyz`.
   Readiness means startup completed and requests are accepted under the global
   policy; one unhealthy service does not make the whole gateway unready.
4. Add configurable drain timeout and an optional bounded load-balancer removal
   delay. Mark unready first, stop admitting new work, allow accepted work to
   finish within budget, then force-close remaining connections. Align the grace
   budget with header, request, and response-write budgets and the orchestrator.

## Phase 4 tasks

1. Add service-owned active probes with interval, timeout, jitter, consecutive
   failure/recovery thresholds, and all-unhealthy 503 behavior. Close response
   bodies and bound worker count. Probe lifecycle is independent of requests.
2. Add trusted-proxy CIDRs with a reviewed multi-hop identity policy. Derive
   identity from the immediate peer and validated headers, then let proxy rewrite
   emit canonical headers. Test untrusted spoofing, malformed chains, and HTTPS
   redirects. Keep the existing immediate-peer-only behavior until implemented.
3. Add `/metrics` to the admin listener: requests, errors, duration, in-flight,
   rejections, backend health, and drain duration. Add reload metrics in Phase 5.
   Labels use bounded route/service/error identifiers; never raw paths, hosts,
   user IDs, or request IDs. Reuse the access observation outcome model.

## Phase 5 tasks

1. Bound config document size and reject duplicate JSON keys as well as unknown
   fields. Build all route/service policy references before publication.
2. Introduce immutable snapshots with rollback and explicit resource cleanup.
   Reload routes, service targets, and named policies. Keep listener, global
   settings, and the initial shared transport policy restart-only.
3. Keep global admission process-owned. Preserve active service permit accounting
   across snapshot generations; lowering a cap rejects new work until usage falls.
   A reload must not create fresh capacity while older requests remain active.
4. Ship a minimal deployment artifact, verified CA roots, non-root execution,
   resource budgets, rollout/rollback instructions, and effective-config inspection
   that excludes secrets. Add per-service transport/TLS policy only when required.

## Phase 6 tasks

Run the release gates below, including Linux signal/socket tests and the race
detector (Windows race validation remains outstanding). Test malformed framing,
ambiguous paths, middleware composition, and repeated failed/successful reloads.
Perform representative load, a 24-hour soak, and a canary with rollback criteria.

## Suggested implementation commits

Start with Phase 1: (1) chain contract and ordering tests; (2) timeout extraction;
(3) coherent write/context budgets, documentation, and real-socket tests.
Then implement the Phase 2 observer and named body-limit policy in separate changes.
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
configuration store, caching, WAF rules, transformation language, ACME, HTTP/3,
arbitrary TCP/UDP proxying, and a general policy engine. Each expands the security
and operational contract substantially. Add one only after defining its owner,
tests and failure behavior.
