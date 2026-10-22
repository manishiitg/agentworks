package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/oauth"
	"github.com/spf13/cobra"
)

// Importing is an operator action, not an agent tool. Read the old encrypted
// file and reseal it with the destination's path-bound AAD; never copy ciphertext
// across paths or emit plaintext. Existing files are preserved for rollback.
func importVaultOAuthCredential(ctx context.Context, api *StreamingAPI, id string, apply bool) (string, error) {
	if err := ValidateConfiguredAuthSecret(); err != nil {
		return "", err
	}
	c, err := vaultSavedConnection(ctx, id)
	if err != nil {
		return "", err
	}
	config, err := mcpclient.LoadMergedConfig(api.mcpConfigPath, api.logger)
	if err != nil {
		return "", errors.New("cannot read legacy MCP configuration")
	}
	name, cfg, err := config.ResolveServer(c.OAuthServer)
	missingProvider := err != nil || cfg.OAuth == nil
	if !missingProvider && cfg.URL != c.UpstreamURL {
		return "", errors.New("legacy provider does not match the Vault connection")
	}
	if missingProvider {
		// A removed catalog template must not strand its encrypted login in the
		// retired provider store. Preserve the real account data; chat can finish
		// provider discovery later without inventing authorization endpoints.
		name = c.OAuthServer
		cfg = mcpclient.MCPServerConfig{URL: c.UpstreamURL, OAuth: &oauth.OAuthConfig{TokenFile: getUserTokenFilePath(platformMCPTokenUserID, name), Resource: c.UpstreamURL}}
	}

	destination := expandPath(getUserTokenFilePath(platformMCPTokenUserID, vaultCredentialName(id)))
	configPath := expandPath(vaultCredentialConfigPath(id))
	tokenExists := false
	if _, err := os.Stat(destination); err == nil {
		tokenExists = true
	} else if !os.IsNotExist(err) {
		return "", errors.New("cannot inspect destination credential")
	}
	if tokenExists {
		if _, err := os.Stat(configPath); err != nil {
			return "", errors.New("incomplete destination credential; inspect it before retrying")
		}
		if missingProvider {
			return "credentials saved; provider setup required", nil
		}
		return "already imported", nil // Never overwrite a refreshed or reauthorized token.
	}
	if _, err := os.Stat(configPath); err == nil || !os.IsNotExist(err) {
		return "", errors.New("destination configuration already exists; inspect it before importing")
	}
	source := expandPath(cfg.OAuth.TokenFile)
	if source == "" {
		source = expandPath(getUserTokenFilePath(platformMCPTokenUserID, name))
	}
	if source == "" || !((platformClientSealer{}).Handles(source)) {
		return "", errors.New("legacy token must be under the platform tokens directory")
	}
	if _, err := os.Stat(source); os.IsNotExist(err) {
		return "sign-in required", nil
	} else if err != nil {
		return "", errors.New("cannot inspect legacy credential")
	}
	data, err := oauth.ReadTokenFile(source)
	if err != nil || len(data) > 64<<10 {
		return "", errors.New("cannot read legacy credential")
	}
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if json.Unmarshal(data, &token) != nil || token.AccessToken == "" {
		return "", errors.New("invalid legacy OAuth token")
	}
	saved := *cfg.OAuth
	clientPath := cfg.OAuth.ClientSecretFile
	if clientPath == "" {
		clientPath = getUserClientFilePath(platformMCPTokenUserID, name)
	}
	clientPath = expandPath(clientPath)
	if _, err := os.Stat(clientPath); err == nil {
		if !((platformClientSealer{}).Handles(clientPath)) {
			return "", errors.New("legacy client secret is outside the platform tokens directory")
		}
		clientData, err := oauth.ReadTokenFile(clientPath)
		var client registeredClient
		if err != nil || json.Unmarshal(clientData, &client) != nil {
			return "", errors.New("cannot read legacy client registration")
		}
		if saved.ClientID == "" {
			saved.ClientID = client.ClientID
		}
		if saved.ClientSecret == "" {
			saved.ClientSecret = client.ClientSecret
		}
		if saved.RedirectURL == "" {
			saved.RedirectURL = client.RedirectURI
		}
	}

	saved.ClientSecretFile = ""
	saved.TokenFile = destination
	configData, err := json.Marshal(saved)
	if err != nil {
		return "", errors.New("cannot encode destination OAuth configuration")
	}
	if !apply {
		return "ready to import", nil
	}
	mutex := platformMCPOAuthMutex("vault:" + id)
	mutex.Lock()
	defer mutex.Unlock()
	// Publish the config before the token. A concurrent broker fails closed
	// until both files exist. A partial write is reported and never overwritten.
	if err := writeImportedVaultCredential(configPath, configData); err != nil {
		return "", errors.New("cannot seal destination OAuth configuration")
	}
	if err := writeImportedVaultCredential(destination, data); err != nil {
		return "", errors.New("cannot seal destination OAuth token")
	}
	if missingProvider {
		return "credentials saved; provider setup required", nil
	}
	return "imported", nil
}

func writeImportedVaultCredential(path string, data []byte) error {
	sealed, err := (platformClientSealer{}).Seal(path, data)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(sealed)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(path)
		return errors.New("cannot write imported credential")
	}
	return nil
}

var importVaultOAuthCmd = &cobra.Command{
	Use:   "import-vault-oauth --connection-id ID --mcp-config FILE [--apply]",
	Short: "Reseal an existing shared OAuth credential for a Vault connection",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if processEuid() == 0 {
			return errors.New("run this as the deployment service account, not root")
		}
		id, _ := cmd.Flags().GetString("connection-id")
		path, _ := cmd.Flags().GetString("mcp-config")
		apply, _ := cmd.Flags().GetBool("apply")
		if strings.TrimSpace(path) == "" {
			return errors.New("the deployment MCP configuration is required")
		}
		status, err := importVaultOAuthCredential(cmd.Context(), &StreamingAPI{mcpConfigPath: path}, id, apply)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", id, status)
		return nil
	},
}

func init() {
	importVaultOAuthCmd.Flags().String("connection-id", "", "Existing Vault OAuth connector ID")
	importVaultOAuthCmd.Flags().String("mcp-config", "", "Legacy deployment MCP catalog with its overlay")
	importVaultOAuthCmd.Flags().Bool("apply", false, "Write sealed destination files; default is read-only")
	_ = importVaultOAuthCmd.MarkFlagRequired("connection-id")
	_ = importVaultOAuthCmd.MarkFlagRequired("mcp-config")
	ServerCmd.AddCommand(importVaultOAuthCmd)
}
