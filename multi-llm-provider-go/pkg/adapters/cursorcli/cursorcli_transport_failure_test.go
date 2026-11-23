package cursorcli

import "testing"

// A reply made only of a dumped transport error is a failed turn; any real answer, even one quoting the error, is not.
func TestCursorTransportFailureOnlyForWholeErrorReplies(t *testing.T) {
	for name, tc := range map[string]struct {
		reply  string
		failed bool
	}{
		"server error sentence":      {"Something went wrong communicating with the server. Please try again.", true},
		"connect error with a frame": {"Error: ConnectError: [unavailable] upstream down\n    at fetch (x.js:1:1)\n", true},
		"retriable error":            {"Error: RetriableError: connection reset", true},
		"agent loop diagnostics":     {"Error: RetriableError: [internal] agent loop stopped", false},
		"answer quoting the error":   {"The log shows:\nError: ConnectError: [unavailable] upstream down\nso retry later.", false},
		"ordinary answer":            {"Done.", false},
	} {
		if got := cursorTransportFailure(tc.reply, "m") != nil; got != tc.failed {
			t.Errorf("%s: failed=%v, want %v", name, got, tc.failed)
		}
	}
}
