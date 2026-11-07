# Run logs

Use runs action=active, runs action=step_status, automations action=runs and the scoped saved run/log files. Select the correct time, run and step before diagnosing. Quote bounded relevant evidence without secret values or other people’s private chats. Distinguish queued/running/failed/completed from a successful HTTP request; do not re-run a failed step just to read its logs.

Saved Python scripts return `log_file`, `log_error` and `log_complete`, whether called from chat or a workflow. The timestamped file retains stdout and stderr beyond the inline reply limit; read a bounded part rather than pasting the entire log into chat. An incomplete capture is not a completed full log.

The platform shell also returns a log path when output spills, times out or continues in the background. Claude and Codex CLI chats use their own native shell and file tools instead; those commands do not return this platform `log_file` receipt. The same distinction applies inside a workflow agent step. Use its native conversation/tool evidence, or the saved-script receipt when it calls a script; never invent a missing path.
