package server

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/spf13/cobra"
)

// setMCPAppCmd sets a deployment's sign-in app from the operator's shell, for
// an admin who has the provider's client file but no browser session on the
// server (docs/design/code_private_mcp.md, "Sign-in apps"). The file is read
// from stdin, so the secret is never written to disk on the host, and is
// stored sealed exactly as the admin card stores it.
// processEuid is os.Geteuid, swappable in tests.
var processEuid = os.Geteuid

var setMCPAppCmd = &cobra.Command{
	Use:   "set-mcp-app --key google < client_secret.json",
	Short: "Set a sign-in app (Google, GitHub, ...) from a client JSON on stdin",
	Long: `Reads an OAuth client (Google's client_secret_*.json with a "web" or "installed" object, or {"client_id": ..., "client_secret": ...}) from standard input and stores it as the deployment's sign-in app for --key (google, github, slack, ...), sealed at rest. Prints the key and client ID, never the secret.

Needs AUTH_SECRET and HOME (the tokens root) from the environment, like the server itself. Running servers read the app at their next connection.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		key, _ := cmd.Flags().GetString("key")
		// The service account must own the file: a root-owned one would be
		// unreadable to the service, which then sees no app.
		if processEuid() == 0 {
			return fmt.Errorf("run this as the service account, not root: the file it writes must be readable by the server")
		}
		key = strings.ToLower(strings.TrimSpace(key))
		if configPath, _ := cmd.Flags().GetString("mcp-config"); configPath != "" {
			catalog, err := mcpclient.LoadMergedConfig(configPath, loggerv2.NewNoop())
			if err != nil {
				return fmt.Errorf("read the MCP catalog: %w", err)
			}
			known := []string{}
			for _, group := range mcpAppGroupsFor(catalog.MCPServers) {
				if group.Key == key {
					known = nil
					break
				}
				known = append(known, group.Key)
			}
			if known != nil {
				return fmt.Errorf("no sign-in app is needed for %q; providers that need one: %s", key, strings.Join(known, ", "))
			}
		}
		raw, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 64<<10))
		if err != nil {
			return err
		}
		app, err := parseMCPAppJSON(raw)
		if err != nil {
			return err
		}
		if err := ValidateConfiguredAuthSecret(); err != nil {
			return err
		}
		app.UpdatedAt, app.UpdatedBy = time.Now().UTC().Format(time.RFC3339), "operator"
		if err := writeMCPApp(key, *app); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "saved sign-in app %q (client ID %s)\n", key, app.ClientID)
		fmt.Fprintln(cmd.OutOrStdout(), "new connections use it; a connection already open keeps its client until it reconnects")
		return nil
	},
}

func init() {
	setMCPAppCmd.Flags().String("key", "", "Provider key: google, github, slack, ... (required)")
	setMCPAppCmd.Flags().String("mcp-config", "", "The deployment's MCP catalog file: refuses a key no server needs (recommended)")
	_ = setMCPAppCmd.MarkFlagRequired("key")
}

// parseMCPAppJSON reads Google's downloaded client file or a plain
// client_id/client_secret object.
func parseMCPAppJSON(raw []byte) (*mcpApp, error) {
	var file struct {
		Web       *mcpApp `json:"web"`
		Installed *mcpApp `json:"installed"`
		mcpApp
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("not a client file: %w", err)
	}
	app := file.Web
	if app == nil {
		app = file.Installed
	}
	if app == nil {
		app = &file.mcpApp
	}
	app.ClientID, app.ClientSecret = strings.TrimSpace(app.ClientID), strings.TrimSpace(app.ClientSecret)
	if app.ClientID == "" || app.ClientSecret == "" {
		return nil, fmt.Errorf("the file has no client_id and client_secret")
	}
	return &mcpApp{ClientID: app.ClientID, ClientSecret: app.ClientSecret}, nil
}
