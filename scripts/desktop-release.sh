#!/usr/bin/env bash
set -euo pipefail

REPO="manishiitg/agentworks"
GH_USER="manishiitg"
MAIN_BRANCH="main"
WORKFLOW_NAME="Desktop DMG"

usage() {
  cat <<'EOF'
Usage:
  scripts/desktop-release.sh [--dry-run] v1.25.81

What it does:
  - uses the stored manishiitg token without switching the active gh account
  - prepares from canonical origin/main in an isolated temporary worktree
  - verifies the version is newer than the current Latest release
  - commits the matching desktop package/package-lock version
  - generates release notes from commits since the previous published release
  - pushes main and the version tag
  - waits for the Desktop DMG workflow
  - publishes the workflow-created draft release with the generated notes
EOF
}

die() {
  echo "error: $*" >&2
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"
}

dry_run=false
if [[ "${1:-}" == "--dry-run" ]]; then
  dry_run=true
  shift
fi

version_arg="${1:-}"
if [[ -z "$version_arg" || "$version_arg" == "-h" || "$version_arg" == "--help" ]]; then
  usage
  exit 0
fi
[[ $# -eq 1 ]] || die "expected exactly one version argument"

if [[ ! "$version_arg" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  die "version must look like v1.25.81"
fi

require_cmd git
require_cmd gh
require_cmd node
require_cmd npm

semver_gt() {
  local candidate="${1#v}"
  local baseline="${2#v}"
  local candidate_major candidate_minor candidate_patch
  local baseline_major baseline_minor baseline_patch
  IFS=. read -r candidate_major candidate_minor candidate_patch <<<"$candidate"
  IFS=. read -r baseline_major baseline_minor baseline_patch <<<"$baseline"
  (( candidate_major > baseline_major )) ||
    (( candidate_major == baseline_major && candidate_minor > baseline_minor )) ||
    (( candidate_major == baseline_major && candidate_minor == baseline_minor && candidate_patch > baseline_patch ))
}

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

release_tmp_root=""
release_worktree=""
notes_file=""
created_tag=""
push_succeeded=false

cleanup() {
  local status=$?
  trap - EXIT
  set +e
  cd "$repo_root" 2>/dev/null
  if [[ -n "$created_tag" && "$push_succeeded" != "true" ]]; then
    git tag -d "$created_tag" >/dev/null 2>&1
  fi
  if [[ -n "$release_worktree" ]]; then
    git worktree remove --force "$release_worktree" >/dev/null 2>&1
  fi
  [[ -z "$release_tmp_root" ]] || rm -rf "$release_tmp_root"
  [[ -z "$notes_file" ]] || rm -f "$notes_file"
  exit "$status"
}
trap cleanup EXIT

tag="$version_arg"
version="${tag#v}"

echo "==> Using GitHub credentials for: $GH_USER"
github_token="$(gh auth token -h github.com -u "$GH_USER")" || die "no stored GitHub token for $GH_USER"
export GH_TOKEN="$github_token"
active_user="$(gh api user --jq .login)"
[[ "$active_user" == "$GH_USER" ]] || die "active gh user is $active_user, expected $GH_USER"

auth_headers="$(gh api -i user 2>/dev/null || true)"
if ! grep -Eiq '^X-Oauth-Scopes:.*workflow' <<<"$auth_headers"; then
  die "the stored $GH_USER token needs workflow scope; refresh it with: gh auth switch -h github.com -u $GH_USER && gh auth refresh -h github.com -s workflow"
fi

echo "==> Checking main branch"
if [[ -n "$(git status --porcelain)" ]]; then
  die "working tree is dirty; commit or stash changes before release"
fi

origin_url="$(git remote get-url origin)"
case "$origin_url" in
  git@github.com:manishiitg/agentworks.git | \
    https://github.com/manishiitg/agentworks.git | \
    https://github.com/manishiitg/agentworks | \
    ssh://git@github.com/manishiitg/agentworks.git | \
  git@github.com:manishiitg/coding-agent-loop.git | \
    https://github.com/manishiitg/coding-agent-loop.git | \
    https://github.com/manishiitg/coding-agent-loop | \
    ssh://git@github.com/manishiitg/coding-agent-loop.git)
    ;;
  *)
    die "origin points to $origin_url, expected the canonical $REPO repository"
    ;;
esac

git fetch origin "$MAIN_BRANCH" --tags
remote_head="$(git rev-parse "origin/$MAIN_BRANCH")"
echo "==> Preparing exact origin/main revision $remote_head"
release_tmp_root="$(mktemp -d)"
release_worktree="$release_tmp_root/repo"
git worktree add --detach --quiet "$release_worktree" "$remote_head"
cd "$release_worktree"

if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
  die "local tag already exists: $tag"
fi
if git ls-remote --exit-code --tags origin "$tag" >/dev/null 2>&1; then
  die "remote tag already exists: $tag"
fi
if gh release view "$tag" --repo "$REPO" >/dev/null 2>&1; then
  die "GitHub release already exists: $tag"
fi

# The repository's "Latest" release may be SparkQuill's (sparkquill-v*), so
# the previous AgentWorks release is the highest published plain vX.Y.Z tag,
# the same filter the desktop updater uses.
previous_tag=""
while IFS= read -r candidate; do
  [[ "$candidate" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || continue
  if [[ -z "$previous_tag" ]] || semver_gt "$candidate" "$previous_tag"; then
    previous_tag="$candidate"
  fi
done < <(gh release list --repo "$REPO" --limit 200 --json tagName,isDraft --jq '.[] | select(.isDraft | not) | .tagName' 2>/dev/null)
if [[ -n "$previous_tag" ]]; then
  echo "==> Previous AgentWorks release: $previous_tag"
  semver_gt "$tag" "$previous_tag" || die "$tag must be greater than the previous AgentWorks release $previous_tag"
else
  echo "==> No published AgentWorks release exists; validating this as the first release"
fi

echo "==> Building changelog"
notes_file="$(mktemp)"
{
  echo "Desktop release $tag."
  echo
  # Install through curl only: the app is not notarized, and macOS
  # quarantines a browser download and then refuses to open it.
  cat <<INSTALL
## Install (macOS, Apple Silicon)

Install from Terminal with \`curl\`. Do not download the DMG in a browser: AgentWorks is not notarized by Apple, so macOS blocks a browser-downloaded copy ("is damaged and can't be opened"). A curl download is not quarantined.

\`\`\`bash
curl -fL -o /tmp/AgentWorks.dmg https://github.com/$REPO/releases/download/$tag/AgentWorks-$version-arm64.dmg
hdiutil attach -nobrowse -quiet -mountpoint /tmp/agentworks-dmg /tmp/AgentWorks.dmg
rm -rf /Applications/AgentWorks.app && cp -R /tmp/agentworks-dmg/AgentWorks.app /Applications/
hdiutil detach -quiet /tmp/agentworks-dmg && rm /tmp/AgentWorks.dmg
open /Applications/AgentWorks.app
\`\`\`

Already installed? The app updates itself from this release.

INSTALL
  if [[ -n "$previous_tag" ]]; then
    echo "Changes since $previous_tag:"
    # GitHub caps a release body at 125,000 characters; list the newest
    # commits and link the full comparison.
    commit_count="$(git rev-list --no-merges --count "$previous_tag"..HEAD 2>/dev/null || echo 0)"
    if ! git log --no-merges --pretty=format:'- %s (%h)' -n 100 "$previous_tag"..HEAD; then
      echo "- Unable to compute changelog from $previous_tag."
    fi
    if [[ "$commit_count" == "0" ]]; then
      echo "- No non-merge commits."
    elif (( commit_count > 100 )); then
      echo
      echo "…and $((commit_count - 100)) more. Full list: https://github.com/$REPO/compare/$previous_tag...$tag"
    fi
  else
    echo "Changes:"
    git log --no-merges --pretty=format:'- %s (%h)' -20
  fi
  echo
} >"$notes_file"

cat "$notes_file"

package_version="$(node -p "require('./desktop/package.json').version")"
lock_version="$(node -p "require('./desktop/package-lock.json').version")"
echo "==> Desktop metadata: package=$package_version lock=$lock_version target=$version"

for metadata_version in "$package_version" "$lock_version"; do
  if [[ ! "$metadata_version" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
    die "desktop version metadata is not plain semver: $metadata_version"
  fi
done
if semver_gt "$package_version" "$version" || semver_gt "$lock_version" "$version"; then
  die "$tag would downgrade desktop version metadata (package=$package_version lock=$lock_version)"
fi

if $dry_run; then
  if [[ "$package_version" != "$version" || "$lock_version" != "$version" ]]; then
    echo "==> Dry run: would update and commit desktop version metadata to $version"
  fi
  echo "==> Dry run complete; no commit, push, tag, workflow, or release was created"
  exit 0
fi

if [[ "$package_version" != "$version" || "$lock_version" != "$version" ]]; then
  echo "==> Updating desktop version metadata to $version"
  (
    cd desktop
    npm version "$version" --no-git-tag-version --allow-same-version
  )
  updated_package_version="$(node -p "require('./desktop/package.json').version")"
  updated_lock_version="$(node -p "require('./desktop/package-lock.json').version")"
  [[ "$updated_package_version" == "$version" && "$updated_lock_version" == "$version" ]] ||
    die "npm did not update both desktop version files to $version"
  git add desktop/package.json desktop/package-lock.json
  staged_files="$(git diff --cached --name-only | sort)"
  expected_staged_files=$'desktop/package-lock.json\ndesktop/package.json'
  [[ "$staged_files" == "$expected_staged_files" ]] ||
    die "version bump staged unexpected files: $staged_files"
  # The isolated release worktree intentionally has no sibling Go dependency
  # checkouts or node_modules. The developer pre-commit hook requires both, so
  # it cannot run there. This commit is restricted to the two validated version
  # files above; the tag workflow performs the complete clean build and tests.
  git -c core.hooksPath=/dev/null commit -m "Bump desktop version to $version"
fi

[[ -z "$(git status --porcelain)" ]] || die "working tree became dirty while preparing release"

echo "==> Creating tag $tag"
git tag -a "$tag" -m "Release $tag"
created_tag="$tag"
echo "==> Atomically pushing main and $tag"
git push --atomic origin "HEAD:refs/heads/$MAIN_BRANCH" "refs/tags/$tag"
push_succeeded=true

head_sha="$(git rev-parse HEAD)"
run_id=""
echo "==> Waiting for $WORKFLOW_NAME run for $tag"
for _ in $(seq 1 60); do
  run_id="$(
    gh run list \
      --repo "$REPO" \
      --workflow "$WORKFLOW_NAME" \
      --limit 20 \
      --json databaseId,headBranch,headSha,event \
      --jq ".[] | select(.headBranch == \"$tag\" and .headSha == \"$head_sha\" and .event == \"push\") | .databaseId" |
      head -n 1
  )"
  [[ -n "$run_id" ]] && break
  sleep 5
done

[[ -n "$run_id" ]] || die "could not find release workflow run for $tag"

gh run watch "$run_id" --repo "$REPO" --exit-status

echo "==> Publishing release notes"
release_rows="$(
  gh api "repos/$REPO/releases?per_page=100" \
    --jq ".[] | select(.tag_name == \"$tag\") | [.id, .draft] | @tsv"
)"

published_id="$(awk '$2 == "false" { print $1; exit }' <<<"$release_rows")"
draft_id="$(awk '$2 == "true" { print $1; exit }' <<<"$release_rows")"

if [[ -n "$published_id" ]]; then
  gh release edit "$tag" --repo "$REPO" --title "$version" --notes-file "$notes_file" --latest
elif [[ -n "$draft_id" ]]; then
  gh api -X PATCH "repos/$REPO/releases/$draft_id" \
    -f name="$version" \
    -f body="$(cat "$notes_file")" \
    -F draft=false \
    -F prerelease=false \
    -f make_latest=true >/dev/null
else
  die "workflow completed, but no GitHub release/draft was found for $tag"
fi

echo "==> Removing duplicate drafts for $tag"
gh api "repos/$REPO/releases?per_page=100" \
  --jq ".[] | select(.tag_name == \"$tag\" and .draft == true) | .id" |
  while read -r duplicate_draft_id; do
    [[ -n "$duplicate_draft_id" ]] || continue
    gh api -X DELETE "repos/$REPO/releases/$duplicate_draft_id" >/dev/null
  done

echo "==> Verifying assets"
expected_assets=(
  "latest-mac.yml"
  "AgentWorks-$version-arm64-mac.zip"
  "AgentWorks-$version-arm64-mac.zip.blockmap"
  "AgentWorks-$version-arm64.dmg"
  "AgentWorks-$version-arm64.dmg.blockmap"
  "Runloop-$version-arm64.dmg"
)

release_assets="$(gh release view "$tag" --repo "$REPO" --json assets --jq '.assets[].name')"
for asset in "${expected_assets[@]}"; do
  grep -Fxq "$asset" <<<"$release_assets" || die "missing release asset: $asset"
done

release_url="$(gh release view "$tag" --repo "$REPO" --json url --jq .url)"
echo "==> Published: $release_url"
