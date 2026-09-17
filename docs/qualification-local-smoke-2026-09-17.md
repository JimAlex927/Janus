# Local qualification smoke — 2026-09-17

This is a development-host measurement of the qualification tool and the
ordinary HTTP forwarding path. It is not a production capacity result.

## Setup

- Build under test: `c298ad1`
- Host: Windows development workstation, loopback only
- Backend: `examples/backend`, HTTP/1, `127.0.0.1:9000`
- Janus: `configs/janus.json`, HTTP/1 listener on `127.0.0.1:8080`
- Generator: `cmd/janus-loadtest`
- Scenario: GET, 2 seconds, target arrival rate 100 requests/s, concurrency 8,
  1 second request timeout
- Response body: 64 bytes from the example backend

## Results

| Target | Scheduled | Started | Completed | 2xx | Errors | End-window cancellations | Mean | P50 | P95 | P99 | Max |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Direct backend | 200 | 200 | 200 | 199 | 0 | 1 | 0.275 ms | 1 ms | 1 ms | 5 ms | 2.264 ms |
| Janus forwarding | 200 | 199 | 199 | 199 | 0 | 0 | 1.347 ms | 5 ms | 5 ms | 10 ms | 8.034 ms |

Actual completed rates were approximately 99.92 requests/s for the direct
backend and 99.46 requests/s through Janus. The one direct-backend cancellation
occurred at the generator's measurement boundary and is reported separately
from errors.

## Limits of this evidence

This run uses loopback, one tiny response, one route, one backend, no TLS, no
HTTP/2 or HTTP/3, no SSE/WebSocket traffic, no overload, and no OS resource
sampling. It provides a smoke check for the generator and a local forwarding
delta only. It must not be used as a supported RPS, latency SLO, memory budget,
or production approval. The Linux VM runbook must repeat direct-baseline,
steady, peak, overload, and long-soak scenarios with agreed business traffic
parameters.
