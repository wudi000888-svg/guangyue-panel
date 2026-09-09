#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
go_bin=${GY_GO:-go}
mkdir -p "$root/build/bin"
(cd "$root/frontend" && npm ci --ignore-scripts && npm run build)
(cd "$root/backend" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "$go_bin" build -trimpath -ldflags='-s -w' -o "$root/build/bin/guangyue-linux-amd64" .)
printf 'Application and web built. Build HY2 with scripts/build-hy2-core.sh --test.\n'
