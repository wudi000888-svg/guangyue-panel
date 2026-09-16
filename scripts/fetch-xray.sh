#!/usr/bin/env bash
# New releases include Xray. Preserve and verify it; old source trees may fetch upstream.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
output=${GY_BIN_DIR:-$root/build/bin}
if test -f "$root/SHA256SUMS"; then output=${GY_BIN_DIR:-$root/bin}; fi
mkdir -p "$output"
if test -f "$root/SHA256SUMS"; then
  python3 - "$root" "$output" <<'PY'
import hashlib, re, sys
from pathlib import Path
root, output = map(Path, sys.argv[1:])
entries = [row.split('  ', 1) for row in (root / 'SHA256SUMS').read_text().splitlines()]
matches = [h for h, n in entries if n == 'bin/xray-linux-amd64']
if len(matches) != 1 or not re.fullmatch('[a-f0-9]{64}', matches[0]):
    raise SystemExit('Bundled Xray manifest missing or invalid; obtain a complete release.')
if hashlib.sha256((output / 'xray-linux-amd64').read_bytes()).hexdigest() != matches[0]:
    raise SystemExit('Bundled Xray checksum mismatch; obtain a complete release.')
print('Bundled Xray session extension verified; no download needed.')
PY
  exit 0
fi
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -fL --retry 2 --connect-timeout 15 --max-time 240 \
  https://github.com/XTLS/Xray-core/releases/download/v26.3.27/Xray-linux-64.zip -o "$tmp/xray.zip"
python3 - "$tmp/xray.zip" "$tmp/xray" <<'PY'
import hashlib,sys,zipfile
from pathlib import Path
archive=Path(sys.argv[1])
if hashlib.sha256(archive.read_bytes()).hexdigest() != '23cd9af937744d97776ee35ecad4972cf4b2109d1e0fe6be9930467608f7c8ae':
    raise SystemExit('Xray checksum mismatch')
with zipfile.ZipFile(archive) as z:
    Path(sys.argv[2]).write_bytes(z.read('xray'))
PY
install -m 0755 "$tmp/xray" "$output/xray-linux-amd64"
printf 'Xray 26.3.27 archive SHA256 verified and installed\n'
