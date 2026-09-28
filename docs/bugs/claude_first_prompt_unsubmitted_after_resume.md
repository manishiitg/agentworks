# Claude's first prompt sat unsubmitted after a resumed launch

**Status:** fixed 2026-09-26 (multi-llm-provider-go `95570a4`), deployed to RTS.

## Symptom

After a deploy restarted RTS, the first message into a crew chat (gptlive1,
10:01 UTC) was pasted into Claude Code's input box but never submitted. The
turn waited until the user pressed Enter by hand in the terminal (the log shows
`[CONTROL] Delivered control key "Enter"` at 10:02:20); first output came 64 s
after the message.

## Evidence

- `10:01:13 [claude-code] Restored native runtime from chat history` — a cold
  start that resumed a conversation of ~1,000 messages.
- `10:01:16 Sending to LLM API`, then no submit confirmation and no retry.
- The next message (10:04, CLI already running) went through the live-input
  path and was confirmed in 0.3 s.

## Root cause

The first-prompt path (`waitForPromptAccepted`) counted *any* activity on the
screen (spinner, "esc to interrupt") as proof the prompt was accepted. A resumed
conversation shows activity while it loads, so the check passed with our prompt
still sitting in the `❯` box; the Enter retries never ran.

## Fix

`claudeInitialPromptAccepted`: activity counts only once our message has left
the input box; while the box still holds it, the loop keeps pressing Enter.
Unit test with the exact screen state; live tests (fresh first prompt, native
resume) pass against the real Claude CLI.
