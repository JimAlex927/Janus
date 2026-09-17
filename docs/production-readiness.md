# First production release acceptance

Status: candidate under qualification; code-level blockers found in the
September 17 review have been repaired, but the release is not yet approved
for production.
The target Linux VM is being prepared by the operator. Windows tests and Linux
cross-compilation do not satisfy Linux execution or deployment gates.

## Evidence and remaining work

| Gate | Candidate evidence | Still required |
| --- | --- | --- |
| Accepted requests survive graceful drain | H3 client completes during shutdown; forced-drain tests retained; process-level lifecycle test exercises a real TCP client and bounded context cancellation; Linux-only child-process H3 drain test added | Execute on target Linux with concurrent TCP/UDP traffic |
| Stream timeouts release resources | Slow TCP reader terminates; H1/H2/H3 proxy tests assert body failure before client deadline, backend cancellation and released admission; HTTP/3 stream timeout closes a slow request body | Target Linux slow readers/uploads and concurrent sibling streams under load |
| Reload matches published bytes | Startup hash comes from the runtime input snapshot; certificate hash/parse/publication use the same bounded bytes; startup race regressions | Repeated route/certificate replacement and rollback during load |
| Correct timeout response | 103 followed by deadline returns final 504 | Protocol fault-injection campaign |
| Linux/race | Pinned CI definition; Linux process SIGTERM tests now cover TCP and H3 and cross-compile; Windows full test/vet plus repeated full tests and race pass | Actual full test/race/vet runs on VM; preserve logs |
| Deployment | Native systemd unit and runbook | Install as non-root on VM, readiness, restart, ports, CA roots and resource caps |
| Capacity | Configuration limits exist | Agree workload, measure overload and 24h soak |
| Canary | Not started | Name non-core business, traffic split, baseline and rollback target; qualify first |

## Workload must be agreed before the load run

Record VM distribution/kernel, CPU model and allocated vCPU, RAM/cgroup memory
limit, Go version, commit, binary SHA-256, enabled protocols and effective config.
Keep certificates, private keys and credentials out of the evidence archive.

Operator inputs still needed: peak and steady RPS, request/response sizes, backend
latency, active ordinary requests, active SSE/WebSockets, connection churn, and
acceptable p95/p99/error rates. Unknown values are not a passed capacity gate.

The existing global admission cap includes long-lived requests: API concurrency
plus active SSE/WebSockets must fit the configured cap and retain capacity for
traffic bursts. Transport connection limits are not a substitute. Size buffered
routes using concurrent buffered responses times the configured response cap;
include connection buffers, TLS/QUIC state, Go heap, health workers and log I/O
when choosing the process memory budget. Do not infer a supported RPS from the
default configuration or from a localhost benchmark.

Run a direct-backend baseline, then Janus at steady target load, peak load and
overload. Use a separate generator when measuring capacity. Capture latency,
errors, admission rejection, RSS/heap, CPU, goroutines and socket counts. Verify
recovery after overload, client cancellation, slow clients and backend failures.
Run the agreed representative mix for 24 hours, including reload and certificate
rotation; retain raw time-series and check for sustained resource growth.

## Linux execution sequence

1. Record VM identity/specifications and install the pinned Go toolchain and a
   supported C compiler for race detection. Synchronize the reviewed candidate.
2. Run `go mod verify`, `go test -count=1 ./...`, `go test -race -count=1 ./...`,
   `go vet ./...`, and the pinned `govulncheck` from CI. The full suite includes
   the Linux-only real-process SIGTERM regression and real TCP/UDP tests.
3. Build and checksum the Linux binary. Validate effective configuration and TLS
   trust on that host. Deploy with the systemd instructions; prove readiness,
   SIGTERM drain, bounded force-close, restart and socket release using clients.
4. Agree capacity numbers, then run overload and soak. Record failures and fixes
   against the exact build; repeat affected gates after code changes.
5. Only after these pass, route a small agreed share of a named non-core business
   to the candidate. Keep the previous binary/config available. Compare agreed
   latency/error/resource thresholds and exercise traffic rollback plus process
   drain. Verify client results and backend state; rollback does not undo writes.

No live business traffic is switched until the destination, traffic scope and
rollback baseline are identified. A local demo is not production canary evidence.

## Local qualification replay: 2026-09-17

The reviewed tree through commit `8e72c0f` passed the following on the development
host: `go test -count=1 ./...`, `go test -count=5 ./...`,
`go test -race -count=1 ./...`, `go vet ./...`,
`go test -fuzz=FuzzLoadNeverPanics -fuzztime=30s ./internal/config`,
`go mod verify`, `govulncheck@v1.7.0`, and a static `linux/amd64` build.
The vulnerability scan reports zero vulnerabilities affecting reachable code;
four module vulnerabilities are present but not called by this code. These are
development-host results and do not replace Linux execution or deployment
evidence.

The cross-platform process lifecycle regression additionally verifies that the
entrypoint reaches `/readyz`, returns the expected backend body, publishes a
route replacement from the watched file, and exits after cancellation. The
Linux-only child-process SIGTERM test remains necessary because it verifies the
actual OS signal and process boundary.

At commit `65375b8`, the full suite and full race suite were rerun after
adding the process test, and
`go test -count=50 -run '^TestRunLifecycle$' ./cmd/janus` passed to check
for startup/reload/shutdown flakiness.
The Linux-only H3 child-process test now also cross-compiles in
`go test -c -o bin/janus-linux-tests ./cmd/janus` with
`GOOS=linux GOARCH=amd64 CGO_ENABLED=0`. It has not been executed on Linux
from this development host; the Linux VM gate remains open.
