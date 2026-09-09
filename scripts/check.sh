#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
go_bin=${GY_GO:-go}
(cd "$root/backend" && "$go_bin" test -race ./... -count=1 && "$go_bin" vet ./...)
(cd "$root/frontend" && npm ci --ignore-scripts && npm test && npm run build)
python3 -m unittest discover -s "$root/deploy/tests" -v
python3 "$root/scripts/release-check.py"
