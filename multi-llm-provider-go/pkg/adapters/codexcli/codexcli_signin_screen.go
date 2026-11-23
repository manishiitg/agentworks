package codexcli

import "strings"

// CodexLoginRequiredError means Codex's terminal is waiting on its own sign-in screen, so a turn can never
// start in it. The text is what the person sees.
type CodexLoginRequiredError struct{}

func (*CodexLoginRequiredError) Error() string {
	return "Codex is not signed in for this account. Reconnect your Codex account in your account settings, or pick another agent. Your message is saved."
}

// codexPaneAsksForSignIn reports Codex's first-run sign-in screen: its "Sign in with ChatGPT" and "Provide your own
// API key" option lines, each on its own line (a "> " marker and "N." number are allowed), in the visible tail of the
// pane. The "Press enter" footer is not required: a variant of the screen without it kept a turn open for 40 minutes.
// The older scrollback is ignored, so a screen the person already answered cannot trigger it, and a sentence that
// mentions both phrases on one line does not count.
func codexPaneAsksForSignIn(captured string) bool {
	signIn, ownKey := false, false
	for _, line := range strings.Split(strings.ToLower(lastNCodexLines(stripCodexANSI(captured), 25)), "\n") {
		line = strings.TrimLeft(strings.TrimSpace(line), "> \u203a")
		line = strings.TrimLeft(line, "0123456789")
		line = strings.TrimSpace(strings.TrimPrefix(line, "."))
		signIn = signIn || strings.HasPrefix(line, "sign in with chatgpt")
		ownKey = ownKey || strings.HasPrefix(line, "provide your own api key")
	}
	return signIn && ownKey
}
