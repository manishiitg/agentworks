## Workspace provider tools

One provider-backed workspace tool is active:

- **`generate_text_llm(user_message, tier)`** runs one text-model call using
  only the current workflow's `capabilities.llm_config`. `high`, `medium`, and
  `low` map to that workflow's `tier_1`, `tier_2`, and `tier_3`. It never uses
  a global tier configuration and fails closed when there is no current
  workflow. Coding-CLI tiers always run as fresh structured one-shot calls;
  they never use tmux, interactive persistence, or resume.

## When to use it

Use **`generate_text_llm`** for one bounded, additional model operation, such
as summarising supplied material, extracting a structured draft, classifying a
defined input, or generating content for a downstream deterministic check.
Choose `low` for simple transformations and high-volume inexpensive work,
`medium` for normal synthesis, and `high` only when the task's reasoning or
quality risk justifies its additional cost. Put the complete requested outcome
in `user_message`; inspect the returned `provider` and `model_id` along with
the response rather than assuming a tier maps to one permanent model.

## Scripted workflow use

In a scripted/code-execution step, these names are **not shell commands** and
must never be replaced with a direct provider request. First read
`references/mcp-bridge.md`, inspect the session's `<available_tools>`, and use
`get_api_spec` for the exact current schema. Then call the granted custom tool
through the authenticated MCP bridge at `$MCP_CUSTOM/generate_text_llm`, following the bridge's response-envelope rules.

Use `execute_shell_command` only to run the bridge-calling script. Do not
invent an endpoint, call a provider SDK directly,
or put provider/MCP credentials in source code, shell text, or output. The
bridge authentication is injected for the step; provider credentials remain
workspace-managed through `set_provider_auth`.

Image, video, audio, music, transcription, and image-reading provider tools
are deprecated and hidden. Do not call them, advertise their providers, or
collect media-provider credentials through workspace provider setup.

For ordinary chat/text provider credentials, use the existing provider setup
flow. A Pi sub-provider credential (including MiniMax) is allowed only when
explicitly configuring Pi for a text model; it is not a media-provider setup.
Store any provider API key via `set_provider_auth` — encrypted, workspace-backed
— never by hand-editing a config file or pasting the raw key into a shell
command or script.
