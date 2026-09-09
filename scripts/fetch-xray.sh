#!/usr/bin/env bash
# Downloads the pinned upstream binary directly; it is not part of our release archive.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
output=${GY_BIN_DIR:-$root/build/bin}
mkdir -p "$output"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -fL --retry 2 --connect-timeout 15 --max-time 240 \
  https://github.com/XTLS/Xray-core/releases/download/v26.3.27/Xray-linux-64.zip -o "$tmp/xray.zip"
python3 - "$tmp/xray.zip" "$tmp/xray" <<'PY'
import hashlib,sys,zipfile
from pathlib import Path
archive=Path(sys.argv[1])
assert hashlib.sha256(archive.read_bytes()).hexdigest() == '23cd9af937744d97776ee35ecad4972cf4b2109d1e0fe6be9930467608f7c8ae', 'Xray checksum mismatch'
with zipfile.ZipFile(archive) as z:
    Path(sys.argv[2]).write_bytes(z.read('xray'))
PY
install -m 0755 "$tmp/xray" "$output/xray-linux-amd64"
printf 'Xray 26.3.27 archive SHA256 verified and installed\n'
