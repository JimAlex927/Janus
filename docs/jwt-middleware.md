# JWT middleware

This document describes the built-in `jwt` middleware. It is available at
route and service scope and is advertised by the backend capability catalog.
JWT authenticates the caller; authorization and claim forwarding remain
separate policies.

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

JWT is authentication, not authorization by itself. A first release should
only establish the caller identity and validate configured issuer/audience and
required claims. Claim-to-header forwarding, arbitrary claim expressions and
scope/role authorization should be separate reviewed features; forwarding
untrusted claims to an upstream by default would create a new trust boundary.

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
