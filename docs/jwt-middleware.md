# JWT middleware

This document describes the built-in `jwt` middleware. It is available at
route and service scope and is advertised by the backend capability catalog.
JWT authenticates the caller. Claim forwarding is available only through an
explicit allowlist in the same JWT policy; it is disabled by default.

## Why it fits Janus

JWT verification belongs after route matching and before the selected action or
service. Therefore it fits the existing named middleware model:

- Route scope protects one route and is the default recommendation.
- Service scope protects every route that forwards to the same Service.
- The same policy can protect ordinary HTTP, SSE and classic HTTP/1 WebSocket
  upgrade requests because verification completes before the response or
  upgrade is committed.
- A failed verification returns `401 Unauthorized` with a generic
  `WWW-Authenticate: Bearer` challenge. Token details stay in bounded server
  logs and are never returned to the client.

JWT is authentication, not authorization by itself. The gateway validates the
token once and can then pass selected verified identity fields to the
application as request headers. This is deliberately an allowlist rather than
a general claim expression engine.

## Policy shape

The candidate configuration is intentionally explicit. Exactly one key source
must be configured, and algorithms must be an allowlist rather than inferred
from the token header:

```json
{
  "middlewares": {
    "orders-auth": {
      "scope": "route",
      "jwt": {
        "key_source": {
          "jwks_url": "https://idp.example.com/.well-known/jwks.json"
        },
        "algorithms": ["RS256"],
        "issuer": "https://idp.example.com/",
        "audience": ["janus-orders"],
        "required_claims": ["sub"],
        "claim_headers": {
          "User": "user",
          "X-User-ID": "sub",
          "X-User-Roles": "roles"
        },
        "remove_authorization": true,
        "clock_skew": "30s"
      }
    }
  }
}
```

The supported key sources are:

- `jwks_url` supports issuer key rotation. It is HTTPS-only, follows only
  same-host HTTPS redirects, fetches at most 1 MiB, caches keys for five
  minutes, and can use the last good set for up to fifteen minutes during a
  provider outage.
- `public_key_file` is a deterministic offline alternative for installations
  that distribute public keys with the deployment.
- `secret_env` supports HS256/384/512 for internal deployments. The secret is
  read only from the process environment, must be at least 32 bytes, and is
  never returned by the admin API or written into access logs.

The implementation must reject `none`, algorithm confusion, missing `kid` when
the selected key set requires it, duplicate claims with ambiguous decoding,
expired tokens, tokens used before `nbf`, invalid issuer/audience, and values
outside the configured clock skew. It must cap token and header sizes before
parsing so authentication cannot become an unbounded allocation path.

## Composition rules

Middleware order remains the ordered attachment array. For a CORS-protected
browser API, CORS must be able to finish an allowed preflight before JWT is
invoked; the implementation and editor must make this ordering visible. A
preflight is not an authenticated application request. Ordinary requests,
SSE and WebSocket upgrades must pass JWT before the backend is contacted.

JWT must not accept tokens in query strings. The initial source is the
`Authorization: Bearer <token>` header only. Cookie support, if needed later,
needs CSRF and SameSite policy of its own.

## Current implementation

The verifier uses an explicit algorithm allowlist and the
`golang-jwt/jwt/v5` parser. It bounds compact tokens at 16 KiB, rejects
duplicate JSON object members, requires `exp`, validates `nbf`/`iss`/`aud` with
configured clock skew, and returns a generic `401` with
`WWW-Authenticate: Bearer`. Tokens are accepted only from the
`Authorization: Bearer` header; query strings and cookies are not
authentication sources.

`claim_headers` maps an upstream header name to a verified top-level claim
name. String claims are copied as-is; arrays and objects are JSON encoded, so a
claim such as `user` can be consumed from the `User` request header by the app.
The gateway deletes each mapped header before applying the mapping, preventing
a client-provided stale value from surviving when a claim is absent. Header
names are restricted to ordinary end-to-end headers; `Authorization`,
`Cookie`, `X-Forwarded-*`, hop-by-hop and framing headers are rejected. Each
value is bounded to 8 KiB and CR/LF is rejected. `remove_authorization` is
useful when the app should trust only gateway-provided identity headers.

Each routing generation owns its verifier. A failed generation construction
cannot publish a partially initialized policy, and a retired generation
releases its verifier with the rest of its handler graph.

## Production acceptance

Before enabling JWT for a business route, verify key rotation, clock skew,
issuer/audience isolation, malformed and oversized tokens, concurrent request
load, 401 response headers, no upstream contact on rejection, and behavior
through H1/H2/H3, SSE and WebSocket paths. CORS must be attached outside JWT
so an allowed preflight completes before authentication; JWT itself never
bypasses authentication for OPTIONS requests.
