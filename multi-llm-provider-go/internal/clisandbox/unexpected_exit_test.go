package clisandbox

import (
	"os/exec"
	"testing"
)

func TestUnexpectedExitNamesStatusAndLastStderrLine(t *testing.T) {
	failed := exec.Command("sh", "-c", "exit 3").Run()
	if got := UnexpectedExit("Muse", failed, "booting\nout of memory\n\n").Error(); got != "Muse stopped unexpectedly (exit 3): out of memory" {
		t.Fatalf("got %q", got)
	}
	if got := UnexpectedExit("Pi", failed, "").Error(); got != "Pi stopped unexpectedly (exit 3)" {
		t.Fatalf("got %q", got)
	}
	if got := UnexpectedExit("Pi", nil, "").Error(); got != "Pi stopped unexpectedly (exit 0)" {
		t.Fatalf("got %q", got)
	}
}
