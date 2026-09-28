# Clicking a decision option only filled the chat box

**Status:** fixed 2026-09-26 (`d2c8f258b`, `e41c4bb52`).

## Symptom

On a "Needs your decision" card, clicking an option ("This workflow's own bot")
appeared to do nothing.

## Root cause

By design the click only placed the answer in the workflow chat's composer and
waited for Send, with no signal on the card. "Take best action" on the same card
sends immediately, so the options looked broken next to it.

## Fix

An option now sends the answer through `sendWorkspacePaneMessageToChat`, the
path Ask AI and "Take best action" use (it finds or opens the chat tab and
queues behind a running turn), with a toast. Like Ask AI it takes two clicks:
the first arms the option ("Click again to send"), a second within 4 s sends,
and a second click under 0.6 s is ignored. "Ask in chat" still fills the box so
a note can be added. Test: `ReportHumanInputPanel.refresh.test.tsx`.
