#!/usr/bin/env bash
# GPL corresponding source and upstream build materials for the bundled core.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
# shellcheck source=versions.env
. "$root/scripts/versions.env"
source_dir="$root/.cache/mihomo-source"
commit=$MIHOMO_COMMIT
version=$MIHOMO_VERSION
mkdir -p "$root/.cache" "$root/build/notices"
if ! test -d "$source_dir"; then
  git clone --depth 1 --branch "$version" https://github.com/MetaCubeX/mihomo.git "$source_dir"
fi
test "$(git -C "$source_dir" rev-parse HEAD)" = "$commit"
test -z "$(git -C "$source_dir" status --porcelain)"
git -C "$source_dir" archive --format=tar --prefix="mihomo-$version/" HEAD | gzip -n > "$root/build/notices/mihomo-source.tar.gz"
cp "$source_dir/LICENSE" "$root/build/notices/MIHOMO-LICENSE.txt"
python3 - "$root/build/notices" "$commit" "$version" "$MIHOMO_BINARY_SHA256" <<'PY'
import hashlib, json, sys
from pathlib import Path
out = Path(sys.argv[1])
version = sys.argv[3]
(out / 'MIHOMO-SOURCE.json').write_text(json.dumps({
    'name': 'Mihomo', 'version': version, 'commit': sys.argv[2],
    'license': 'GPL-3.0', 'modifications': 'Unmodified official amd64-compatible binary; independent managed executable.',
    'source': 'https://github.com/MetaCubeX/mihomo/tree/' + sys.argv[2],
    'source_archive': 'mihomo-source.tar.gz',
    'source_archive_sha256': hashlib.sha256((out / 'mihomo-source.tar.gz').read_bytes()).hexdigest(),
    'binary': 'https://github.com/MetaCubeX/mihomo/releases/download/' + version + '/mihomo-linux-amd64-compatible-' + version + '.gz',
    'binary_sha256': sys.argv[4],
    'build_materials': 'The source archive contains the upstream Go modules, Makefile, build scripts and CI workflows. Install its pinned Go toolchain and follow the upstream Makefile/README; no source modifications are applied.',
}, indent=2) + '\n')
PY
printf 'Mihomo %s fixed GPL source and license bundled\n' "$version"
