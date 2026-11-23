package codexcli

import (
	"errors"
	"strings"
	"testing"
)

// The pane text found on a stuck production session: Codex waiting on its sign-in screen.
const codexSignInScreen = "  or connect an API key for usage-based billing\n" +
	"> 1. Sign in with ChatGPT\n     Usage included with Plus, Pro, Business, and Enterprise plans\n" +
	"  2. Sign in with Device Code\n     Sign in from another device with a one-time code\n" +
	"  3. Provide your own API key\n     Pay for what you use\n\n  Press enter to continue\n"

func TestCodexPaneAsksForSignIn(t *testing.T) {
	if !codexPaneAsksForSignIn(codexSignInScreen) {
		t.Fatal("the sign-in screen was not recognised")
	}
	// The variant seen on a stuck chat: both option lines, no "Press enter" footer.
	if !codexPaneAsksForSignIn("  Sign in with ChatGPT\n  Sign in with Device Code\n  Provide your own API key\n") {
		t.Fatal("the sign-in screen without its footer was not recognised")
	}
	for name, pane := range map[string]string{
		"working":            "\u2022 Working (12s \u2022 esc to interrupt)\n> \n",
		"only one phrase":    "Sign in with ChatGPT to continue\n",
		"answered earlier":   codexSignInScreen + strings.Repeat("some later output line\n", 40),
		"empty":              "",
		"an answer about it": "You can sign in with ChatGPT or provide your own API key.\n",
	} {
		if codexPaneAsksForSignIn(pane) {
			t.Errorf("%s: wrongly treated as the sign-in screen", name)
		}
	}
}

func TestCodexLoginRequiredErrorIsPlainWords(t *testing.T) {
	var err error = &CodexLoginRequiredError{}
	var target *CodexLoginRequiredError
	if !errors.As(err, &target) {
		t.Fatal("errors.As must find the typed error")
	}
	for _, want := range []string{"not signed in", "Reconnect", "pick another agent", "message is saved"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q lacks %q", err.Error(), want)
		}
	}
}
