#!/usr/bin/env bash
# Publish only after all four assets have been downloaded and verified again.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
tag=${1:?version tag required}
test "$tag" = "v$(cat VERSION)"
version=${tag#v}
assets=("build/releases/guangyue-panel-lite-$version-linux-amd64.tar.gz" "build/releases/guangyue-panel-pro-$version-linux-amd64.tar.gz" build/releases/SHA256SUMS build/releases/SBOM.spdx.json)
for file in "${assets[@]}"; do test -s "$file"; done
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
python3 - "$version" "$tmp/notes.md" <<'PY'
from pathlib import Path
import sys
version, destination = sys.argv[1:]
entry = Path('CHANGELOG.md').read_text().split('## ' + version + ' ', 1)[1].split('\n## ', 1)[0]
Path(destination).write_text('## ' + version + ' ' + entry.strip() + '\n')
PY
if gh release view "$tag" --json isDraft >/dev/null 2>&1; then
  test "$(gh release view "$tag" --json isDraft --jq .isDraft)" = true || { echo 'Published releases are immutable; use a new version.'; exit 1; }
  gh release upload "$tag" "${assets[@]}" --clobber
  gh release edit "$tag" --notes-file "$tmp/notes.md"
else
  gh release create "$tag" --verify-tag --draft --title "广月面板 $tag" --notes-file "$tmp/notes.md" "${assets[@]}"
fi
mkdir "$tmp/download"
gh release download "$tag" --dir "$tmp/download"
test "$(find "$tmp/download" -type f | wc -l)" -eq 4
cmp build/releases/SHA256SUMS "$tmp/download/SHA256SUMS"
(cd "$tmp/download" && sha256sum -c SHA256SUMS)
gh release edit "$tag" --draft=false --latest
