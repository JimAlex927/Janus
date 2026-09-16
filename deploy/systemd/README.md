# Native Linux deployment

`janus.service` is a minimal systemd artifact for a native Linux deployment.
It runs Janus as the dedicated unprivileged `janus` user, sends logs to the
journal, and applies starter resource and filesystem budgets. The limits are
guardrails, not a capacity claim; tune them from measured workload evidence.

## Install

Build the binary on a pinned, patched Go toolchain. For a static amd64 Linux
artifact from another host:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags='-s -w' -o bin/janus-linux-amd64 ./cmd/janus
sha256sum bin/janus-linux-amd64
```

Install the binary and configuration with a dedicated account. The service
must be able to read the configuration and any TLS certificate/key files.
Keep the private key readable only by `root` and the `janus` group.

```sh
useradd --system --home-dir /nonexistent --shell /usr/sbin/nologin janus
install -o root -g root -m 0755 bin/janus-linux-amd64 /usr/local/bin/janus
install -d -o root -g janus -m 0750 /etc/janus /etc/janus/certs
install -o root -g janus -m 0640 janus.json /etc/janus/janus.json
install -o root -g janus -m 0640 janus.crt /etc/janus/certs/janus.crt
install -o root -g janus -m 0640 janus.key /etc/janus/certs/janus.key
install -o root -g root -m 0644 deploy/systemd/janus.service \
  /etc/systemd/system/janus.service
```

Use a private/loopback admin address in the configuration. Before enabling the
unit, validate the complete configuration and inspect its normalized form:

```sh
/usr/local/bin/janus -check -config /etc/janus/janus.json
/usr/local/bin/janus -print-effective-config -config /etc/janus/janus.json
systemctl daemon-reload
systemctl enable --now janus
systemctl status janus
```

The effective view includes defaults and normalized legacy Limen syntax, but
does not include TLS certificate or private-key asset paths. It is suitable for
review output; it is not a secret store or a readiness check.

`TimeoutStopSec` is aligned with the default 35-second drain budget. If
`settings.shutdown.drain_timeout` is increased, increase the unit timeout with
some margin and verify that the load balancer removal delay is included in the
same drain budget.

## Rollout and rollback

For a route/service or certificate-only update, validate a new file first and
replace the live file with an atomic rename on the same filesystem. The file
reloader keeps the last valid generation when the new file is malformed or
incomplete. Keep at least one previous configuration and certificate pair until
the new generation and readiness probe have been observed.

For listener, protocol, global limit, transport, or TLS-policy changes, use a
versioned binary/configuration rollout: start the new instance on its own
address or behind a load balancer, wait for `/readyz`, shift traffic, then
drain the old instance. Do not assume a reload can change those startup-owned
settings.

To roll back a dynamic update, atomically restore the previous known-good file
and wait for the reload outcome in the journal. To roll back a binary or
startup configuration, stop traffic to the new instance, restore the previous
binary/configuration, and restart it; retain the old artifact checksum so the
rollback is reproducible.

## Current qualification boundary

This artifact is syntax-checked and cross-compiled by the repository's release
checks, but it has not been run here because the development host does not have
Docker or a Linux systemd environment. The repository CI provides Linux Go
test/race/vet/static-build checks; Linux signal/socket behavior, systemd
resource limits, load, soak, and canary rollback still require a successful CI
run or representative deployment host before production certification.
