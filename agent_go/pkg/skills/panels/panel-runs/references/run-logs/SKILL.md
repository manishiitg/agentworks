# Run logs

Use runs action=get or logs for a saved run and runs action=status for its exact session. A workflow started through functions action=run_trigger can also be inspected with its trigger_status receipt. Select the correct time, workflow and run before diagnosing; Python step labels identify what happened inside that run. Quote bounded evidence without secret values or another person's private chats. A successful HTTP request is not a completed workflow. Reading logs does not require starting another run; a rerun repeats real effects.

For an agent() turn, read its answer_file for the full final answer and its saved provider log for tool evidence. A completed turn is not proof that its task succeeded. The workflow's Python checks and saved outputs decide the result.

Saved Python scripts return `log_file`, `log_error` and `log_complete`, whether called from chat or a workflow. The timestamped file retains stdout and stderr beyond the inline reply limit; read a bounded part rather than pasting the entire log into chat. An incomplete capture is not a completed full log.

The platform shell also returns a log path when output spills, times out or continues in the background. Claude and Codex CLI chats use their own native shell and file tools instead; those commands do not return this platform `log_file` receipt. The same distinction applies inside a workflow agent step. Use its native conversation/tool evidence, or the saved-script receipt when it calls a script; never invent a missing path.
