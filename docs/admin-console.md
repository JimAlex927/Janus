# Janus Admin Console

Janus includes an optional private HTTP administration console. The frontend
source lives in `frontend/` and is built independently with Vite; the release
binary embeds the resulting static files under `internal/admin/ui/`.

## Configuration

Set `settings.admin.address` to enable the listener. The address must be a
loopback or private-network address. Set `username` and a bcrypt
`password_hash` to enable login protection and configuration publishing:

```json
"admin": {
  "address": "192.168.10.20:9090",
  "username": "admin",
  "password_hash": "$2a$..."
}
```

The example hash is only a development placeholder and must be replaced before
deployment. Health probes remain available at `/livez` and `/readyz`; the
console API requires the configured session when credentials are present.

Generate a replacement hash without putting the password in shell history:

```text
go run ./cmd/janus-hash
```

For automation, pipe a protected password file through standard input:

```text
Get-Content -Raw .\password.txt | go run ./cmd/janus-hash -password-stdin
```

The command writes only the bcrypt hash to standard output. Copy that value to
`settings.admin.password_hash` and restart Janus.

## Configuration lifecycle

The console keeps a browser draft separate from the running configuration. A
publish request is checked against `X-Janus-Revision`, validated, built as a
new Runtime generation, and then persisted with an atomic rename. If
persistence fails, the previous generation is restored. Settings that affect
listeners or process-wide request infrastructure remain startup-owned; a
configuration containing those changes is rejected by Runtime and must be
applied after a restart.

The API also remains compatible with direct atomic edits of the JSON file. The
file reloader recognizes a snapshot already published by the console and does
not build a duplicate generation.

## Visual configuration canvas

The `配置画布` page has two synchronized modes:

- `画布` mode shows only Limen and Route nodes. Service and Middleware are
  managed in their own resource pages and referenced from the Route editor.
  Nodes can be added from the palette, dragged around the canvas, zoomed,
  automatically arranged, and edited in a standalone node editor modal. Click
  or drag from a node's output port to another node's input port (or the
  highlighted target node) to create a supported relation.
- Node parameters open in a type-specific modal from the card's `…` action.
  Route editing includes a larger Match text area and an inline Middleware flow
  with create, select, and explicit reorder controls; closing the modal leaves
  the canvas layout unchanged.
- Limen protocol fields use fixed multi-select options backed by the supported
  transport protocol matrix. Route application protocols are written in the
  Match DSL with `Protocol(...)`. Middleware creation and parameter editing expose
  only the policies supported by the current scope: Route supports `buffer`
  and `body_limit`, while Service additionally supports `in_flight`. Existing
  invalid references remain visible so an operator can remove them without
  falling back to JSON. Middleware names can be edited in place; references in
  every Route and Service are updated atomically. Each new Middleware declares
  a `scope` of `route`, `service`, or the backwards-compatible shared scope.
- The canvas includes a request simulation drawer. It evaluates the current
  browser draft, accepts a full URL or path plus Host and Header fields, shows
  the winning Route, Action, Middleware chain, and Service, and lists every
  candidate's match result without sending traffic.
- `JSON` mode keeps the complete configuration available for advanced fields.
  Both modes edit the same browser draft and use the same validation and
  publish actions.
- The Limen inspector exposes trusted proxies, TLS, and HTTP/3 settings;
  Service health checks expose their complete timing and threshold controls.
- Visual and JSON edits stay in a browser draft until published. The console
  shows whether the draft is synced, disables publish for an unchanged or
  invalid draft, and provides undo/redo for canvas edits (rapid typing is
  grouped into one edit).
- A runtime generation event never overwrites a dirty local draft. Manual
  refresh explicitly confirms discarding it; publishing reloads the new
  generation automatically.

Canvas coordinates are stored only in the browser's local storage and are not
part of the runtime configuration. This keeps the published JSON stable while
allowing each operator to arrange the graph independently.

## Frontend development

```text
cd frontend
npm install
npm run dev
npm run test:model
```

The Vite development server proxies `/api`, `/metrics`, and `/readyz` to the
local Admin listener. A release build is copied to `internal/admin/ui/` before
`go build` so the Go `embed` package has the same UI that was reviewed in the
frontend build. The graph model, canvas, and type-specific node editors are
kept in separate modules. Deterministic model tests cover Service visibility,
connection semantics, missing references, and canvas bounds.
