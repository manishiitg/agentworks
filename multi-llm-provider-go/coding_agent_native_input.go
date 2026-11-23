package llmproviders

import (
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/agycli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/musecli"
)

// AdoptCodingAgentNativeInput scopes retained observation to a prompt the
// human already submitted in tmux. It never types or submits anything. File
// transcript providers use acceptedAt; providers without per-row timestamps
// pin their native row/ref/sequence here instead.
func AdoptCodingAgentNativeInput(provider Provider, ownerSessionID, message string, acceptedAt time.Time) error {
	switch provider {
	case ProviderCursorCLI:
		return cursorcli.AdoptNativeInput(ownerSessionID, message, acceptedAt)
	case ProviderMuseCLI:
		return musecli.AdoptNativeInput(ownerSessionID, message)
	case ProviderAgyCLI:
		return agycli.AdoptNativeInput(ownerSessionID, message, acceptedAt)
	}
	return nil
}
