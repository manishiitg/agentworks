package llmproviders

import (
	"context"
	"fmt"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/agycli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/musecli"
)

// ReadAccountUsage does not run inference or create a conversation. The caller
// authorizes access and supplies the complete selected-account environment.
func ReadAccountUsage(ctx context.Context, provider string, environment []string) (llmtypes.AccountUsage, error) {
	switch provider {
	case "claude-code":
		return claudecode.ReadAccountUsage(ctx, environment)
	case "codex-cli":
		return codexcli.ReadAccountUsage(ctx, environment)
	case "cursor-cli":
		return cursorcli.ReadAccountUsage(ctx, environment)
	case "muse-cli":
		return musecli.ReadAccountUsage(ctx, environment)
	case "agy-cli":
		return agycli.ReadAccountUsage(ctx, environment)
	default:
		return llmtypes.AccountUsage{}, fmt.Errorf("native account usage is unavailable")
	}
}
