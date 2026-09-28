package virtualtools

import (
	"context"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// google_workspace_cli gives the agent direct, general-purpose access to
// gogcli for explicitly authorized Gmail reads/drafts/sends/replies and the
// Google Workspace services a connection was authorized for (Drive, Sheets,
// Docs, Slides, Calendar) — see
// services/google_services.go. Unlike notify_user (a fixed send operation),
// there is no single obvious operation for these services, so the agent
// drives the CLI itself rather than calling a bespoke tool per operation.
//
// The access token never reaches the agent or this file: RunGoogleCLI
// resolves it server-side from the connection's stored credential and injects
// it into the gog invocation. Enforcement (which services, read vs write) is
// done there too, backed by gogcli's own --readonly flag — this file only
// shapes the tool description and passes args through.

func createGoogleCLITool() llmtypes.Tool {
	serviceNames := "gmail, " + strings.Join(sortedGoogleServiceCatalogKeys(), ", ")
	description := fmt.Sprintf(
		"Run a gogcli (`gog`) command against a Google account connection the user has authorized for a specific service. "+
			"Use this for Gmail search/message retrieval and, when the connection explicitly enables agent writes, Gmail drafts and send/reply; also use it for Google Drive, Sheets, Docs, Slides, and Calendar. notify_user remains the fixed notification-send path. "+
			"Before calling this tool, check installed skills; if necessary install `https://github.com/openclaw/gogcli` with `install_skill`, then load `gog` and the relevant `gog-*` service skill with `read_skill`. Those versioned skills, not this description, define current command syntax. "+
			"args is the argument list to pass to `gog`, EXCLUDING the `gog` binary name and any account or authentication flags. "+
			"The FIRST element must be one of: %s — whichever the connection was authorized for. "+
			"Never pass --access-token, --account, --client, or --home: the server injects the authorized connection's credential automatically, and passing "+
			"any of those is rejected. Prefer --json or --plain for parseable output. If the connection was only granted read access to a service, "+
			"gogcli itself blocks any mutating call against it (via --readonly, plus --gmail-no-send for Gmail) — a rejection there means the user must explicitly enable write access "+
			"for that service, not that the command was malformed. If no connection is authorized for the requested service at all, the tool returns a "+
			"clear error naming which service is missing; tell the user which service to connect rather than retrying blindly. "+
			"Optionally pass connection_id to target a specific account; omitted, the account's default connection is used.",
		serviceNames,
	)
	return llmtypes.Tool{
		Type: "function",
		Function: &llmtypes.FunctionDefinition{
			Name:        "google_workspace_cli",
			Description: description,
			Parameters: llmtypes.NewParameters(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"args": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "Arguments to pass to `gog`, starting with the service name. Do not include --access-token/--account/--client/--home.",
					},
					"connection_id": map[string]interface{}{
						"type":        "string",
						"description": "Optional. Which Google account connection to use; defaults to the account's default connection.",
					},
				},
				"required": []string{"args"},
			}),
		},
	}
}

func sortedGoogleServiceCatalogKeys() []string {
	catalog := services.GoogleServiceCatalog()
	keys := make([]string, 0, len(catalog))
	for key := range catalog {
		keys = append(keys, key)
	}
	sort.Strings(keys) // deterministic tool description across restarts
	return keys
}

func handleGoogleWorkspaceCLI(ctx context.Context, args map[string]interface{}) (string, error) {
	rawArgs, _ := args["args"].([]interface{})
	if len(rawArgs) == 0 {
		return "", fmt.Errorf("args is required and must start with a supported Google service name; read the attached gog service skill for current syntax")
	}
	cliArgs := make([]string, 0, len(rawArgs))
	for _, item := range rawArgs {
		s, ok := item.(string)
		if !ok {
			return "", fmt.Errorf("args must be a list of strings")
		}
		cliArgs = append(cliArgs, s)
	}
	connectionID, _ := args["connection_id"].(string)

	// A Code session uses only its own private accounts, as their owner;
	// every other session only shared ones.
	codeWorkspace, userID := common.GmailScopeFromContext(ctx)
	output, err := services.RunGoogleCLIIn(ctx, strings.TrimSpace(connectionID), cliArgs, services.GmailUseScope{CodeWorkspace: codeWorkspace, UserID: userID})
	if err != nil {
		if strings.TrimSpace(output) != "" {
			return "", fmt.Errorf("%w\noutput:\n%s", err, output)
		}
		return "", err
	}
	if strings.TrimSpace(output) == "" {
		return "(no output)", nil
	}
	return output, nil
}
