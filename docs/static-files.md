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

Each Gateway generation opens the configured directory once with `os.OpenRoot`.
Files, indexes, SPA fallbacks and directory listings are opened relative to that
handle; `Stat` and `ServeContent` use the same opened file. This removes the old
check-then-open symlink race. Replacing the root pathname does not change an
already running generation's directory; reload to adopt the replacement.
The handle closes after that generation drains, or on candidate build failure.
A symlinked root is supported. Relative symlinks contained inside it are allowed;
absolute symlinks and escaping symlinks are rejected (404), even if an absolute
symlink happens to point back inside. Use relative symlinks for internal assets.
This is path confinement, not a filesystem sandbox: trusted operators must still
control hard links, mounts and the contents of the served directory.

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
