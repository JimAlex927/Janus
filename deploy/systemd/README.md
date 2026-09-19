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

Before installing a candidate, run the repository Linux gate from the checked
out commit. It produces the same static artifact shape used by CI; archive the
printed checksum with the configuration and deployment record:

```sh
JANUS_LINUX_ARTIFACT=bin/janus-linux-amd64 ./scripts/verify-linux.sh
```

Install the binary and configuration with a dedicated account. The service
must be able to read the configuration and any TLS certificate/key files.
Keep the private key readable only by `root` and the `janus` group.

```sh
useradd --system --home-dir /nonexistent --shell /usr/sbin/nologin janus
install -o root -g root -m 0755 bin/janus-linux-amd64 /usr/local/bin/janus
install -d -o root -g janus -m 0770 /etc/janus
install -d -o root -g janus -m 0750 /etc/janus/certs
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

The unit repeats the `-check` validation as `ExecStartPre` on every start and
restart. A malformed replacement therefore fails before Janus binds either
business or admin sockets; systemd retains the previous process if the change
was deployed through a parallel rollout.

The unit deliberately keeps `/etc/janus` writable while `ProtectSystem=strict`
is enabled. Janus needs that directory for the SQLite configuration library,
atomic configuration replacement, and its temporary file; keep the certificate
directory and certificate/key files non-writable by the `janus` user as shown
above.

`TimeoutStopSec` is aligned with the default 35-second drain budget. If
`settings.shutdown.drain_timeout` is increased, increase the unit timeout with
some margin and verify that the load balancer removal delay is included in the
same drain budget.

## Rollout and rollback

For a route/service or certificate-only update, validate a new file first and
replace the live file with an atomic rename on the same filesystem. Janus also
flushes the containing directory after the rename so the replacement survives
a power loss once the publish returns successfully. The file reloader keeps the last valid generation when the new file is malformed or
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

The admin configuration library also keeps archived versions. After a process
crash during a publish, startup reconciles the `active` marker to an exact
matching historical file when one exists; inspect the journal for an
unrecognized-file warning before choosing an archived version to publish.

## Current qualification boundary

This artifact is syntax-checked and cross-compiled by the repository's release
checks and has passed a local Debian Linux container smoke. It has not yet been
installed under a real systemd host. The repository CI provides Linux Go
test/race/vet/static-build checks; systemd resource limits, load, soak, and
canary rollback still require a successful CI run or representative deployment
host before production certification.
