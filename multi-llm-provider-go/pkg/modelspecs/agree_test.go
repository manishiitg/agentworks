package modelspecs_test

import (
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/utils"
	"github.com/manishiitg/multi-llm-provider-go/pkg/modelspecs"
)

// Real bug: Opus 5.5 showed a 200K limit from one provider's list while Claude Code served it with 1M. Every
// provider that lists a model the shared table knows must agree with it on the context window.
func TestProvidersAgreeWithTheSharedTable(t *testing.T) {
	for _, m := range utils.GetAllModelMetadata() {
		spec, ok := modelspecs.For(m.ModelID, m.Provider)
		if !ok || m.Provider == "minimax-coding-plan" { // MiniMax serves its own copies of Claude models
			continue
		}
		if m.ContextWindow != spec.ContextWindow {
			t.Errorf("%s via %s: context window %d, shared table %d", m.ModelID, m.Provider, m.ContextWindow, spec.ContextWindow)
		}
	}
}
