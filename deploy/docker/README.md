# Docker deployment

This Compose setup builds the Janus binary and Admin Console, runs as the
unprivileged `65532` user, and persists the active config and SQLite config
library under `deploy/docker/data`. The example config is copied there for this
checkout; that directory is git-ignored so local routes and credentials stay
out of commits.

## Start on Windows with Docker Desktop

From the repository root, with Docker Desktop using Linux containers:

```powershell
New-Item -ItemType Directory -Force deploy/docker/data | Out-Null
if (!(Test-Path deploy/docker/data/janus.json)) { Copy-Item deploy/docker/janus.json.example deploy/docker/data/janus.json }
if (!(Test-Path .env)) { Copy-Item .env.example .env }
docker compose up -d --build
docker compose logs -f janus
```

Before exposing the Admin Console beyond local development, replace the
example `settings.admin.password_hash`. Generate a bcrypt hash with
`go run ./cmd/janus-hash`, then update `deploy/docker/data/janus.json`.

The gateway is published on `http://localhost:8081`. The Admin Console is
published only on the host loopback at `http://127.0.0.1:9090`.

The checked-out `.env` points the static route at the current Windows files
directory. Change `JANUS_FILES_DIR` in `.env` to another host folder when needed.
Use forward slashes in Windows paths. Compose mounts that folder read-only at
`/srv/janus/files`, which is the root configured for the `/files` route.

Backend services and Nacos keep the addresses from the Windows example. A
container can reach LAN services when the Docker host has a route to them. If a
backend runs directly on the Docker host, change its upstream to
`http://host.docker.internal:<port>`.

The Admin Console config is mounted read/write so its save action persists to
`deploy/docker/data/janus.json`. The SQLite config library is stored beside it
and is also persistent. Back up this directory before changing or removing the
container.

## Network settings

Compose assigns Janus `172.31.240.2` because the Admin Console only permits a
loopback or private bind address. The matching config address is
`172.31.240.2:9090`; Compose forwards the host's loopback port 9090 to it. If
that subnet overlaps an existing Docker or LAN network, choose another unused
private subnet in `compose.yaml` and update the Admin address in
`deploy/docker/data/janus.json` to the new Janus container IP.

The current config enables HTTP/1 on port 8081. If you enable HTTP/3 later,
publish the configured UDP port in `compose.yaml` as well.

Stop Janus with:

```powershell
docker compose down
```

This removes the container and network while keeping the config directory and
its database on the host.
