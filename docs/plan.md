# Delivery plan: from starter to a production gateway

## Objective

A small, auditable HTTP API reverse proxy that an individual can maintain. First
deployment: private HTTP listener behind a TLS load balancer, statically configured
HTTP/HTTPS backends, bounded-duration API calls. Public clients must not bypass
the load balancer. Backend apps retain their business authorization responsibility.

The scaffold establishes the boundaries and request flow. Production readiness
is a set of verified behaviors for your environment, not a directory layout.

## Decisions to record before sizing

Specify deployment platform, peak and sustained requests/second, concurrency,
payload distribution, maximum upload size, normal/maximum request duration, route
count, number of backends, availability objective, and acceptable added p99 latency.
Also record TLS ownership, whether backends require mTLS, and whether authentication
lives at the gateway or in services. Choose numbers from real workloads; there is
no defensible universal RPS promise for a Go gateway.

## Milestones and exit criteria

Work sequentially; postpone optional features until a real use case needs them.
For one engineer already comfortable with Go, the ranges below are rough planning
estimates, not delivery commitments. Protocol study and external review may extend them.

| Phase | Work | Exit criteria | Indicative effort |
| --- | --- | --- | --- |
| 0: contract | Specify route precedence, path interpretation, header trust, supported protocols, SLOs and deployment topology | Reviewed examples of allowed traffic and explicit unsupported cases | 2–4 days |
| 1: forwarding baseline | Finish the starter: configurable validated deadlines, request IDs, access observation, body limits, separate admin listener | Real-socket tests for upload/download, HTTPS verification, cancellation, errors, headers, trailers and drain | 1–2 weeks |
| 2: predictable failure | Admission limits, active health checks, recovery thresholds, ready/draining state, per-service limits | Backend loss, slow clients and traffic spikes cause bounded resource use and predictable errors | 1–2 weeks |
| 3: operability | Metrics, structured access logs, reload transactions, deployment artifact, least privilege, rollback/runbook | Invalid reload preserves traffic; rollout drains correctly; alerts explain injected incidents | 1–2 weeks |
| 4: qualification | Protocol fuzzing, security review, realistic comparison benchmarks, soak tests and canary | All release gates below pass for a named build and environment | 1–2 weeks |

Minimum production candidate includes phases 0–4. Optional authentication middleware,
distributed rate limits, retries, circuit breakers, gRPC and discovery come later.
Health checks and a circuit breaker are distinct mechanisms; neither is mandatory
as a duplicate of the other. Start with health checks plus admission limits.

## Next implementation tasks, in order

1. Move server/transport constants into validated typed settings with documented
   duration syntax and min/max bounds. Separate connect, header, body and overall
   budgets. Decide whether an overall budget actively cancels backend work.
2. Add request-body size enforcement for both known lengths and chunked input.
   Reject oversized known bodies early. A streaming limit can reject only after
   some bytes have reached the backend; document this and never retry those writes.
3. Add a global admission semaphore and per-service request caps. Reject saturation
   with 503, use 429 for a deliberate client quota, and keep queues bounded or absent.
4. Add request IDs and access logs with route/service IDs, status, duration, bytes
   and error class. Do not log authorization, cookies, bodies or raw query strings.
   Preserve flushing/trailers when wrapping the response writer.
5. Add a loopback/private admin listener: `/livez`, `/readyz`, `/metrics`. Readiness
   means able to accept traffic under a defined policy, not that every backend is
   healthy. Mark unready before drain; do not expose pprof on the data listener.
6. Add bounded active probes with jitter, timeout, consecutive failure/recovery
   thresholds and all-unhealthy 503 behavior. Probe a cheap backend endpoint.
   Avoid per-request probes. Close probe response bodies and stop workers on reload.
7. Implement trusted-proxy CIDRs plus forwarding-header sanitation. Only allow
   known load balancers to supply original scheme/client identity. Test spoofing
   from untrusted peers, multiple hops and HTTPS redirects.
8. Add metrics: requests/errors/duration by route and service, in-flight and rejected
   work, backend health, pool waits, reload outcomes and drain duration. No raw path,
   user ID, request ID, or arbitrary hostname metric labels.
9. Implement reload with immutable snapshots, rollback on validation/build failure,
   and explicit lifecycle ownership as described in the architecture document.
10. Build a minimal deployment image and manifests for the chosen platform. Run as
    non-root with read-only filesystem where practical, explicit CPU/memory/file
    descriptor budgets, verified CA roots and graceful termination settings.

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

Plugin marketplace, scripting, dashboard, Kubernetes/Docker discovery, distributed
configuration store, caching, WAF rules, transformation language, ACME, HTTP/3,
arbitrary TCP/UDP proxying, and a general policy engine. Each expands the security
and operational contract substantially. Add one only after defining its owner,
tests and failure behavior.
