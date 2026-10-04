#!/usr/bin/env bash
# Shared by both activation scripts when they use a prebuilt release (PLAT-426, deploy/common/build-release.sh). Source this file.

# The activation runs python from the build's read-only-by-intent source tree (validators, catalog builder): never let it leave
# __pycache__ there, or the next verification of that build would refuse an unlisted file (seen when run as root).
export PYTHONDONTWRITEBYTECODE=1

# prebuilt_verify REPO_ROOT BUILD_DIR [release_manifest.py verify options...]: refuse a build that does not match this host
# (CPU architecture, glibc) or whose files differ from manifest.json, or whose agent cannot find a shared library here.
prebuilt_verify() {
  local repo_root="$1" build_dir="$2" missing
  shift 2
  test -d "$build_dir" || { echo "Prebuilt release $build_dir does not exist" >&2; return 1; }
  python3 "$repo_root/deploy/common/release_manifest.py" verify "$build_dir" "$@" || { echo "Refusing prebuilt release $build_dir" >&2; return 1; }
  missing="$(LD_LIBRARY_PATH="$build_dir/bin/lib" ldd "$build_dir/bin/agent" 2>&1 | grep -E 'not found|not a dynamic' || true)"
  [[ -z "$missing" ]] || { echo "Refusing prebuilt release: the agent cannot load on this host: $missing" >&2; return 1; }
}

# prebuilt_revisions BUILD_DIR NAME: the commit a build holds for one repository, from its SOURCE_REVISIONS.
prebuilt_revision() {
  sed -n "s/^$2=//p" "$1/SOURCE_REVISIONS" | head -n 1
}

# prebuilt_copy_bin BUILD_DIR DEST_BIN PREFIX "<names to rename to PREFIX-name>" "<names to leave out>": copy bin/* of a build into a
# release's bin/. The build keeps product-neutral names (agent, workspace, gateway, browser); each target names its own. Plain
# cp -R (new files get today's mtime, like a fresh build): the carried-over frontend asset cleanup deletes by age.
prebuilt_copy_bin() {
  local build_dir="$1" dest="$2" prefix="$3" rename=" $4 " skip=" $5 " built name
  mkdir -p "$dest"
  for built in "$build_dir"/bin/*; do
    name="$(basename "$built")"
    if [[ "$skip" == *" $name "* ]]; then continue; fi
    if [[ "$rename" == *" $name "* ]]; then cp -R "$built" "$dest/$prefix-$name"; else cp -R "$built" "$dest/$name"; fi
  done
}
