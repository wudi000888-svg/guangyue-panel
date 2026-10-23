#!/bin/bash
# Pinned official Linux amd64 build used by the shared airport egress process.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
# shellcheck source=versions.env
. "$root/scripts/versions.env"
output=${GY_BIN_DIR:-$root/build/bin}
mkdir -p "$output"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -fL --retry 2 --connect-timeout 15 --max-time 240 \
  "https://github.com/MetaCubeX/mihomo/releases/download/$MIHOMO_VERSION/mihomo-linux-amd64-compatible-$MIHOMO_VERSION.gz" \
  -o "$tmp/mihomo.gz"
gzip -dc "$tmp/mihomo.gz" > "$tmp/mihomo"
expected=$MIHOMO_BINARY_SHA256
if command -v sha256sum >/dev/null; then actual=$(sha256sum "$tmp/mihomo" | cut -d ' ' -f 1); else actual=$(shasum -a 256 "$tmp/mihomo" | cut -d ' ' -f 1); fi
test "$actual" = "$expected"
install -m 0755 "$tmp/mihomo" "$output/mihomo-linux-amd64"
printf 'Mihomo %s SHA256 verified and installed\n' "$MIHOMO_VERSION"
