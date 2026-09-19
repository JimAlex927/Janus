# First production release acceptance

Status: candidate under qualification; code-level blockers found in the
September 17 review have been repaired, but the release is not yet approved
for production.
The target Linux VM is being prepared by the operator. Windows tests and Linux
cross-compilation do not satisfy Linux execution or deployment gates.

The release toolchain is pinned to Go 1.25.14 in CI. The module's `go 1.25.13`
directive is the minimum compatibility floor, not the release toolchain
selection. The local development host currently runs Go 1.26.5; its
`govulncheck` result must not be used as release evidence because that patch
line has seven standard-library findings fixed in Go 1.26.6. Release approval
requires the pinned Go 1.25.14 CI scan to complete successfully.

## Candidate support boundary

This is the release candidate's explicit support contract. A feature listed as
supported still requires the Linux, load and canary gates below before it is
approved for production use.

| Area | Supported contract | Explicit non-goals |
| --- | --- | --- |
| Inbound protocols | HTTP/1; native TLS HTTP/2; native TLS HTTP/3 with a TCP HTTP/1 or HTTP/2 fallback; explicit cleartext h2c on its own Limen | Arbitrary TCP/UDP tunnels; HTTP/2 or HTTP/3 extended CONNECT; public TLS termination as a Janus responsibility |
| Application streams | Bounded ordinary HTTP, SSE, and classic HTTP/1 WebSocket upgrades with separate stream budgets and drain handling | A general gRPC support claim; WebSocket extended CONNECT over HTTP/2 or HTTP/3 |
| Routing/actions | Host/path or Match DSL rules with ordered middleware, forward/redirect/respond actions, and controlled Static file serving for GET/HEAD | Path rules as an authorization boundary without backend normalization agreement; cross-directory static mounts |
| Discovery/upstreams | Static HTTP/HTTPS origins, Nacos services with named registries and namespaces, bounded health checks, TLS certificate verification | Arbitrary service-discovery plugins or unbounded retry/circuit-breaker semantics |
| Identity/policy | JWT validation, explicit claims-to-header allowlists, Traefik-style ForwardAuth, Basic Auth, IP allowlists, CORS, rate limits, body limits, headers and prefix policies | Automatic claim/header forwarding without configuration; trust-all forwarded identity; plaintext credentials in config |
| Operations | Authenticated private admin console, versioned config library, atomic reload, certificate rotation, readiness/metrics, graceful drain and rollback artifacts | Treating the local container smoke or a single localhost benchmark as production certification |

## Evidence and remaining work

| Gate | Candidate evidence | Still required |
| --- | --- | --- |
| Accepted requests survive graceful drain | H3 client completes during shutdown; forced-drain tests retained; process-level lifecycle test exercises a real TCP client and bounded context cancellation; Linux-only child-process H3 drain test added | Execute on target Linux with concurrent TCP/UDP traffic |
| Stream timeouts release resources | Slow TCP reader terminates; H1/H2/H3 proxy tests assert body failure before client deadline, backend cancellation and released admission; HTTP/3 stream timeout closes a slow request body | Target Linux slow readers/uploads and concurrent sibling streams under load |
| Reload matches published bytes | Startup hash comes from the runtime input snapshot; certificate hash/parse/publication use the same bounded bytes; startup race regressions | Repeated route/certificate replacement and rollback during load |
| Correct timeout response | 103 followed by deadline returns final 504 | Protocol fault-injection campaign |
| Linux/race | Pinned CI definition; Linux process SIGTERM tests now cover TCP and H3 and cross-compile; Windows full test/vet plus repeated full tests and race pass | Actual full test/race/vet runs on VM; preserve logs |
| Deployment | Native systemd unit and runbook; local Debian container smoke | Install as non-root on VM, readiness, restart, ports, CA roots and resource caps |
| Capacity | Configuration limits exist | Agree workload, measure overload and 24h soak |
| Canary | Not started | Name non-core business, traffic split, baseline and rollback target; qualify first |

## Current repository verification replay: 2026-09-19

The current working tree, including the refreshed embedded Admin Console,
passed the following local checks:

| Check | Result | Scope note |
| --- | --- | --- |
| Frontend build | pass | `npm run build` in `frontend` |
| Embedded UI parity | pass | `./scripts/verify-embedded-ui.sh` |
| Admin package tests | pass | `go test ./internal/admin/...` |
| Linux release gate | pass | `JANUS_FUZZ_TIME=20s JANUS_LINUX_ARTIFACT=/private/tmp/janus-linux-amd64-4a69e23 ./scripts/verify-linux.sh` at commit `4a69e23` |
| Linux artifact | `sha256: 79b6bb495284920326a34158b000fcdbfb7fc7d29cf1a90684c10e072cb179e4` | Static `linux/amd64`, built by the gate after active-history protection and draft-save atomicity fixes |
| Vulnerability scan | open | Local Go 1.26.5 reports seven standard-library findings; do not release from this toolchain |

The release gate was run with a 20-second fuzz smoke because the local machine
occasionally pauses the configuration fuzz workers for several seconds. It
passed full tests, fuzz, race, vet, module verification, optional unit syntax
checks, and the static Linux build. This remains development-host evidence; it
does not close the real Linux VM, systemd, capacity, soak, or canary gates.

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

The repository includes `cmd/janus-loadtest`, a separate standard-library
generator for ordinary HTTP capacity runs. Build it independently from Janus:

```sh
go build -trimpath -o bin/janus-loadtest ./cmd/janus-loadtest
./bin/janus-loadtest -url http://backend:9000/api \
  -duration 60s -concurrency 64 -rate 500 -timeout 5s > direct-backend.json
./bin/janus-loadtest -url http://janus:8080/api \
  -duration 60s -concurrency 64 -rate 500 -timeout 5s > janus-steady.json
```

Run the generator outside the gateway process, repeat each scenario after
warmup, and retain the JSON output alongside OS-level CPU/RSS/socket samples.
The tool deliberately reports ordinary HTTP only; SSE and WebSocket scenarios
need protocol-aware clients and must be added to the same workload mix rather
than inferred from ordinary-request results.

The first local smoke is archived in
[qualification-local-smoke-2026-09-17.md](qualification-local-smoke-2026-09-17.md).
It is explicitly development-only and does not close the Linux capacity gate.

## Linux execution sequence

1. Record VM identity/specifications and install the pinned Go toolchain and a
   supported C compiler for race detection. Synchronize the reviewed candidate.
2. Run `./scripts/verify-linux.sh` and the pinned `govulncheck` from CI. The
   script runs module verification, the full suite, config fuzz smoke, race,
   vet, and the reproducible static Linux build. The full suite includes the
   Linux-only real-process SIGTERM regression and real TCP/UDP tests.
3. Build and checksum the Linux binary. Validate effective configuration and TLS
   trust on that host. Deploy with the systemd instructions; prove readiness,
   SIGTERM drain, bounded force-close, restart and socket release using clients.
4. Agree capacity numbers, then run overload and soak. Record failures and fixes
   against the exact build; repeat affected gates after code changes.
5. Only after these pass, route a small agreed share of a named non-core business
   to the candidate. Keep the previous binary/config available. Compare agreed
   latency/error/resource thresholds and exercise traffic rollback plus process
   drain. Verify client results and backend state; rollback does not undo writes.

### Local Linux container smoke: 2026-09-19

At commit `ae9f22b`, a `CGO_ENABLED=0 GOOS=linux GOARCH=arm64` binary was run
inside a clean `debian:bookworm-slim` container on the local OrbStack Linux
engine. A temporary minimal configuration exposed one response route and the
private admin listener. The smoke observed `/readyz` = `200 ok`, the route body
`linux-ok`, then sent `SIGTERM`; the container exited with code `0` and the
logs showed request draining plus the configured load-balancer removal delay.
The container was removed after the check. This closes only a Linux process and
socket smoke; it does not close the target-VM systemd, race, capacity, soak, or
canary gates above.

A second run used a non-root UID 1001 as the Janus process itself (without a
privileged wrapper). It created the SQLite library in the writable configuration
directory, served the same checks, received `SIGTERM`, entered drain, and exited
with code `0`. This specifically exercises the permissions required by the
native unit's `ReadWritePaths=/etc/janus` contract.

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

At commit `35395db`, after atomic control-plane publication changes, the
current Windows development host again passed `go test ./...`,
`go test -race ./...`, and `go vet ./...`. This refreshes local regression
evidence only; it does not close the target-Linux, deployment, capacity, soak,
or canary gates listed above.

The Linux-only H3 child-process test now also cross-compiles in
`go test -c -o bin/janus-linux-tests ./cmd/janus` with
`GOOS=linux GOARCH=amd64 CGO_ENABLED=0`. It has not been executed on Linux
from this development host; the Linux VM gate remains open.
