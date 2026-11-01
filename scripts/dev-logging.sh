#!/usr/bin/env bash
# Local setup diagnostics; service logs and process ownership stay with the runner.

dev_log_init() {
    [ -n "${DEV_STARTUP_LOG:-}" ] && return 0
    local log_dir="${AGENTWORKS_LOG_DIR:-logs}"
    case "$log_dir" in /*) ;; *) log_dir="${SCRIPT_DIR}/$log_dir" ;; esac
    mkdir -p "$log_dir" || return 1
    DEV_STARTUP_LOG="$(umask 077; mktemp "$log_dir/startup-$(date +%Y%m%d-%H%M%S).XXXXXX")" || return 1
}

dev_log_location() {
    dev_log_init || return 1
    printf 'Setup details: %s (use --verbose to show them here)\n' "$DEV_STARTUP_LOG"
}

dev_detail() {
    dev_log_init || return 1
    printf '%s\n' "$*" >> "$DEV_STARTUP_LOG"
    if [ "${AGENTWORKS_DEV_VERBOSE:-0}" = 1 ]; then printf '%s\n' "$*"; fi
    return 0
}

dev_setup() {
    if [ "${AGENTWORKS_DEV_VERBOSE:-0}" = 1 ]; then "$@"; return $?; fi
    dev_log_init || return 1
    # Only routine stdout is quiet. Compiler/npm warnings and errors on stderr
    # remain live, and a failed command also exposes its final stdout details.
    local status
    if "$@" >> "$DEV_STARTUP_LOG"; then return 0; else status=$?; fi
    printf 'Setup failed (exit %s). Details: %s\n' "$status" "$DEV_STARTUP_LOG" >&2
    tail -n 20 "$DEV_STARTUP_LOG" >&2
    return "$status"
}
