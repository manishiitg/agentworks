# Codex app: owner installation test

Run after the reviewed AgentWorks CLI and setup page are deployed. This tests
app setup without a separate Codex CLI on PATH. Use a fresh or otherwise unused
profile when available; an existing profile can still check visibility and trust.
Record whether this was a fresh install or an existing package.

1. Install/update the reviewed AgentWorks CLI and sign in to the intended
   server in your own terminal. Open the Codex app and record both versions.
   Confirm this terminal has no standalone `codex` command. After sign-in,
   `agentworks attach` must recognize the supported app; cancel selection if
   you are only checking discovery. No chat should be attached on cancellation.
2. Open **Settings → Plugins → Browse directory → Add → Add a marketplace**.
   Enter `manishiitg/agentworks-plugins` in **Source**, then select
   **Add marketplace**. Confirm the `agentworks` source appears.
3. Choose that source, open **AgentWorks** (`agentworks-codex`) and select
   **Install plugin**. Check its enabled state under **Settings → Plugins**.
   It should expose two skills, the messaging MCP and four hook definitions.
   App and bundled CLI must refer to the same Codex home/cache; report a custom
   `CODEX_HOME` if used. Native attachment from a custom home is a separate
   pending check; matching plugin configuration alone does not prove it. An existing disabled package must stay disabled during
   an update unless you choose to enable it.
4. Open **Settings → Hooks → From Plugins → AgentWorks**. Review and trust
   the SessionStart, SessionEnd, UserPromptSubmit and Stop definitions yourself.
   Use **Reload hooks** and reload the app/start a new chat when requested.
   Record whether the app showed the review prompts and any loading issue.
5. In the exact chat, type `/agentworks attach codex-app-test`, then
   `/agentworks agents`. Ask an already connected agent to send one unique
   test message to that name. Require `admitted`, an explicit reply in the
   original inbox, then `processed`. Confirm the other chat did not receive
   it. A plugin listing, notification or stored message alone is not a pass.

Report app/CLI/plugin versions, fresh/existing installation, resolved Codex
home, visible hook/review result, attachment name and receipt results. Exclude
credentials and unrelated transcripts. Native acceptance remains pending until
these results are recorded; source or mocked browser checks do not replace it.
