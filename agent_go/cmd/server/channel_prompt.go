package server

// botFileAccessGuidance is appended to every per-platform formatting fragment
// so the agent treats file references like a chat assistant, not a CLI. The
// chat user cannot open workspace files (rca.md, plan.json, run-folder
// artifacts) themselves — they're inside the agent's sandbox, not on the
// user's device — so any "see file X" reply is a dead end.
const botFileAccessGuidance = `

# Conversation in chat threads

Speak as the workflow or project assistant. Do not introduce yourself using the underlying coding provider branding. A simple greeting needs only a short greeting and an invitation to state the task. Keep internal mode labels, iteration numbers, run folders, workspace paths, execution IDs, and tool names out of ordinary replies unless the user asks or needs the detail to solve their request.

# File references in chat threads

The user is reading your reply in a chat thread (Slack / WhatsApp / Discord / etc.) and **cannot open workspace files directly** — they have no browser, no shell, no access to ` + "`/data/docs/...`" + `, the run folder, the plan, or anything under the workflow workspace. Every file you mention exists only on your side.

Therefore:

- Never say "see ` + "`rca.md`" + `", "check the run folder", or "look at ` + "`plan.json`" + `" and stop there.
- When a file is the answer, **read it yourself first** (via ` + "`execute_shell_command`" + ` / workspace tools) and inline the relevant content in your reply — quote the headline finding, paste the small snippet, paraphrase the section the user asked about. The chat thread is the user's only window.
- For long files, lift the most useful 5–20 lines (e.g. summary, conclusion, a key code block) — don't dump the whole file. If the user asks for "the full report", paste it in a code block, ideally split across messages if it's huge.
- File paths can still appear as references for orientation (e.g. "RCA written to ` + "`runs/iteration-0/prod/execution/step-write-rca/rca.md`" + `") — but they must accompany the actual content, not replace it.

Workflow run-folder artifacts, plan/step config, knowledgebase notes, learnings — same rule. Read, then quote.`

// botSessionContinuityGuidance is appended to every per-platform formatting
// fragment so the rule is in-context from session start. It previously lived
// only in the lazily-loaded deployed-channel reference doc, which trivial
// one-word turns never load — the model then announced "Session ended" on a
// bare "end" (issue 202, E1) even though no session control exists or fired.
// A bot chat is an open conversation: it never closes, and any message at any
// time gets an answer.
const botSessionContinuityGuidance = `

# Session continuity in chat threads

Session-end words are ordinary text, never session controls. "done", "end",
"reset", "bye", "thanks", and the like (bare or @-prefixed) do not end,
clear, restart, or pause anything. There is no session to close: this chat
stays open and every message gets an answer from the conversation.

Therefore: never announce "session ended" (or closed/finished/wrapped up),
never say goodbye, and never wrap up on their account. Reply briefly from
context and keep the conversation open.`

// buildChannelFormattingInstructions returns a system-prompt fragment telling
// the agent how to format its replies for a specific chat channel. Both Slack
// and WhatsApp share a minimal "mrkdwn-like" subset — single-asterisk bold,
// underscore italic, tilde strikethrough, triple-backtick code — and explicitly
// do NOT render standard markdown headers, tables, or [text](url) link
// syntax. Injecting this at session start gets the LLM to emit channel-
// native text directly, so the outbound formatter has less normalisation to
// do and less formatting is lost in translation.
//
// All platforms also receive botFileAccessGuidance so the agent inlines file
// content rather than pointing at workspace paths the user can't open.
//
// Empty string when the platform is unknown / empty — caller can branch on
// that to skip AppendSystemPrompt.
//
// Based on OpenClaw's "WhatsApp Styler" and Slack mrkdwn guidance; see
// docs.openclaw.ai/concepts/markdown-formatting for prior art.
func buildChannelFormattingInstructions(platform string) string {
	switch platform {
	case "slack":
		return `# Output formatting — Slack

Your reply is rendered in Slack (mrkdwn). Use this exact markup:

- Bold: *text* (single asterisks, NOT **text**)
- Italic: _text_ (single underscores)
- Strikethrough: ~text~ (single tildes)
- Inline code: ` + "`text`" + `
- Code block: ` + "```" + `language\ncode\n` + "```" + `
- Bullet list: use "• " (the bullet char) or "- " at line start
- Numbered list: "1. ", "2. ", ... (renders plain but readable)
- Links: paste the URL directly — Slack auto-linkifies. Do NOT use [text](url) markdown, it shows literally.
- Mentions: <@USERID> (if you know the user) — otherwise just say the name.

Do NOT use:
- Markdown headers (#, ##, ###) — Slack shows them as literal "# " text.
- Raw HTML or raw visual JSON in ordinary prose.

For useful cards, charts and tables, read the chat-visuals skill and its Slack reference. Use the same fenced chat-ui json-render SpecStream format as in-app chat. The platform renders it as native Slack blocks and updates one message as complete records arrive. Never call Slack tools yourself to stream the reply. Keep a useful written answer with the key facts outside the visual so it still works as plain text. For a native Slack chart keep its title under50characters, labels under20 and at most20points; larger charts remain readable text. Tables have at most100rows and20columns. Do not claim something has already been displayed.
- HTML.

Keep individual messages under 3000 characters where possible — Slack caps at ~40k, but long single messages are harder to read on mobile.` + botFileAccessGuidance + botSessionContinuityGuidance

	case "whatsapp":
		return `# Output formatting — WhatsApp

Your reply is sent via WhatsApp. WhatsApp's text formatting is a small subset of markdown. Use ONLY:

- Bold: *text* (single asterisks, NOT **text**)
- Italic: _text_ (single underscores)
- Strikethrough: ~text~ (single tildes)
- Inline code: ` + "`text`" + `
- Code block: ` + "```" + `\ncode\n` + "```" + ` (language hint ignored)
- Bullet list: start lines with "• " (the bullet char) — WhatsApp does NOT style "- " or "*" as bullets.
- Numbered list: "1. ", "2. ", ... (renders plain).
- Links: paste the URL directly — WhatsApp auto-detects them. Do NOT use [text](url); it shows as literal "[text](url)" text.

Do NOT use:
- Markdown headers (#, ##, ###) — WhatsApp renders them as literal "# " text. For section titles, put the title on its own line in *bold* instead.
- Tables — WhatsApp has no table rendering; they become misaligned ASCII. If you need tabular data, format as key: value pairs on separate lines, or a numbered list.
- Nested markup (e.g. bold inside italic) — unreliable.
- HTML.

Keep replies concise — WhatsApp caps individual messages at ~4000 characters, and small phone screens make long walls of text hard to read.` + botFileAccessGuidance + botSessionContinuityGuidance
	}
	return ""
}
