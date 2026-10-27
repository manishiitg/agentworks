package server

import (
	"context"

	providers "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
)

// Account actions use the same transport as chats. CLI and SDK browser logins
// are different stores; a CLI login cannot authenticate an SDK chat (PLAT-872-64).
func cursorAccountCommand(ctx context.Context, action string) (providerSetupCommand, error) {
	if !providers.CursorSDKEnabled() {
		switch action {
		case "login":
			return providerSetupCommands["cursor-cli"]["authenticate"], nil
		case "status":
			return providerStatusCommands["cursor-cli"], nil
		case "models":
			return providerSetupCommand{command: "cursor-agent", args: []string{"--list-models"}}, nil
		case "logout":
			return providerLogoutCommands["cursor-cli"], nil
		}
	}
	command, args, reads, err := cursorcli.SDKAuthCommand(ctx, action)
	return providerSetupCommand{command: command, args: args, readPaths: reads}, err
}
