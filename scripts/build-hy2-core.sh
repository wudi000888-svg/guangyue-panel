#!/bin/bash
# Reproducible source patch: upstream Hysteria 2.9.2 with per-authenticated-node routes.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
patch="$root/core-patches/hysteria-node-routing.patch"
commit=c3a806b5cbbb20fe72099529573da26b1a2e9f22
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
[[ -x "$go_bin" ]] || { printf 'Go 1.26+ is required; install Go or set GY_GO.\n' >&2; exit 1; }
if ! test -d "$source_dir"; then
  git -c http.version=HTTP/1.1 clone --depth 1 --branch app/v2.9.2 https://github.com/apernet/hysteria.git "$source_dir"
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
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "$go_bin" build -trimpath -ldflags="-s -w -X github.com/apernet/hysteria/app/v2/cmd.appVersion=v2.9.2-guangyue-node1 -X github.com/apernet/hysteria/app/v2/cmd.appCommit=$commit" -o "$root/build/bin/hysteria-node-linux-amd64" .
printf 'Built Hysteria 2.9.2 + authenticated node routing, patch %s\n' "$patch_id"
