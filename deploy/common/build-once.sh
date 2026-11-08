#!/usr/bin/env bash
# Build once, deploy everywhere (PLAT-426): the local half, sourced by deploy.sh (needs REPO_ROOT, the repository root of this checkout).
# Talks to the build host over ssh (BUILD_HOST, BUILD_PORT, BUILD_USER, BUILD_SSH_KEY, BUILDS_DIR) to build, list, select and ship
# a release made by deploy/common/build-release.sh. Written for bash 3.2 (macOS): no associative arrays, no ${var@Q}.

BUILD_REPOS="mcp-agent-builder-go mcpagent multi-llm-provider-go"
BUILDS_DIR="${BUILDS_DIR:-/srv/_builds}"

build_ssh() {
  local key=()
  [[ -z "${BUILD_SSH_KEY:-}" ]] || key=(-i "$BUILD_SSH_KEY")
  ssh -p "${BUILD_PORT:?set BUILD_PORT in the private deployments/deploy.env}" -o BatchMode=yes -o ConnectTimeout=10 -o StrictHostKeyChecking=accept-new ${key[@]+"${key[@]}"} \
    "${BUILD_USER:-root}@${BUILD_HOST:?set BUILD_HOST in the private deployments/deploy.env}" "$@"
}

repo_dir() { # the local checkout of a repository: this one, or its sibling
  if [[ "$1" == mcp-agent-builder-go ]]; then echo "$REPO_ROOT"; else echo "$REPO_ROOT/../$1"; fi
}

# DEPLOY_BRANCH chooses what a deploy ships: main (default) or dev, the shared integration branch of this repository.
deploy_branch() {
  case "${DEPLOY_BRANCH:-main}" in
    main|dev) printf '%s\n' "${DEPLOY_BRANCH:-main}" ;;
    *) echo "DEPLOY_BRANCH must be main or dev (got '${DEPLOY_BRANCH}')" >&2; return 1 ;;
  esac
}

# branch_ref <dir>: origin/<DEPLOY_BRANCH> of that checkout. Only a sibling repository without that branch uses its origin/main;
# this repository must have the branch (fetched when missing), so a dev deploy never passes a main-only app revision as dev.
branch_ref() {
  local branch
  branch="$(deploy_branch)" || return 1
  if git -C "$1" rev-parse -q --verify "refs/remotes/origin/$branch^{commit}" >/dev/null 2>&1; then echo "origin/$branch"; return; fi
  if [[ "$1" -ef "$REPO_ROOT" ]]; then
    git -C "$1" fetch -q origin "$branch" >/dev/null 2>&1 \
      && git -C "$1" rev-parse -q --verify "refs/remotes/origin/$branch^{commit}" >/dev/null 2>&1 \
      && { echo "origin/$branch"; return; }
    echo "DEPLOY_BRANCH=$branch: this repository has no origin/$branch (cannot fetch it); refusing." >&2
    return 1
  fi
  echo origin/main
}

# commit_on_branch <dir> <sha>: the pushed deploy branch (branch_ref) holds this commit, or the commit is an older commit of it plus
# changes that are each already on it. The second case lets a release carry finished fixes without newer work that a server cannot
# take yet. Nothing that has not landed on that pushed branch passes, never an arbitrary local commit. The version file is left out
# of the comparison: every commit bumps it, so it always differs.
# Go test files are left out too (reviewer, 7 Nov): deploy builds do not compile tests, and a test file often cannot cherry-pick
# cleanly because the branch's earlier test edits sit in its context. All production code in a pinned build must still be on the branch.
patch_id() {
  git -C "$1" diff-tree -p "$2" -- . ':(exclude)frontend/src/version.ts' ':(exclude)*_test.go' | git patch-id --stable | cut -d' ' -f1
}
commit_on_branch() {
  local dir="$1" sha="$2" ref base commit landed id
  ref="$(branch_ref "$dir")" || return 1
  git -C "$dir" merge-base --is-ancestor "$sha" "$ref" 2>/dev/null && return 0
  base="$(git -C "$dir" merge-base "$sha" "$ref" 2>/dev/null)" || return 1
  [[ -z "$(git -C "$dir" rev-list --merges "$base..$sha")" ]] || return 1
  landed="$(for commit in $(git -C "$dir" rev-list --no-merges "$base..$ref"); do patch_id "$dir" "$commit"; done)"
  for commit in $(git -C "$dir" rev-list "$base..$sha"); do
    id="$(patch_id "$dir" "$commit")"
    # No ID: the commit changes only the version file or test files, so it carries no production code to compare.
    [[ -n "$id" ]] || { echo "pin check: ${commit:0:10} changes no production code (version or test files only); skipped" >&2; continue; }
    grep -qxF "$id" <<<"$landed" || return 1
  done
}

to_https_url() {
  local url="$1"
  url="${url/ssh:\/\/git@github.com\//https://github.com/}"
  url="${url/git@github.com:/https://github.com/}"
  url="${url%.git}"
  [[ "$url" =~ ^https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || { echo "Unsupported repository URL: $1" >&2; return 1; }
  printf '%s\n' "$url"
}

# Builds on the build host, printing the build's name on stdout (progress on stderr). Reuses the build of the same three revisions.
# gomod_pinned_sha <app sha> <module repo>: the full commit agent_go/go.mod at that app commit pins for the module
# (its pseudo-version suffix), so a release builds what the app was tested against, not whatever main moved on to.
gomod_pinned_sha() {
  local app="$1" repo="$2" module version short full
  case "$repo" in
    mcpagent) module="github.com/manishiitg/mcpagent" ;;
    multi-llm-provider-go) module="github.com/manishiitg/multi-llm-provider-go" ;;
    *) return 1 ;;
  esac
  git -C "$REPO_ROOT" fetch -q origin "$app" 2>/dev/null || true
  version="$(git -C "$REPO_ROOT" show "$app:agent_go/go.mod" 2>/dev/null | awk -v m="$module" '$1 == m {print $2; exit}')"
  [[ -n "$version" ]] || return 1
  git -C "$(repo_dir "$repo")" fetch -q --tags origin 2>/dev/null || true
  if [[ "$version" =~ -[0-9a-f]{12}$ ]]; then
    short="${version##*-}"   # pseudo-version: its commit
  else
    short="$version"          # a release tag such as v1.2.3
  fi
  full="$(git -C "$(repo_dir "$repo")" rev-parse -q --verify "$short^{commit}" 2>/dev/null)" || return 1
  printf '%s\n' "$full"
}

build_release_remote() {
  local branch repo url sha args=() out name app_sha="" source
  branch="$(deploy_branch)" || return 1
  for repo in $BUILD_REPOS; do
    url="$(to_https_url "$(git -C "$(repo_dir "$repo")" remote get-url origin)")" || return 1
    # DEPLOY_SHA_<REPO> (REPO upper-case, - as _) builds that commit instead of the branch head, so a release can leave out work still landing on the branch.
    # It must pass commit_on_branch for that repository.
    local pin_var="DEPLOY_SHA_$(printf '%s' "$repo" | tr 'a-z-' 'A-Z_')" pinned
    pinned="${!pin_var:-}"
    source=""
    if [[ -n "$pinned" ]]; then
      [[ "$pinned" =~ ^[0-9a-f]{40}$ ]] || { echo "$pin_var must be a full 40-hex commit" >&2; return 1; }
      commit_on_branch "$(repo_dir "$repo")" "$pinned" \
        || { echo "$pin_var ${pinned:0:10} is not an ancestor of $(branch_ref "$(repo_dir "$repo")") of $repo, and not an ancestor plus changes already on it; refusing." >&2; return 1; }
      sha="$pinned" source=" (pinned)"
    elif [[ "$repo" != mcp-agent-builder-go ]] && sha="$(gomod_pinned_sha "$app_sha" "$repo")"; then
      # The app's go.mod decides the dependency revisions; main's head of a dependency may be untested with this app.
      source=" (agent_go/go.mod)"
    elif [[ "$repo" == mcp-agent-builder-go ]]; then
      sha="$(git ls-remote "$url" "refs/heads/$branch" | awk '{print $1}')"
    else
      # Never fall back to a branch head: that is how an untested dependency reached servers.
      echo "agent_go/go.mod at ${app_sha:0:10} pins a $repo version this checkout cannot resolve; fetch $(repo_dir "$repo") or set $pin_var=<40-hex>." >&2
      return 1
    fi
    [[ "$sha" =~ ^[0-9a-f]{40}$ ]] || { echo "Cannot resolve $branch of $url" >&2; return 1; }
    [[ "$repo" != mcp-agent-builder-go ]] || app_sha="$sha"
    echo "  $repo $branch = ${sha:0:10}$source" >&2
    args+=(--source "$repo" "$url" "$sha")
  done
  [[ "${DEPLOY_FORCE_BUILD:-0}" != 1 ]] || args+=(--force)
  local quoted="" arg
  for arg in "${args[@]}"; do quoted+=" $(printf '%q' "$arg")"; done
  echo "==> Building the release once on ${BUILD_HOST:-the build host} (reused when these revisions were already built)" >&2
  build_ssh "install -d -m 0755 '$BUILDS_DIR' && cat > '$BUILDS_DIR/.build-release.sh' && chmod 0755 '$BUILDS_DIR/.build-release.sh'" < "$REPO_ROOT/deploy/common/build-release.sh"
  # CPU and memory limits keep the live products on this box responsive (override with BUILD_CPU_QUOTA / BUILD_MEMORY_MAX).
  out="$(build_ssh "systemd-run --quiet --wait --pipe --collect --setenv=HOME=/root --setenv=BUILDS_DIR='$BUILDS_DIR' -p CPUQuota=${BUILD_CPU_QUOTA:-800%} -p MemoryMax=${BUILD_MEMORY_MAX:-12G} -p Nice=10 bash '$BUILDS_DIR/.build-release.sh'$quoted" | tee /dev/stderr)"
  name="$(printf '%s\n' "$out" | sed -n 's/^BUILD_NAME=//p' | tail -n 1)"
  [[ -n "$name" ]] || { echo "The build did not report a build name" >&2; return 1; }
  printf '%s\n' "$name"
}

# Verifies HEAD on the build host (deploy/common/verify-change.sh): the same build, vet and Verify-* tests as GitHub, faster.
# The commit must be pushed for the host to fetch it, so it goes to a throwaway branch hetzner-verify/<name> (not verify/*, which
# would also start the GitHub workflow) that is deleted afterwards.
verify_change_remote() {
  local name="${1:-$(git -C "$REPO_ROOT" rev-parse --short HEAD)}" sha url branch rc
  sha="$(git -C "$REPO_ROOT" rev-parse HEAD)"
  branch="hetzner-verify/$name"
  url="$(to_https_url "$(git -C "$REPO_ROOT" remote get-url origin)")" || return 1
  git -C "$REPO_ROOT" push -q -f origin "HEAD:refs/heads/$branch" || { echo "Could not push $branch" >&2; return 1; }
  echo "==> Verifying ${sha:0:10} on ${BUILD_HOST:-the build host}" >&2
  # Replace the script with a new file (rename), never rewrite it in place: another verification may be running it.
  build_ssh "install -d -m 0755 '$BUILDS_DIR' && t=\$(mktemp '$BUILDS_DIR/.verify-change.XXXXXX') && cat > \"\$t\" && chmod 0755 \"\$t\" && mv -f \"\$t\" '$BUILDS_DIR/.verify-change.sh'" < "$REPO_ROOT/deploy/common/verify-change.sh" \
    || { git -C "$REPO_ROOT" push -q origin --delete "$branch" >/dev/null 2>&1; echo "Cannot reach the build host" >&2; return 1; }
  # Lower limits than a build so a verification never starves a deploy build that runs at the same time.
  rc=0
  build_ssh "systemd-run --quiet --wait --pipe --collect --setenv=HOME=/root --setenv=BUILDS_DIR='$BUILDS_DIR' -p CPUQuota=${VERIFY_CPU_QUOTA:-400%} -p MemoryMax=${VERIFY_MEMORY_MAX:-8G} -p Nice=15 bash '$BUILDS_DIR/.verify-change.sh' --url '$url' --sha '$sha' --builds-dir '$BUILDS_DIR'" || rc=$?
  git -C "$REPO_ROOT" push -q origin --delete "$branch" >/dev/null 2>&1 || true
  return $rc
}

# Removes old builds on the build host: all but the newest BUILD_KEEP (default 1), never a pinned one or one younger than 15 minutes. Run after every
# successful deploy; a pruning problem never fails a deploy.
prune_builds_remote() {
  build_ssh "install -d -m 0755 '$BUILDS_DIR' && cat > '$BUILDS_DIR/.build-release.sh' && chmod 0755 '$BUILDS_DIR/.build-release.sh'" < "$REPO_ROOT/deploy/common/build-release.sh" \
    || { echo "Build pruning skipped (cannot reach the build host)" >&2; return 0; }
  build_ssh "BUILDS_DIR='$BUILDS_DIR' '$BUILDS_DIR/.build-release.sh' --prune-only --builds-dir '$BUILDS_DIR' --keep '${BUILD_KEEP:-1}'" >&2 \
    || echo "Build pruning failed (the deploy itself succeeded)" >&2
}

# find_build <name|sha prefix>: prints "<name> <sha1> <sha2> <sha3>" for the one matching build, or fails.
find_build() {
  [[ "$1" =~ ^[0-9A-Za-z._-]+$ ]] || { echo "Invalid build selector: $1" >&2; return 1; }
  build_ssh "python3 - find '$BUILDS_DIR' '$1'" < "$REPO_ROOT/deploy/common/release_manifest.py"
}

# A chosen (older) build may be deployed only if each of its revisions passes commit_on_branch.
check_build_on_branch() { # name sha1 sha2 sha3
  local name="$1" repo sha dir branch; shift
  branch="$(deploy_branch)" || return 1
  for repo in $BUILD_REPOS; do
    sha="$1"; shift
    dir="$(repo_dir "$repo")"
    git -C "$dir" fetch -q origin main >/dev/null 2>&1 || { echo "Cannot fetch $repo to check ancestry" >&2; return 1; }
    if [[ "$branch" != main ]]; then
      if [[ "$repo" == mcp-agent-builder-go ]]; then
        git -C "$dir" fetch -q origin "$branch" >/dev/null 2>&1 || { echo "Cannot fetch origin/$branch of $repo to check ancestry; refusing." >&2; return 1; }
      else
        git -C "$dir" fetch -q origin "$branch" >/dev/null 2>&1 || true  # a sibling may have no such branch
      fi
    fi
    commit_on_branch "$dir" "$sha" \
      || { echo "Build $name holds $repo ${sha:0:10}, which is not an ancestor of $(branch_ref "$dir"), nor one plus changes already on it; refusing." >&2; return 1; }
  done
}

# Prints the build name to deploy: the pinned one (all-hetzner), the chosen one (--build), or a build of the current branch head.
ensure_prebuilt_build() {
  local found
  if [[ -n "${DEPLOY_PREBUILT_NAME:-}" ]]; then printf '%s\n' "$DEPLOY_PREBUILT_NAME"; return; fi
  if [[ -n "${DEPLOY_BUILD:-}" ]]; then
    found="$(find_build "$DEPLOY_BUILD")" || { echo "No unique build matches '$DEPLOY_BUILD' (see ./deploy.sh builds)" >&2; return 1; }
    echo "==> Using existing build: $found" >&2
    # shellcheck disable=SC2086
    check_build_on_branch $found || return 1
    printf '%s\n' "${found%% *}"
    return
  fi
  build_release_remote
}

# Streams a trimmed copy of a build to server A (no mcpagent/provider source, no downloads/: that host never used them).
ship_build_to_rts() { # name remote_job_dir
  local name="$1" job="$2"
  build_ssh "tar -C '$BUILDS_DIR' --exclude='$name/source/mcpagent' --exclude='$name/source/multi-llm-provider-go' --exclude='$name/downloads' -cf - '$name' | gzip -3" \
    | "${SSH[@]}" "cd '$job' && tar -xzf - && mv '$name' build"
}


# Publishes a build to the public builds repository from the build host (deploy/common/publish-build.sh: idempotent; repairs a
# half upload; prunes old releases). Prints the release tag on stdout when the build is on GitHub, nothing when it is not (for
# example no upload token on the build host yet, which is only a message, never a failure). Progress goes to stderr.
publish_build_remote() { # name
  local name="$1" out
  build_ssh "install -d -m 0755 '$BUILDS_DIR' && cat > '$BUILDS_DIR/.publish-build.sh' && chmod 0755 '$BUILDS_DIR/.publish-build.sh'" < "$REPO_ROOT/deploy/common/publish-build.sh" || return 1
  out="$(build_ssh "BUILDS_DIR='$BUILDS_DIR' bash '$BUILDS_DIR/.publish-build.sh' '$BUILDS_DIR/$name'" | tee /dev/stderr)" || return 1
  printf '%s\n' "$out" | sed -n 's/^RELEASE_TAG=//p' | tail -n 1
}

# Gets the trimmed build onto the server A job folder as <job>/build. DEPLOY_BUILD_TRANSPORT: auto (default: Server A downloads the build from
# GitHub, and the old stream through this machine is the fallback when the release is missing or the download fails), github
# (never fall back), stream (the old path only). Needs SSH (the ssh command line to server A) like ship_build_to_rts.
deliver_build_to_rts() { # name remote_job_dir manifest_sha256
  local name="$1" job="$2" hash="$3" mode="${DEPLOY_BUILD_TRANSPORT:-auto}" tag="" why=""
  case "$mode" in auto|github|stream) ;; *) echo "DEPLOY_BUILD_TRANSPORT must be auto, github or stream (got '$mode')" >&2; return 1 ;; esac
  if [[ "$mode" != stream ]]; then
    tag="$(publish_build_remote "$name")" || tag=""
    if [[ -z "$tag" ]]; then
      why="the build is not on GitHub (no upload token on the build host yet, or the upload failed)"
    else
      echo "==> RTS downloads build $name itself from GitHub ($tag)" >&2
      if "${SSH[@]}" "cat > '$job/fetch-build.sh'" < "$REPO_ROOT/deploy/common/fetch-build.sh" \
         && "${SSH[@]}" "bash '$job/fetch-build.sh' '$tag' build-rts.tar.gz '$hash' '$job/build'"; then
        return 0
      fi
      why="RTS could not download and verify $tag"
    fi
    [[ "$mode" != github ]] || { echo "DEPLOY_BUILD_TRANSPORT=github: $why" >&2; return 1; }
    echo "==> Falling back to streaming the build through this machine: $why" >&2
  fi
  ship_build_to_rts "$name" "$job"
}

# Gets a build onto a product host that cannot read the build host's folder (PREBUILT_DELIVERY=fetch) as
# dest (<app>/prebuilt/<name>). The host downloads the published release from GitHub and checks the manifest hash read
# from the build host; without a release (or when that fails) the build is streamed through this machine. Older copies
# in the same folder are removed first: each release copies what it needs. Needs SSH (the ssh command line to the host).
deliver_build_to_product_host() { # name dest
  local name="$1" dest="$2" parent hash tag=""
  parent="$(dirname "$dest")"
  "${SSH[@]}" "install -d -m 0700 '$parent' && find '$parent' -mindepth 1 -maxdepth 1 ! -name '$name' -exec rm -rf {} +"
  if "${SSH[@]}" "test -f '$dest/manifest.json'"; then
    echo "==> Build $name is already on the host" >&2
    return 0
  fi
  hash="$(build_ssh "sha256sum '$BUILDS_DIR/$name/manifest.json'" | awk '{print $1}')"
  [[ "$hash" =~ ^[0-9a-f]{64}$ ]] || { echo "Cannot read the manifest hash of $name" >&2; return 1; }
  tag="$(publish_build_remote "$name")" || tag=""
  if [[ -n "$tag" ]]; then
    echo "==> The host downloads build $name from GitHub ($tag)" >&2
    if "${SSH[@]}" "cat > '$parent/.fetch-build.sh'" < "$REPO_ROOT/deploy/common/fetch-build.sh" \
       && "${SSH[@]}" "bash '$parent/.fetch-build.sh' '$tag' build.tar.gz '$hash' '$dest'; rc=\$?; rm -f '$parent/.fetch-build.sh'; exit \$rc"; then
      return 0
    fi
  fi
  echo "==> Streaming build $name to the host through this machine" >&2
  build_ssh "tar -C '$BUILDS_DIR' -cf - '$name' | gzip -3" \
    | "${SSH[@]}" "set -e; t=\$(mktemp -d '$parent/.in.XXXXXX'); trap 'rm -rf \"\$t\"' EXIT; tar -xzf - -C \"\$t\"
      test \"\$(sha256sum \"\$t/$name/manifest.json\" | cut -d' ' -f1)\" = '$hash' || { echo 'streamed build: manifest hash mismatch' >&2; exit 1; }
      mv \"\$t/$name\" '$dest'"
}

# ---- One deploy per server at a time, visible to everyone who deploys -------------------------------------------------------------
# A deploy holds <builds-dir>/.deploys/<server> on the build host (every deploy already reaches it), with who holds it and since
# when. A second deploy to the same server waits and says whose deploy it is waiting for; `./deploy.sh queue` lists the holders.
# A holder older than DEPLOY_LOCK_STALE_MINUTES (default 40) is treated as abandoned. Agents stop coordinating deploys by message.
deploy_lock_who() {
  local rev
  rev="$(git -C "$REPO_ROOT" rev-parse --short=8 HEAD 2>/dev/null || echo unknown)"
  printf '%s@%s (%s) %s' "$(id -un)" "$(hostname -s)" "${DEPLOY_WHO:-$(basename "$REPO_ROOT")}" "$rev"
}

deploy_lock_take() { # server
  local server="$1" who out last=""
  who="$(deploy_lock_who)"
  while :; do
    out="$(build_ssh "d='$BUILDS_DIR/.deploys'; install -d -m 0777 \"\$d\" 2>/dev/null || mkdir -p \"\$d\"
      l=\"\$d/$server\"
      if [ -d \"\$l\" ] && [ \$(( \$(date +%s) - \$(stat -c %Y \"\$l\") )) -gt $(( ${DEPLOY_LOCK_STALE_MINUTES:-40} * 60 )) ]; then
        echo \"abandoned: \$(cat \"\$l/holder\" 2>/dev/null)\" >&2; rm -rf \"\$l\"
      fi
      if mkdir \"\$l\" 2>/dev/null; then printf '%s\n' '$who' > \"\$l/holder\"; echo TAKEN
      else echo \"HELD \$(cat \"\$l/holder\" 2>/dev/null) since \$(date -u -d @\$(stat -c %Y \"\$l\") +%H:%MZ) (\$(( (\$(date +%s) - \$(stat -c %Y \"\$l\")) / 60 )) min)\"; fi")" || return 1
    [[ "$out" == TAKEN ]] && { echo "==> Deploying $server (others wait until this deploy is live)" >&2; return 0; }
    if [[ "${out%% since*}" != "$last" ]]; then
      echo "==> Waiting: $server is being deployed by ${out#HELD }" >&2
      last="${out%% since*}"
    fi
    sleep 15
  done
}

deploy_lock_release() { # server
  build_ssh "rm -rf '$BUILDS_DIR/.deploys/$1'" >/dev/null 2>&1 || true
}

deploy_lock_list() {
  build_ssh "cd '$BUILDS_DIR/.deploys' 2>/dev/null && for l in *; do [ -d \"\$l\" ] && echo \"\$l: \$(cat \"\$l/holder\" 2>/dev/null) since \$(date -u -d @\$(stat -c %Y \"\$l\") +%H:%MZ)\"; done" | grep . || echo "No deploy is running."
}
