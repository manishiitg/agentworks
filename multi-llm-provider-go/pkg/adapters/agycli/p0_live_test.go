package agycli

import (
	"flag"
	"fmt"
	"os"
	"testing"
)

var codingCLIP0Live = flag.Bool("coding-cli-p0-live", false, "run the authenticated live coding-CLI P0 contract")

// Hold key mode across the whole live package run. Individual sidecar tests
// may acquire another hold, but exec-lane P0 cases also need modelProvider
// "gemini" or their inherited GEMINI_API_KEY is ignored by agy.
func TestMain(m *testing.M) {
	flag.Parse()
	if !*codingCLIP0Live || !AgyKeyModeRequested() {
		os.Exit(m.Run())
	}
	restore, err := AgyEnsureKeyMode()
	if err != nil {
		fmt.Fprintln(os.Stderr, "agy P0 key mode:", err)
		os.Exit(1)
	}
	code := m.Run()
	restore()
	os.Exit(code)
}
