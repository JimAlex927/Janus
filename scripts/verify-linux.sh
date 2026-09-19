#!/bin/sh
set -eu

# This is the repeatable Linux release gate. Keep it aligned with CI and the
# production-readiness runbook; deployment and canary checks remain separate.
artifact=${JANUS_LINUX_ARTIFACT:-janus-linux-amd64}

go mod verify
go test -count=2 ./...
go test -fuzz=FuzzLoadNeverPanics -fuzztime="${JANUS_FUZZ_TIME:-10s}" ./internal/config
go test -race -count=1 ./...
go vet ./...

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags="-s -w" -o "$artifact" ./cmd/janus

test -x "$artifact"
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum "$artifact"
else
  shasum -a 256 "$artifact"
fi
