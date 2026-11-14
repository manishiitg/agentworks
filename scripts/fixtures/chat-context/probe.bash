__aw_prompt() { local s=$?; HISTARG=$1; printf '\nPROMPT history=%s prev=%s\n' "$HISTARG" "$__aw_prev" >/dev/tty; __aw_prev=$HISTARG; __aw_seen=; PS1='PROBE> '; return "$s"; }
__aw_debug() { local HISTARG=$1; case "$BASH_COMMAND" in __aw_prompt*) return;; esac; if [ -z "$__aw_seen" ]; then __aw_seen=1; printf '\nPRE history=%s prev=%s command=<%s> entry=<%s>\n' "$HISTARG" "$__aw_prev" "$BASH_COMMAND" "$(history 1); $(fc -ln 0)" >/dev/tty; fi; }
PROMPT_COMMAND='__aw_prompt "$HISTCMD"' 
trap '__aw_debug "$HISTCMD" "$BASH_COMMAND"' DEBUG
