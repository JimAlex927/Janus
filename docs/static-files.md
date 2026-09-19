# Static file routes

Janus can serve a local frontend or other controlled assets directly from a
route. Use the `static` terminal action when the gateway should behave like a
small, configuration-driven Nginx static server.

```json
{
  "name": "frontend",
  "path_prefix": "/",
  "priority": 10,
  "action": {
    "static": {
      "root": "/srv/janus/web/dist",
      "index": "index.html",
      "spa_fallback": true,
      "directory_listing": false,
      "cache_control": "public, max-age=3600"
    }
  }
}
```

`root` must be an existing absolute directory readable by the Janus process.
Only `GET` and `HEAD` are accepted. The configured `index` is used for a
directory request and, when `spa_fallback` is enabled, for a missing file
requested by a GET. This is suitable for React, Vue, and other history-mode
single-page applications. Directory listing is disabled by default and should
only be enabled for an intentionally public, controlled file tree.

Janus resolves the configured root once at startup and resolves each existing
requested file before serving it. A symlinked root is supported, but a symlink
inside that tree may not resolve outside the root; such a request returns 404.
Do not use a static route as a cross-directory mount mechanism.

The action preserves the standard Go HTTP file-serving behavior, including
content type detection, byte ranges, `Last-Modified`, and conditional
requests. `cache_control` is applied to files that are actually served, not to
method errors or missing-path responses.

For a mounted application, compose the action with the existing
`strip_prefix` route middleware. Keep API routes at a higher priority than a
catch-all `/` static route so that API requests are selected first:

```json
{
  "name": "api",
  "match": "PathPrefix(`/api`)",
  "priority": 100,
  "action": { "forward": { "service": "api" } }
}
```

The root path is intentionally absolute. Configurations can be loaded from a
startup file, the admin console, or a reload transaction, and resolving a
relative path against the process working directory would make those paths
ambiguous.
