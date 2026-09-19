# Built-in HTTP middleware

Janus follows the same named, ordered route/service middleware model as Traefik,
but keeps each policy explicitly typed and validated by the Janus config
schema. The admin console reads the capability catalog from the running binary.

The production middleware set currently includes:

- `jwt`: signature, issuer, audience, time and required-claim validation. It
  never forwards claims or credentials to an upstream automatically.
- `basic_auth`: HTTP Basic authentication backed by bcrypt hashes. Successful
  authentication can remove `Authorization` before proxying.
- `ip_allowlist`: CIDR allow/deny using the client IP resolved through the
  limen's trusted-proxy policy, so arbitrary `X-Forwarded-For` values are not
  trusted.
- `rate_limit`: bounded per-client token bucket with `average`, `period`,
  `burst`, `max_keys`, HTTP 429 and `Retry-After`.
- `compress`: negotiated gzip for ordinary responses. WebSocket upgrades,
  SSE, empty/204/304 responses and already encoded responses are skipped.
- `headers`, `cors`, `body_limit`, `buffer`, `in_flight`, `strip_prefix` and
  `add_prefix` for request shaping and routing.

`retry`, `circuit_breaker` and `forward_auth` are intentionally not enabled by
default yet. They require explicit upstream retry semantics, response
buffering, or an external authentication lifecycle; adding them without those
contracts would make failure behavior ambiguous. They should be introduced as
separate typed policies with bounded resources and dedicated tests.

Example:

```json
{
  "middlewares": {
    "admin-auth": {
      "scope": "route",
      "basic_auth": {
        "realm": "Janus Admin",
        "users": { "admin": "$2a$10$replace-with-a-bcrypt-hash" },
        "remove_header": true
      }
    },
    "public-limit": {
      "scope": "route",
      "rate_limit": { "average": 20, "period": "1s", "burst": 40 }
    },
    "gzip": { "compress": {} }
  }
}
```

Never place plaintext passwords in configuration. Generate bcrypt hashes out of
band and protect the configuration store itself.
