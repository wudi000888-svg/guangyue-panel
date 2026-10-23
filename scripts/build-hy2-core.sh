#!/bin/bash
# Reproducible source patch: upstream Hysteria 2 (scripts/versions.env) with per-authenticated-node routes.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
# shellcheck source=versions.env
. "$root/scripts/versions.env"
patch="$root/core-patches/hysteria-node-routing.patch"
commit=$HY2_COMMIT
patch_id=$(shasum -a 256 "$patch" | cut -c1-16)
source_dir="$root/.cache/hysteria-node-$patch_id"
mkdir -p "$root/.cache"
if [[ -n "${GY_GO:-}" ]]; then
  go_bin="$GY_GO"
elif command -v go >/dev/null 2>&1; then
  go_bin=$(command -v go)
else
  go_bin=go
fi
# backend/go.mod's toolchain directive is the project's single Go version pin.
go_required=$(sed -n 's/^toolchain go//p' "$root/backend/go.mod" 2>/dev/null || true)
[[ -x "$go_bin" ]] || { printf 'Go %s+ is required; install Go or set GY_GO.\n' "${go_required:-(see backend/go.mod)}" >&2; exit 1; }
if ! test -d "$source_dir"; then
  git -c http.version=HTTP/1.1 clone --depth 1 --branch "app/$HY2_VERSION" https://github.com/apernet/hysteria.git "$source_dir"
fi
test "$(git -C "$source_dir" rev-parse HEAD)" = "$commit"
if git -C "$source_dir" apply --check "$patch" 2>/dev/null; then
  git -C "$source_dir" apply "$patch"
else
  git -C "$source_dir" apply --reverse --check "$patch"
fi
mkdir -p "$root/build/bin"
cd "$source_dir/app"
if test "${1:-}" = --test; then
  "$go_bin" test -race ./cmd -run 'TestNodeOutboundConfigAndUnknownIdentity|TestServerConfig' -count=1
  (cd ../core && "$go_bin" test -race ./internal/integration_tests -run 'TestNodeOutboundIsolationTCPAndUDP|TestClientServerTCPEcho|TestClientServerUDPEcho' -count=1)
fi
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "$go_bin" build -trimpath -ldflags="-s -w -X github.com/apernet/hysteria/app/v2/cmd.appVersion=$HY2_VERSION-guangyue-node1 -X github.com/apernet/hysteria/app/v2/cmd.appCommit=$commit" -o "$root/build/bin/hysteria-node-linux-amd64" .
printf 'Built Hysteria %s + authenticated node routing, patch %s\n' "${HY2_VERSION#v}" "$patch_id"
