#!/usr/bin/env bash
# Triggered only by successful, complete CI runs from this repository.
set -euo pipefail
: "${TESTED_SHA:?}" "${TESTED_BRANCH:?}" "${TESTED_EVENT:?}"
test "$(git rev-parse HEAD)" = "$TESTED_SHA"
git fetch origin main --tags
git config user.name 'github-actions[bot]'
git config user.email '41898282+github-actions[bot]@users.noreply.github.com'
repo=${GITHUB_REPOSITORY:?}

# Dispatches occasionally fail transiently, or race a branch that was just
# pushed; a lost dispatch would stall the release, so retry a few times.
dispatch() {
  local attempt
  for attempt in 1 2 3 4 5; do
    gh workflow run "$@" && return 0
    printf 'gh workflow run %s failed (attempt %s); retrying\n' "$*" "$attempt" >&2
    sleep $((attempt * 10))
  done
  return 1
}

# Default GITHUB_TOKEN writes do not trigger push/PR workflows. Dispatch CI
# explicitly at both transitions, then resume here only after it succeeds.
if [[ "$TESTED_BRANCH" == codex/auto-release-* ]]; then
  test "$TESTED_EVENT" = workflow_dispatch || exit 0
  pr=$(gh pr list --base main --head "$TESTED_BRANCH" --state open --json number --jq '.[0].number // empty')
  test -n "$pr" || exit 0
  test "$(gh pr view "$pr" --json headRefOid --jq .headRefOid)" = "$TESTED_SHA" || exit 0
  # -R keeps gh from touching the detached local checkout; only the remote
  # bump branch is deleted.
  gh pr merge "$pr" -R "$repo" --squash --delete-branch --match-head-commit "$TESTED_SHA" --subject "release: v$(cat VERSION) [skip release]"
  dispatch ci.yml --ref main
  exit 0
fi

test "$TESTED_BRANCH" = main || exit 0
# An older main run must never overwrite a newer release preparation.
test "$(git rev-parse origin/main)" = "$TESTED_SHA" || exit 0
version=$(cat VERSION)
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]
subject=$(git log -1 --format=%s)
if [[ "$subject" == "release: v${version} [skip release]"* ]]; then
  tag="v${version}"
  if git rev-parse --verify "refs/tags/$tag" >/dev/null 2>&1; then
    test "$(git rev-parse "$tag^{commit}")" = "$TESTED_SHA"
  else
    git tag -a "$tag" "$TESTED_SHA" -m "广月面板 $tag"
    git push origin "$tag"
  fi
  # Published releases are immutable; a repeated CI completion is a no-op.
  if test "$(gh release view "$tag" --json isDraft --jq .isDraft 2>/dev/null || true)" != false; then
    dispatch release.yml --ref main -f tag="$tag"
  fi
  exit 0
fi
[[ "$subject" != *'[skip release]'* ]] || exit 0
version=$(python3 scripts/bump-version.py)
branch="codex/auto-release-v${version}-${TESTED_SHA:0:12}"
pr=$(gh pr list --base main --head "$branch" --state open --json number --jq '.[0].number // empty')
if test -z "$pr"; then
  git switch -c "$branch"
  git add VERSION backend/internal/controlplane/config.go frontend/package.json frontend/package-lock.json README.md CHANGELOG.md
  git commit -m "release: v${version} [skip release]"
  git push origin "$branch"
  body=$(mktemp)
  trap 'rm -f "$body"' EXIT
  cat > "$body" <<BODY
Bump the tested main commit to v${version}. CI on this branch verifies that the diff is exactly \`scripts/bump-version.py\` applied to main and runs the release tree checks; the required checks report from that run.

CI is explicitly dispatched because PRs created by GITHUB_TOKEN do not trigger workflows. After merging, complete CI (build, security and every installation test) runs on main again; only its exact artifacts may be published.
BODY
  gh pr create --base main --head "$branch" --title "release: v${version} [skip release]" --body-file "$body"
fi
# Only the newest tested main commit should be released; close older bump PRs.
gh pr list -R "$repo" --base main --state open --json number,headRefName,isCrossRepository \
  --jq '.[] | select(.isCrossRepository | not) | select(.headRefName | startswith("codex/auto-release-")) | "\(.number) \(.headRefName)"' |
  while read -r stale stale_branch; do
    test "$stale_branch" != "$branch" || continue
    gh pr close "$stale" -R "$repo" --delete-branch --comment "Superseded by \`$branch\` for the newer main commit ${TESTED_SHA:0:12}." ||
      printf 'Could not close superseded release PR #%s; continuing\n' "$stale" >&2
  done
dispatch ci.yml --ref "$branch"
