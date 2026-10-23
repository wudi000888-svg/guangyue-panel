#!/usr/bin/env bash
# Complete project check. With no arguments every section runs serially, as
# before. CI runs the same sections in parallel jobs, for example:
#   bash scripts/check.sh catalog release        # lint job
#   GY_SQLITE_SHARD=2 bash scripts/check.sh sqlite
# Sections: catalog sqlite postgres vet frontend deploy release (default: all).
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
go_bin=${GY_GO:-go}

catalog() { python3 "$root/scripts/sync-client-catalog.py" --check; }
sqlite() {
  # Keep every backend test and each process's existing ten-minute limit.
  # Redis configuration remains unchanged; PostgreSQL integration runs
  # separately so replaying business invariants does not double the Lite run.
  (cd "$root/backend" && python3 "$root/scripts/test-sqlite-backend.py" "$go_bin" ${GY_SQLITE_SHARD:+--shard "$GY_SQLITE_SHARD"})
}
postgres() {
  local postgres_test_dsn=${GY_TEST_POSTGRES_DSN:-}
  if [[ -z "$postgres_test_dsn" ]]; then
    printf 'GY_TEST_POSTGRES_DSN is not set; skipping PostgreSQL integration tests.\n'
    return 0
  fi
  (cd "$root/backend" && GY_TEST_POSTGRES_DSN="$postgres_test_dsn" "$go_bin" test -race ./... -run '^TestPostgres' -count=1)
}
vet() { (cd "$root/backend" && "$go_bin" vet ./...); }
frontend() { (cd "$root/frontend" && npm ci --ignore-scripts && npm test && npm run build); }
deploy() { python3 -m unittest discover -s "$root/deploy/tests" -v; }
release() { python3 "$root/scripts/release-check.py"; }

sections=("$@")
[[ ${#sections[@]} -gt 0 ]] || sections=(catalog sqlite postgres vet frontend deploy release)
for section in "${sections[@]}"; do
  case "$section" in
    catalog|sqlite|postgres|vet|frontend|deploy|release) "$section" ;;
    *) printf 'Unknown check section: %s\n' "$section" >&2; exit 2 ;;
  esac
done
