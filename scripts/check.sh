#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
go_bin=${GY_GO:-go}
python3 "$root/scripts/sync-client-catalog.py" --check
(
  cd "$root/backend"
  postgres_test_dsn=${GY_TEST_POSTGRES_DSN:-}
  # Keep every backend test and each process's existing ten-minute limit.
  # Redis configuration remains unchanged; PostgreSQL integration runs
  # separately so replaying business invariants does not double the Lite run.
  python3 "$root/scripts/test-sqlite-backend.py" "$go_bin"
  if [[ -n "$postgres_test_dsn" ]]; then
    GY_TEST_POSTGRES_DSN="$postgres_test_dsn" "$go_bin" test -race ./... -run '^TestPostgres' -count=1
  fi
  "$go_bin" vet ./...
)
(cd "$root/frontend" && npm ci --ignore-scripts && npm test && npm run build)
python3 -m unittest discover -s "$root/deploy/tests" -v
python3 "$root/scripts/release-check.py"
