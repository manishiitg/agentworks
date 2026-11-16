package server

import "slices"

// Enough room for personal SVG art and expressive CSS, still bounded per theme.
// The HTTP envelope also allows JSON escaping of every CSS byte plus metadata.
const appearanceCSSMaxBytes = 128 << 10
const appearanceRequestMaxBytes = 8 * appearanceCSSMaxBytes

// One documented selector inventory. Values describe presentation only: they
// never expose identifiers or create an execution/permission state machine.
var appearanceHookGuide = map[string]string{
	"[data-theme-chat-row]":                    "Measured core transcript content row; responsive reading column. Never restyle the virtualizer scroll/measurement wrappers.",
	"[data-app-main]":                          "The main workspace; personal background art may be static here.",
	"[data-app-ambient]":                       "Empty full-window decorative layer. Pointer-events none; use pseudo-elements, not scripts or controls.",
	"html[data-appearance-visible=\"true\"]":   "Document visibility, not focus or execution. Pause custom motion unless this is true.",
	"[data-theme-surface]":                     "Core app/main/sidebar, panel, composer/composer-band, chat, tool, code, terminal, dialog, menu, popover and empty surfaces. Legacy dialogs/menus also expose their native role selectors below.",
	"[data-theme-row]":                         "Core navigation/project/agent rows. No agent IDs or messages in the hook.",
	"[data-theme-card]":                        "Shared Card primitive, still using surface=panel. No new wrapper.",
	"[data-theme-control]":                     "Shared button/input/textarea/select and composer-input controls. Use native :disabled, :focus-visible and aria-* attributes for their existing state.",
	"[data-theme-action]":                      "Composer send, stop, attach, microphone and composer-menu. Presentation only; never hide or cover an action.",
	"[data-theme-composer-controls]":           "The shared composer actions band.",
	"[data-theme-message-role]":                "Transcript user/assistant and system-activity containers. This is the renderer role, NOT proof who sent a message.",
	"[data-theme-message-body]":                "Text/Markdown message body, separate from delivery receipts and actions.",
	"[data-theme-agent-state]":                 "Existing menu rows or state glyphs: running/waiting/failed/done/quiet (quiet is only supplied by rows). Quiet is not proof of idle; unread failed/done clear on opening, visual holds remain.",
	"[data-theme-agent-selected]":              "Existing selected agent boolean, true/false.",
	"[data-theme-team-working]":                "Existing viewer-admitted Team summary boolean; no separate browser query or count.",
	"[data-theme-team-waiting]":                "Existing viewer-admitted Team waiting boolean, not necessarily an alert.",
	"[data-theme-turn-live]":                   "Existing live working-group boolean. Absence (virtualized or unmounted) is unknown.",
	"[data-theme-tools-running]":               "Existing live-and-running tool group boolean. Gate tool effects on true, not historical tool status alone.",
	"[data-theme-tool-state]":                  "Tool card's existing running/ok/error display status. Historical unless inside a live tools/turn group.",
	"[data-theme-loader]":                      "Shared decorative loading glyph; caller owns actual status/lifetime. Paused when hidden/reduced motion.",
	"svg.lucide":                               "Existing Lucide outline icon family (stable library class). Personal stroke/scale/colour fields exclude authored report, code and terminal islands; logos and loading glyphs are separate.",
	"[data-chat-composer-card]":                "Existing composer card hook remains stable.",
	"[data-chat-find-scope]":                   "Existing transcript region; do not change scroll/virtualizer geometry blindly.",
	".conversation-markdown":                   "Existing conversation Markdown body hook remains stable.",
	"[data-testid=\"transcript-user-bubble\"]": "Existing short user bubble hook remains stable.",
	"[data-product-settings]":                  "Existing account/settings menu region.",
	"nav[aria-label=\"Workspace navigation\"]": "Existing primary navigation hook.",
	"[role=\"dialog\"]":                        "All existing semantic dialogs, including feature-specific legacy surfaces.",
	"[role=\"menu\"]":                          "All existing semantic menus; role=menuitem selects their rows.",
}

func appearanceHookSelectors() []string {
	hooks := make([]string, 0, len(appearanceHookGuide))
	for hook := range appearanceHookGuide {
		hooks = append(hooks, hook)
	}
	slices.Sort(hooks)
	return hooks
}
