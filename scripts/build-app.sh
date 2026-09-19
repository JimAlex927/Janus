#!/bin/sh
set -eu

# Build the reviewed Admin Console, refresh the Go embed, then produce a
# small, static Janus binary. Override JANUS_OUTPUT/GOOS/GOARCH as needed.
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
frontend="$root/frontend"
dist="$frontend/dist"
embed="$root/internal/admin/ui"
goos=${GOOS:-$(go env GOOS)}
goarch=${GOARCH:-$(go env GOARCH)}
output=${JANUS_OUTPUT:-$root/bin/janus}
ui_base=${JANUS_UI_BASE_URL:-}

die() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

command -v node >/dev/null 2>&1 || die "node is required"
command -v npm >/dev/null 2>&1 || die "npm is required"
command -v go >/dev/null 2>&1 || die "go is required"

case "$ui_base" in
  ""|/) ui_base="" ;;
  /*)
    case "$ui_base" in *[!A-Za-z0-9._/-]*) die "JANUS_UI_BASE_URL must be empty or an absolute URL path such as /janus" ;; esac
    ui_base=${ui_base%/}
    case "$ui_base" in *"//"*) die "JANUS_UI_BASE_URL must not contain empty path segments" ;; esac
    ;;
  *) die "JANUS_UI_BASE_URL must be empty or an absolute URL path such as /janus" ;;
esac

if [ "$goos" = "windows" ]; then
  case "$output" in
    *.exe) ;;
    *) output="$output.exe" ;;
  esac
fi

printf '%s\n' '==> building Admin Console'
if [ "${JANUS_INSTALL_DEPS:-0}" = "1" ] || [ ! -d "$frontend/node_modules" ]; then
  (cd "$frontend" && npm ci)
fi
(cd "$frontend" && JANUS_UI_BASE_URL="$ui_base" npm run build)

test -f "$dist/index.html" || die "frontend build did not produce dist/index.html"
test -d "$dist/assets" || die "frontend build did not produce dist/assets"

printf '%s\n' '==> synchronizing embedded Admin Console'
mkdir -p "$embed/assets"
find "$embed/assets" -mindepth 1 -maxdepth 1 -type f -delete
cp "$dist/index.html" "$embed/index.html"
find "$dist/assets" -mindepth 1 -maxdepth 1 -type f -exec cp {} "$embed/assets/" \;
"$root/scripts/verify-embedded-ui.sh"

mkdir -p "$(dirname "$output")"
printf '%s\n' "==> building $goos/$goarch -> $output"
CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
  go build -trimpath -buildvcs=false -ldflags="-s -w -buildid= -X janus/internal/admin.uiBaseURL=$ui_base" -o "$output" ./cmd/janus

if [ "${JANUS_UPX:-0}" = "1" ]; then
  command -v upx >/dev/null 2>&1 || die "JANUS_UPX=1 requires upx in PATH"
  printf '%s\n' '==> applying optional UPX compression'
  upx --best --lzma "$output"
fi

test -f "$output" || die "Go build did not produce $output"
if command -v shasum >/dev/null 2>&1; then
  shasum -a 256 "$output"
fi
printf 'built %s (%s bytes)\n' "$output" "$(wc -c < "$output" | tr -d ' ')"
