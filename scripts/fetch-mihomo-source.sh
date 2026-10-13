#!/usr/bin/env bash
# GPL corresponding source and upstream build materials for the bundled core.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
source_dir="$root/.cache/mihomo-source"
commit=ac017cdd246ce8bd547653d927e7bf77d7ee73d5
mkdir -p "$root/.cache" "$root/build/notices"
if ! test -d "$source_dir"; then
  git clone --depth 1 --branch v1.19.30 https://github.com/MetaCubeX/mihomo.git "$source_dir"
fi
test "$(git -C "$source_dir" rev-parse HEAD)" = "$commit"
test -z "$(git -C "$source_dir" status --porcelain)"
git -C "$source_dir" archive --format=tar --prefix=mihomo-v1.19.30/ HEAD | gzip -n > "$root/build/notices/mihomo-source.tar.gz"
cp "$source_dir/LICENSE" "$root/build/notices/MIHOMO-LICENSE.txt"
python3 - "$root/build/notices" "$commit" <<'PY'
import hashlib, json, sys
from pathlib import Path
out = Path(sys.argv[1])
(out / 'MIHOMO-SOURCE.json').write_text(json.dumps({
    'name': 'Mihomo', 'version': 'v1.19.30', 'commit': sys.argv[2],
    'license': 'GPL-3.0', 'modifications': 'Unmodified official amd64-compatible binary; independent managed executable.',
    'source': 'https://github.com/MetaCubeX/mihomo/tree/' + sys.argv[2],
    'source_archive': 'mihomo-source.tar.gz',
    'source_archive_sha256': hashlib.sha256((out / 'mihomo-source.tar.gz').read_bytes()).hexdigest(),
    'binary': 'https://github.com/MetaCubeX/mihomo/releases/download/v1.19.30/mihomo-linux-amd64-compatible-v1.19.30.gz',
    'binary_sha256': '8ad44e28fe72be4640254b96741b677f4074991b99186cc4486a1c28ded02b1a',
    'build_materials': 'The source archive contains the upstream Go modules, Makefile, build scripts and CI workflows. Install its pinned Go toolchain and follow the upstream Makefile/README; no source modifications are applied.',
}, indent=2) + '\n')
PY
printf 'Mihomo v1.19.30 fixed GPL source and license bundled\n'
