package server

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// setMCPAppCmd sets a deployment's sign-in app from the operator's shell, for
// an admin who has the provider's client file but no browser session on the
// server (docs/design/code_private_mcp.md, "Sign-in apps"). The file is read
// from stdin, so the secret is never written to disk on the host, and is
// stored sealed exactly as the admin card stores it.
var setMCPAppCmd = &cobra.Command{
	Use:   "set-mcp-app --key google < client_secret.json",
	Short: "Set a sign-in app (Google, GitHub, ...) from a client JSON on stdin",
	Long: `Reads an OAuth client (Google's client_secret_*.json with a "web" or "installed" object, or {"client_id": ..., "client_secret": ...}) from standard input and stores it as the deployment's sign-in app for --key (google, github, slack, ...), sealed at rest. Prints the key and client ID, never the secret.

Needs AUTH_SECRET and HOME (the tokens root) from the environment, like the server itself. Running servers read the app at their next connection.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		key, _ := cmd.Flags().GetString("key")
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
		key = strings.ToLower(strings.TrimSpace(key))
		if err := writeMCPApp(key, *app); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "saved sign-in app %q (client ID %s)\n", key, app.ClientID)
		return nil
	},
}

func init() {
	setMCPAppCmd.Flags().String("key", "", "Provider key: google, github, slack, ... (required)")
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
