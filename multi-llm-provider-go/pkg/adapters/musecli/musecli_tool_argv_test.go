package musecli

import (
	"context"
	"slices"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Bridge-only Muse has its shell and writes disabled. Full CLI Muse runs with
// --yolo (no approvals, no Muse sandbox): its own Bubblewrap probe fails inside
// AgentWorks' lock and kept the TUI from settling.
func TestMuseToolArgv(t *testing.T) {
	if got := museToolArgv(true); !slices.Equal(got, []string{"--disable-shell", "--disable-write"}) {
		t.Fatalf("bridge-only argv = %v", got)
	}
	if got := museToolArgv(false); !slices.Equal(got, []string{"--yolo"}) {
		t.Fatalf("full argv = %v, want --yolo", got)
	}
}

// Interactive launches carry the chat's reasoning effort (the exec lane always
// did); an unknown value is refused, not dropped.
func TestMuseEffortArgv(t *testing.T) {
	if got, err := museEffortArgv(context.Background()); err != nil || len(got) != 0 {
		t.Fatalf("no account: %v %v", got, err)
	}
	ctx := museWithAccount(context.Background(), &llmtypes.CallOptions{ReasoningEffort: "Max"}, "")
	if got, err := museEffortArgv(ctx); err != nil || !slices.Equal(got, []string{"--reasoning-effort", "max"}) {
		t.Fatalf("max effort: %v %v", got, err)
	}
	ctx = museWithAccount(context.Background(), &llmtypes.CallOptions{ReasoningEffort: "turbo"}, "")
	if _, err := museEffortArgv(ctx); err == nil {
		t.Fatal("an unknown effort must be refused")
	}
}
