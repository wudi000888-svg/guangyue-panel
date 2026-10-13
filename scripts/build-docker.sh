#!/usr/bin/env bash
# Build once from the verified native artifact; installations only load images.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
version=$(cat "$root/VERSION")
bundle=
out="$root/build/docker-releases"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --bundle) bundle=$2; shift 2 ;;
    --output) out=$2; shift 2 ;;
    *) printf 'Unknown build option: %s\n' "$1" >&2; exit 2 ;;
  esac
done
command -v docker >/dev/null || { printf 'Docker is required to build the image artifact.\n' >&2; exit 1; }
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || exit 2
mkdir -p "$out"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
if [[ -z "$bundle" ]]; then
  archive="$root/build/releases/guangyue-panel-pro-$version-linux-amd64.tar.gz"
  python3 - "$root" "$archive" "$work/bundle" "$version" <<'PY'
import sys
from pathlib import Path
root, archive, destination = map(Path, sys.argv[1:4])
sys.path.insert(0, str(root / 'deploy'))
import oneclick
rows = oneclick.manifest_rows((archive.parent / 'SHA256SUMS').read_text())
if rows.get(archive.name) != oneclick.sha256(archive):
    raise SystemExit('Native build artifact checksum mismatch')
oneclick.safe_extract(archive, destination, archive.name.removesuffix('.tar.gz'))
PY
  bundle="$work/bundle"
fi
python3 - "$root" "$bundle" "$version" <<'PY'
import sys
from pathlib import Path
root, bundle = map(Path, sys.argv[1:3])
sys.path.insert(0, str(root / 'deploy'))
import oneclick
oneclick.verify_bundle(bundle, {'version': sys.argv[3], 'edition': 'pro'})
for source in ('licenses/mihomo-source.tar.gz', 'licenses/MIHOMO-LICENSE.txt', 'licenses/xray-source.tar.gz', 'licenses/go-ethereum-source.tar.gz'):
    if not (bundle / source).is_file():
        raise SystemExit('Required corresponding source missing: ' + source)
PY
context="$work/context"
mkdir -p "$context"
for item in bin web licenses VERSION; do cp -R "$bundle/$item" "$context/$item"; done
cp "$root/deploy/docker/Dockerfile" "$context/Dockerfile"
cp "$root/deploy/docker/entrypoint.sh" "$context/entrypoint.sh"
postgres='postgres:17-bookworm@sha256:66aafa11cf15800a3c94763f7e11d1e7b2e37e5e84bc4ce027cb6e1e4bfaf4df'
redis='redis:8-bookworm@sha256:a1da393d1b8099236b645f1471d2e93bcdcdcbbf7e829b38bcc9b71227f0a901'
app="guangyue-panel-app:$version-linux-amd64"
docker pull --platform linux/amd64 "$postgres"
docker pull --platform linux/amd64 "$redis"
python3 "$root/scripts/docker-provenance.py" "$context/licenses" "$root/.cache/docker-source" "$postgres" "$redis"
docker build --platform linux/amd64 --build-arg "PANEL_VERSION=$version" -t "$app" "$context"
archive="$out/guangyue-panel-docker-$version-linux-amd64.tar.gz"
app_id=$(docker image inspect --format '{{.Id}}' "$app")
postgres_id=$(docker image inspect --format '{{.Id}}' "$postgres")
redis_id=$(docker image inspect --format '{{.Id}}' "$redis")
for image in "$app_id" "$postgres_id" "$redis_id"; do
  [[ "$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$image")" == linux/amd64 ]] || { printf 'Image platform mismatch\n' >&2; exit 1; }
done
docker save "$app_id" "$postgres_id" "$redis_id" | gzip -n > "$archive.part"
mv "$archive.part" "$archive"
python3 - "$out" "$version" "$app_id" "$postgres_id" "$redis_id" "$postgres" "$redis" <<'PY'
import hashlib, json, sys
from pathlib import Path
out = Path(sys.argv[1]); version = sys.argv[2]
archive = out / ('guangyue-panel-docker-' + version + '-linux-amd64.tar.gz')
def sha(path):
    h = hashlib.sha256()
    with path.open('rb') as stream:
        for block in iter(lambda: stream.read(1 << 20), b''): h.update(block)
    return h.hexdigest()
manifest = out / 'DOCKER_IMAGES.json'
manifest.write_text(json.dumps({'schema': 1, 'version': version, 'platform': 'linux/amd64', 'archive': archive.name, 'sha256': sha(archive), 'images': {
    'app': {'id': sys.argv[3]},
    'postgres': {'id': sys.argv[4], 'source': sys.argv[6]},
    'redis': {'id': sys.argv[5], 'source': sys.argv[7]},
}}, indent=2) + '\n')
(out / 'SHA256SUMS').write_text('\n'.join(sha(p) + '  ' + p.name for p in [archive, manifest]) + '\n')
PY
printf 'Built verified linux/amd64 application, PostgreSQL and Redis image archive for %s\n' "$version"
