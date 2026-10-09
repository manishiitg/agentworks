#!/usr/bin/env bash
# Per-user Linux accounts ("slots"): the build and install steps shared by every deployment's
# build-and-activate.sh (rootless-linux products and the AWS EC2 / RTS host). Source this file.
#
# What a deployment gets: the two small programs (slotctl, slottmux) built into every release, and, on a
# host an administrator has provisioned (deploy/common/provision-slots.sh creates /etc/agentworks/slots.json),
# the tmux front-end installed outside the releases and the service switched to run shell commands as the
# user's own account. A host without the table is untouched.

# slots_build WORKSPACE_ROOT GOWORK BUILDER_REPO_ROOT BUILD_DIR
slots_build() {
  local workspace_root="$1" gowork="$2" repo_root="$3" build_dir="$4" program
  # slotctl is the one program the service account may run as a slot; a root-run provision-slots.sh
  # installs it root-owned. slottmux is the tmux front-end. slotcheck is the deploy self-test (slotcheck.sh).
  for program in slotctl slottmux slotcheck; do
    (cd "$workspace_root" && GOWORK="$gowork" GOOS=linux GOARCH="${BUILD_ARCH:-amd64}" CGO_ENABLED=0 go build -o "$build_dir/bin/$program" "$repo_root/workspace/cmd/$program")
  done
}

# slots_enabled: this host uses slots (the product asked for them, or an administrator provisioned them).
slots_enabled() {
  [[ "${SLOTS_ENABLED:-false}" == "true" ]] || [[ -r /etc/agentworks/slots.json ]]
}

# slots_install_shim APP_DIR BUILD_DIR: put the tmux front-end outside the releases (its path never
# changes) so a service PATH that lists APP_DIR/slots/bin first finds it. Prints the directory to put
# first in PATH, or nothing when this host does not use slots.
slots_install_shim() {
  local app_dir="$1" build_dir="$2"
  slots_enabled || return 0
  install -d -m 0755 "$app_dir/slots/bin"
  cp "$build_dir/bin/slottmux" "$app_dir/slots/bin/.tmux.new" && chmod 0755 "$app_dir/slots/bin/.tmux.new" \
    && mv -f "$app_dir/slots/bin/.tmux.new" "$app_dir/slots/bin/tmux"
  printf '%s\n' "$app_dir/slots/bin"
}

# slots_release_traversal APP_DIR BUILD_DIR: a slot account must reach the Landlock launcher in the release
# (BUILD_DIR/bin/video-studio-landlock-runner, which slotctl starts as the slot). RTS had releases/ at 0700, so every
# slotted command failed with "fork/exec ...: permission denied" (PLAT-478). Search-only (x), never read: releases/ is
# 0711 (traversable, not listable), the release folder and its bin/ get o+x, the launcher is 0755. Idempotent; only
# folders this account owns are touched (the folders above APP_DIR are provision-slots.sh's, run as root).
slots_release_traversal() {
  local app_dir="$1" build_dir="$2" dir
  slots_enabled || return 0
  [[ -d "$app_dir/releases" ]] || return 0
  chmod 0711 "$app_dir/releases"
  for dir in "$app_dir" "$build_dir" "$build_dir/bin"; do
    [[ -d "$dir" && -O "$dir" ]] || continue
    chmod o+x "$dir"
  done
  [[ -f "$build_dir/bin/video-studio-landlock-runner" ]] && chmod 0755 "$build_dir/bin/video-studio-landlock-runner"
  return 0
}

# slots_selfcheck APP_DIR DOCS PRODUCT BUILD_DIR: the read-only deploy self-test (deploy/common/slotcheck.sh, copied
# into every release). Returns non-zero when a slot check fails; the caller fails the deploy loudly after activation.
slots_selfcheck() {
  local app_dir="$1" docs="$2" product="$3" build_dir="$4"
  bash "$build_dir/slotcheck.sh" --app "$app_dir" --docs "$docs" --product "$product" --release "$build_dir"
}
