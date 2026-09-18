# JWT middleware design

This document records the design boundary for a future built-in `jwt`
middleware. The middleware is not implemented or advertised by the current
capability catalog yet. Keeping the design separate prevents a partially
verified token parser from becoming a production authentication boundary.

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

## Proposed policy shape

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

The first production profile should prefer asymmetric verification:

- `jwks_url` supports issuer key rotation, with HTTPS certificate verification,
  bounded fetches, an immutable cached key set and stale-key behavior defined
  before implementation.
- `public_key_file` is a deterministic offline alternative for installations
  that distribute public keys with the deployment.
- `secret_env` may be supported for small internal deployments only; it must
  never be returned by the admin API or written into access logs.

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

## Implementation stages

1. Add typed config, strict validation and a capability-catalog entry only when
   the constructor has a real verifier. Add redaction rules for every secret
   source.
2. Implement a leaf verifier package with an explicit algorithm allowlist,
   bounded compact-token parsing, standard time claims, issuer/audience checks,
   and generic client errors. Unit-test every rejection before wiring it into
   HTTP middleware.
3. Add static public-key verification first. Add a bounded JWKS cache and key
   rotation worker only after lifecycle ownership across Runtime generations
   is specified. A failed candidate generation must release its verifier/cache.
4. Wire Route and Service construction through the existing typed factory.
   Add real handler tests for 401/200, preflight, backend non-contact,
   streaming protocols, reload rollback and old-generation retirement.
5. Generate the console form from the backend catalog. Do not add JWT field or
   type branches to TypeScript; the existing dynamic editor should render its
   fields and restrictions.

## Production acceptance

Before enabling JWT for a business route, verify key rotation, clock skew,
issuer/audience isolation, malformed and oversized tokens, concurrent request
load, 401 response headers, no upstream contact on rejection, and behavior
through H1/H2/H3, SSE and WebSocket paths. The deployment must also define
whether the identity provider is reachable during startup and what happens
when a cached JWKS becomes stale. Until those answers and tests exist, JWT is
not part of the supported production middleware set.
