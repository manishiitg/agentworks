#!/usr/bin/env bash
# PLAT-856-439: restart the owning launcher, never infer ownership from a port.

dev_process_start() {
    ps -p "$1" -o lstart= 2>/dev/null
}

dev_process_matches() {
    case "$1" in ''|*[!0-9]*) return 1 ;; esac
    [ -n "$2" ] && [ "$(dev_process_start "$1")" = "$2" ] || return 1
    case "$(ps -p "$1" -o stat= 2>/dev/null)" in *Z*) return 1 ;; esac
}

dev_instance_release() {
    [ -n "${DEV_INSTANCE_FILE:-}" ] || return 0
    [ "$(head -1 "$DEV_INSTANCE_FILE" 2>/dev/null)" = "$$" ] || return 0
    rm -f "$DEV_INSTANCE_FILE"
}

dev_instance_claim() {
    local launcher="$1" old_pid old_start command attempt
    DEV_INSTANCE_FILE="$(dirname "$DEV_STARTUP_LOG")/full-dev.pid"
    if [ -f "$DEV_INSTANCE_FILE" ]; then
        old_pid="$(head -1 "$DEV_INSTANCE_FILE")"
        old_start="$(sed -n '2p' "$DEV_INSTANCE_FILE")"
        if dev_process_matches "$old_pid" "$old_start"; then
            command="$(ps -p "$old_pid" -o command=)"
            case "$command" in
                *"bash $launcher"|*"bash $launcher "*) ;;
                *) echo "Cannot restart: the recorded process is not this AgentWorks launcher." >&2; return 1 ;;
            esac
            echo "Stopping the AgentWorks already running here…"
            kill -TERM "$old_pid" || return 1
            for attempt in $(seq 1 600); do
                dev_process_matches "$old_pid" "$old_start" || break
                sleep 0.1
            done
            if dev_process_matches "$old_pid" "$old_start"; then
                echo "The previous AgentWorks is still stopping. Wait for it to finish, then run again." >&2
                return 1
            fi
        fi
    fi
    (umask 077; printf '%s\n%s\n' "$$" "$(dev_process_start "$$")" > "$DEV_INSTANCE_FILE") || return 1
}

# Remember a listener only while it is demonstrably in our spawned process tree.
# go run can exit before its child on Ctrl+C, so retain the child's birth time too.
dev_owned_listener() {
    local root="$1" port="$2" pid parent
    for pid in $(lsof -nP -iTCP:"$port" -sTCP:LISTEN -t 2>/dev/null); do
        parent="$pid"
        while [ -n "$parent" ] && [ "$parent" -gt 1 ] 2>/dev/null; do
            if [ "$parent" = "$root" ]; then
                printf '%s\n%s\n' "$pid" "$(dev_process_start "$pid")"
                return 0
            fi
            parent="$(ps -p "$parent" -o ppid= 2>/dev/null | tr -d ' ')"
        done
    done
}

dev_stop_recorded_listener() {
    local record="$1" label="$2" pid started
    pid="$(printf '%s\n' "$record" | head -1)"
    started="$(printf '%s\n' "$record" | sed -n '2p')"
    if dev_process_matches "$pid" "$started"; then
        kill_process_tree "$pid" "$label" 50
    fi
    return 0
}
