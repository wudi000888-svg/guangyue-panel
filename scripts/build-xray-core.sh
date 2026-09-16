#!/usr/bin/env bash
# Fixed upstream source plus the MPL-2.0 user session gauge extension.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
source_dir="$root/.cache/xray-monitor-source"
patch="$root/core-patches/xray-user-sessions.patch"
commit=d2758a023cd7f4174a5a5fa4ff66e487d4342ba0
go_bin=${GY_GO:-go}
mkdir -p "$root/.cache" "$root/build/bin"
if ! test -d "$source_dir"; then
  git clone --depth 1 --branch v26.3.27 https://github.com/XTLS/Xray-core.git "$source_dir"
fi
test "$(git -C "$source_dir" rev-parse HEAD)" = "$commit"
if git -C "$source_dir" apply --check "$patch" 2>/dev/null; then
  git -C "$source_dir" apply "$patch"
else
  git -C "$source_dir" apply --reverse --check "$patch"
fi
cd "$source_dir"
if test "${1:-}" = --test; then
  "$go_bin" test -race ./app/dispatcher ./app/stats/... -count=1
  "$go_bin" build -trimpath -o "$root/.cache/xray-test" ./main
  python3 "$root/scripts/test-xray-sessions.py" "$root/.cache/xray-test"
fi
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "$go_bin" build -trimpath -ldflags="-s -w -X github.com/xtls/xray-core/core.build=guangyue-sessions1" -o "$root/build/bin/xray-linux-amd64" ./main
python3 - "$source_dir" "$root/build/xray-source.tar.gz" <<'PY'
import gzip, subprocess, sys, tarfile
from pathlib import Path
source, destination = map(Path, sys.argv[1:])
files = set(subprocess.check_output(['git', 'ls-files'], cwd=source).decode().splitlines())
files.add('app/dispatcher/guangyue_sessions.go')
with destination.open('wb') as output, gzip.GzipFile(filename='', mode='wb', fileobj=output, mtime=0) as compressed, tarfile.open(fileobj=compressed, mode='w') as archive:
    for name in sorted(files):
        path = source / name
        if not path.is_file(): continue
        info = archive.gettarinfo(str(path), arcname='xray-core/' + name)
        info.uid = info.gid = info.mtime = 0
        info.uname = info.gname = 'root'
        with path.open('rb') as content: archive.addfile(info, content)
PY
printf 'Built Xray 26.3.27 + user session gauges\n'
