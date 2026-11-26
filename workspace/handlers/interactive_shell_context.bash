# Sourced once at the end of the managed shell's existing prompt setup.
# No command is recovered from terminal output. A history item is usable only
# when the shell itself added exactly one item since the preceding prompt.
if [ -z "${__aw_context_installed:-}" ]; then
  __aw_context_installed=1
  __aw_context_declaration=$(declare -p PROMPT_COMMAND 2>/dev/null)
  if [[ $__aw_context_declaration == 'declare -- '* || $__aw_context_declaration == 'declare -x '* ]] && [ -n "${BASH_VERSION:-}" ] && [ -z "$(trap -p DEBUG)" ] && [[ $- != *T* ]]; then
    __aw_context_prompt=$PROMPT_COMMAND
    __aw_context_serial=0
    __aw_context_active=
    __aw_context_seen=1
    __aw_context_history() {
      local entry LC_ALL=C
      __aw_context_number=; __aw_context_command=
      entry=$(HISTTIMEFORMAT= builtin history 1 | head -c 32769)
      if [ "${#entry}" -le 32768 ] && [[ $entry =~ ^[[:space:]]*([0-9]+)[[:space:]]+(.*)$ ]]; then
        __aw_context_number=${BASH_REMATCH[1]}
        __aw_context_command=${BASH_REMATCH[2]}
      fi
    }
    __aw_context_emit() {
      # /dev/tty, not stdout: redirects and pipelines must remain untouched.
      printf '\033Ptmux;\033\033]6973;%s\007\033\\' "$1" >/dev/tty 2>/dev/null
    }
    __aw_context_before() {
      local frame encoded
      # Never trace prompt hooks, functions, command substitutions or subshells.
      for frame in "${FUNCNAME[@]:1}"; do
        [ "$frame" = __aw_context_after ] && return
      done
      [[ $BASH_COMMAND == __aw_context_after* ]] && return
      [ "${BASH_SUBSHELL:-0}" -eq 0 ] || return
      [ -z "$__aw_context_seen" ] || return
      __aw_context_seen=1
      __aw_context_history
      # HISTCONTROL/HISTIGNORE, history disabled, resets or custom history hooks
      # can omit/change an entry. In all those cases, attach nothing.
      [ -n "$__aw_context_previous" ] && [ -n "$__aw_context_number" ] || return
      [ "$__aw_context_number" -eq "$((__aw_context_previous + 1))" ] || return
      [ -n "$__aw_context_command" ] && [ -z "$(jobs -pr)" ] || return
      __aw_context_serial=$((__aw_context_serial + 1))
      __aw_context_active="__AW_GENERATION__-$__aw_context_serial"
      encoded=$(printf %s "$__aw_context_command" | base64 | tr -d '\n')
      __aw_context_emit "C;$__aw_context_active;$encoded"
    }
    __aw_context_status() { return "$1"; }
    __aw_context_after() {
      local status=$?
      if [ -n "$__aw_context_active" ] && [ -z "$(jobs -pr)" ]; then
        __aw_context_emit "D;$__aw_context_active;$status"
      fi
      __aw_context_active=
      # Preserve the previous prompt setup and the command's exit status.
      __aw_context_status "$status"
      eval "$__aw_context_prompt"
      __aw_context_history
      __aw_context_previous=${__aw_context_number:-0}
      __aw_context_seen=
      return "$status"
    }
    PROMPT_COMMAND=__aw_context_after
    trap '__aw_context_before' DEBUG
    __aw_context_after
  fi
fi
