# Mechanisms to learn, with experiments

Read this in order while building the milestones. The purpose is to know what can
break at a protocol boundary and how to demonstrate correct behavior.

| Topic | Why it matters | Small experiment |
| --- | --- | --- |
| TCP, deadlines, half-closes | A connected peer can stop making progress without disconnecting | Backend accepts TCP but never returns headers |
| HTTP/1.1 framing and persistence | Message boundaries determine whether reusing a connection is safe | Exercise chunked uploads, early rejection and conflicting framing over raw sockets |
| Hop-by-hop versus end-to-end headers | Connection metadata must not leak through the proxy | Send `Connection: X-Debug` and verify `X-Debug` is removed downstream |
| Host, SNI and TLS verification | Routing identity and certificate identity are related but different | Use a wrong-name backend certificate and require a failure |
| Pooling and backpressure | Slow peers consume capacity; idle pools do not bound all work | Compare keepalive on/off and slow readers under a fixed admission limit |
| Cancellation and timeout budgets | Abandoned requests otherwise keep using backend capacity | Disconnect a client and observe backend context cancellation |
| Idempotency and retries | A timed-out write may already have succeeded | Commit a write then break the connection before its response |
| HTTP/2 multiplexing and flow control | Stream counts and connection counts differ | Run many requests on one connection and inspect service admission limits |
| Reload and shutdown | In-flight requests need stable policy and owned resources | Reload during long requests; stop during upload; inspect leaked goroutines |
| Trust boundaries and normalization | Different interpretations can bypass auth/routing rules | Test encoded slashes, dot segments, host variants and spoofed forwarded headers |

## HTTP framing and header trust

## Phase 0 support matrix

| Request or condition | Janus behavior |
| --- | --- |
| `GET /api`, `GET /api/users` | Matches a `/api` segment prefix. |
| `GET /apix` | Does not match `/api`; returns 404 unless another route matches. |
| Exact host plus hostless route | The exact, case-insensitive host rule wins; the incoming port is ignored for matching. |
| `GET /api/a%2Fb` | Routing uses Go's decoded `URL.Path`; the reverse proxy preserves the escaped path for the backend. |
| Client-supplied `X-Forwarded-*`, `X-Real-IP`, or `Forwarded` | Removed or rewritten; forwarded identity is trusted only when the immediate peer matches the limen's explicit CIDRs. |
| `CONNECT` or a non-WebSocket `Upgrade` request | Rejected with 501; Janus does not create a tunnel. |
| No matching host/path | Returns 404 without contacting a backend. |
| Overall deadline before response commitment | Returns 504 when the response socket remains writable. |
| Overall deadline after response commitment | Stops the stream; it does not append a second 504 response. |

This matrix is the Phase 0 protocol boundary. Body-size rejection, admission and
trusted multi-hop forwarding identity are explicit policies; additional protocol
modes remain later policies, not implicit behavior.

## Request body limits

`body_limit.max_bytes` is a named middleware that can be attached to a route or
service. A known `Content-Length` above the cap is rejected with 413 before the
backend handler is invoked. Chunked and otherwise unknown-length bodies are
bounded as they are read; if the cap is exceeded while the proxy is forwarding,
Janus returns 413 when the response is still writable. The backend can therefore
have observed a prefix, but Janus does not retry the request.

When both the matched route and its service have body-limit policies, the nested
standard-library readers enforce the smallest effective cap. The policy limits
bytes, not upload duration; use the server read deadline and the fixed/service
admission policies for those separate concerns.

The fixed global observer adds a fresh `X-Request-ID` to each request and response,
overwriting client input. Its access record
contains only the method, path without query, route/service names, status, duration,
request/response byte counts and a bounded error class. It does not log headers,
cookies, bodies or raw queries. Route metadata is written into request-local shared
state so early route-policy responses remain attributable after the handler returns.

Let the standard library parse and serialize HTTP. Never concatenate raw request
headers or implement chunk decoding yourself. Go's reverse proxy handles
hop-by-hop removal; its `Rewrite` API clears standard forwarding headers before
you supply new ones. The starter derives them from the direct connection and
also removes alternate `X-Forwarded-*`, `X-Real-IP` and `Forwarded` hints. With an
explicit limen `trusted_proxies` policy, Janus validates an IP-only XFF chain from
the trusted immediate peer, walks it from right to left, and emits the sanitized
chain plus the current peer. XFP accepts only `http` or `https`; XFH accepts only
a validated host. Backends should trust only the documented identity headers
emitted by Janus.

An existing TLS terminator is a separate trust hop. Janus does not copy client
forwarding headers merely because they exist: an untrusted immediate peer is
reported as the client, while a trusted peer must pass the CIDR and syntax checks.
The policy is per limen and has no insecure “trust all” mode. The inbound
`X-Forwarded-Proto: https` value is therefore available to backend applications
for HTTPS-aware redirects only when it came through a trusted peer.

URI normalization is another boundary: routing uses decoded `URL.Path`, while
backends may handle escapes, repeated slashes and dot segments differently.
Define one tested policy before adding path-based authentication. Never decode a
path repeatedly. Query parsing also deserves tests; do not blindly override the
standard proxy's invalid-query handling just to preserve every input byte.

## Timeouts are several controls

| Control | What it bounds | What it does not guarantee |
| --- | --- | --- |
| Read-header timeout | Time to read inbound headers | Total body size or active connection count |
| Server read timeout | Reading a request, including body | Application work after reading |
| Dial timeout | Establishing a backend connection | Backend response time |
| TLS handshake timeout | Backend TLS negotiation | Subsequent headers/body |
| Response-header timeout | Waiting for backend headers after sending request | Reading an entire backend response |
| Request context deadline | Deadline observed by outbound transport/work | Interrupting every arbitrary blocking operation |
| Server write timeout | Socket response-write deadline | Automatically canceling all handler work or returning a clean 504 |
| Idle timeout | Waiting between keepalive requests | An active request's duration |

The starter exposes these values as validated settings. Its overall API duration
is an active request-context deadline, so outbound backend work observes cancellation
when that budget expires. `server.write_timeout` is an independent socket deadline
and must exceed the overall budget by enough headroom to write a timeout response;
it is not a replacement for context cancellation. The named body-size policy is
separate from these time budgets. Do not apply a short API timeout to
WebSockets, gRPC streams or SSE. Janus now skips the bounded API timeout for
explicitly classified SSE/WebSocket requests, clears the finite response-write
deadline, and lets Limen shutdown own the drain. Streaming still needs a
production per-stream lifetime/idle policy.

## Retries and backend health

Keep application retries disabled initially. A connection reset does not prove a
write failed. An idempotency key is useful only when the backend implements durable
deduplication. Replaying a request requires both a semantically safe operation and
a replayable body, remaining deadline, bounded attempts and a retry budget.
Once response bytes have reached the client, switching backends is no longer a
transparent retry.

Even without an application retry loop, Go's transport has limited automatic
retries for eligible requests on reused-connection failures. Audit these semantics
when designing write APIs rather than promising exactly-once delivery.

Active health probes estimate backend availability between requests. They need
timeouts, jitter and failure/recovery thresholds. Passive error tracking sees real
traffic but can confuse client cancellation with backend failure. A circuit breaker
rejects calls during repeated failures; it is a different state machine. Retry
storms and synchronized probes can amplify a backend outage.

## HTTPS and protocol expansion

The legacy listener receives HTTP/1.x on a private interface. Versioned startup
configuration can also create a native TLS Limen with HTTP/1.1 and HTTP/2
selected through ALPN. A load balancer can still terminate public TLS and forward
HTTP/1.1 to Janus. Outbound TLS must verify certificates and hostnames; never ship
`InsecureSkipVerify` to make a deployment work. Plan CA rotation and mTLS
separately if required.

gRPC requires a deliberate HTTP/2 path, trailer preservation, deadline and
cancellation semantics, gRPC status visibility and streaming tests. HTTPS
upstream HTTP/2 capability alone is not a gRPC support claim. Janus supports SSE
through the normal HTTP response path and proxies classic HTTP/1 WebSocket
upgrades through `ReverseProxy`. WebSocket extended CONNECT over HTTP/2 is not
enabled. WebSocket routes must be explicitly declared with
`"protocols": ["websocket"]`; SSE routes use `"protocols": ["sse"]`. The
route-level buffer policy bypasses both streaming modes so it cannot delay
events or break the upgrade handshake.

## Reading list

These are primary references; use the docs for the Go version you actually deploy.

- [Go ReverseProxy](https://pkg.go.dev/net/http/httputil#ReverseProxy): forwarding, Rewrite, trailers and error behavior.
- [Go HTTP Transport](https://pkg.go.dev/net/http#Transport): pooling, HTTP/2, automatic retries and timeout semantics.
- [Go HTTP Server](https://pkg.go.dev/net/http#Server): listener controls and shutdown behavior.
- [Go release policy](https://go.dev/doc/devel/release): supported versions and security maintenance.
- [HTTP/1.1 specification, RFC 9112](https://www.rfc-editor.org/info/rfc9112/): message boundaries, persistence and framing ambiguity.
- [Go transport source](https://go.dev/src/net/http/transport.go), [server source](https://go.dev/src/net/http/server.go), and [reverse proxy source](https://go.dev/src/net/http/httputil/reverseproxy.go): follow the actual call paths after reading the API contracts.
- [Traefik routing overview](https://doc.traefik.io/traefik/v3.2/routing/overview/): component responsibilities.
- [Traefik services](https://doc.traefik.io/traefik/reference/routing-configuration/http/load-balancing/service/): backend balancing and health configuration.
- [NGINX upstream module](https://nginx.org/en/docs/http/ngx_http_upstream_module.html): connection pools and balancing controls.
- [NGINX proxy module](https://nginx.org/en/docs/http/ngx_http_proxy_module.html): buffering and timeout tradeoffs.
- [NGINX development guide](https://nginx.org/en/docs/dev/development_guide.html): event-driven server internals.

Source links are design references, not evidence that this starter matches these
products' performance or has their production maturity.
