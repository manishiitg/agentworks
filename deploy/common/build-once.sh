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

to_https_url() {
  local url="$1"
  url="${url/ssh:\/\/git@github.com\//https://github.com/}"
  url="${url/git@github.com:/https://github.com/}"
  url="${url%.git}"
  [[ "$url" =~ ^https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || { echo "Unsupported repository URL: $1" >&2; return 1; }
  printf '%s\n' "$url"
}

# Builds on the build host, printing the build's name on stdout (progress on stderr). Reuses the build of the same three revisions.
build_release_remote() {
  local branch="${DEPLOY_BRANCH:-main}" repo url sha args=() out name
  for repo in $BUILD_REPOS; do
    url="$(to_https_url "$(git -C "$(repo_dir "$repo")" remote get-url origin)")" || return 1
    sha="$(git ls-remote "$url" "refs/heads/$branch" | awk '{print $1}')"
    # DEPLOY_SHA_<REPO> (REPO upper-case, - as _) builds that commit instead of the branch head, so a release can leave out work still landing on main.
    # It must already be on main of that repository.
    local pin_var="DEPLOY_SHA_$(printf '%s' "$repo" | tr 'a-z-' 'A-Z_')" pinned
    pinned="${!pin_var:-}"
    if [[ -n "$pinned" ]]; then
      [[ "$pinned" =~ ^[0-9a-f]{40}$ ]] || { echo "$pin_var must be a full 40-hex commit" >&2; return 1; }
      git -C "$(repo_dir "$repo")" merge-base --is-ancestor "$pinned" origin/main 2>/dev/null \
        || { echo "$pin_var ${pinned:0:10} is not an ancestor of origin/main of $repo; refusing." >&2; return 1; }
      sha="$pinned"
    fi
    [[ "$sha" =~ ^[0-9a-f]{40}$ ]] || { echo "Cannot resolve $branch of $url" >&2; return 1; }
    echo "  $repo $branch = ${sha:0:10}${pinned:+ (pinned)}" >&2
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

# A chosen (older) build may be deployed only if each of its revisions is an ancestor of origin/main of its repository.
check_build_on_main() { # name sha1 sha2 sha3
  local name="$1" repo sha dir; shift
  for repo in $BUILD_REPOS; do
    sha="$1"; shift
    dir="$(repo_dir "$repo")"
    git -C "$dir" fetch -q origin main >/dev/null 2>&1 || { echo "Cannot fetch $repo to check ancestry" >&2; return 1; }
    git -C "$dir" merge-base --is-ancestor "$sha" origin/main 2>/dev/null \
      || { echo "Build $name holds $repo ${sha:0:10}, which is not an ancestor of origin/main; refusing." >&2; return 1; }
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
    check_build_on_main $found || return 1
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
