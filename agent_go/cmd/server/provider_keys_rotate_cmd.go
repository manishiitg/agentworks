package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var rotateProviderKeysCmd = &cobra.Command{
	Use:   "rotate-provider-keys-auth-secret",
	Short: "Rotate workspace provider key encryption to a new AUTH_SECRET",
	Long: `Decrypt the workspace provider key file with the current or provided AUTH_SECRET,
then re-encrypt it with a new AUTH_SECRET. Optionally updates agent_go/.env locally.`,
	RunE: runRotateProviderKeysAuthSecret,
}

func init() {
	rotateProviderKeysCmd.Flags().String("file", defaultProviderKeysRotationPath(), "Path to config/provider-api-keys.json")
	rotateProviderKeysCmd.Flags().String("env-file", "agent_go/.env", "Local env file to update with AUTH_SECRET")
	rotateProviderKeysCmd.Flags().String("old-auth-secret", "", "Old AUTH_SECRET to decrypt existing provider keys (defaults to current runtime secret)")
	rotateProviderKeysCmd.Flags().String("new-auth-secret", "", "New AUTH_SECRET to use for re-encryption")
	rotateProviderKeysCmd.Flags().Bool("generate-new-auth-secret", false, "Generate a new random AUTH_SECRET")
	rotateProviderKeysCmd.Flags().Bool("write-env", true, "Write the new AUTH_SECRET into the env file")
	rotateProviderKeysCmd.Flags().Bool("backup", true, "Create a timestamped backup of the encrypted provider key file before rotation")
}

func defaultProviderKeysRotationPath() string {
	if root := os.Getenv("WORKSPACE_DOCS_PATH"); root != "" {
		return filepath.Join(root, "config", "provider-api-keys.json")
	}
	return filepath.Join("workspace-docs", "config", "provider-api-keys.json")
}

func runRotateProviderKeysAuthSecret(cmd *cobra.Command, args []string) error {
	filePath, _ := cmd.Flags().GetString("file")
	envFilePath, _ := cmd.Flags().GetString("env-file")
	oldAuthSecret, _ := cmd.Flags().GetString("old-auth-secret")
	newAuthSecret, _ := cmd.Flags().GetString("new-auth-secret")
	generateNewAuthSecret, _ := cmd.Flags().GetBool("generate-new-auth-secret")
	writeEnv, _ := cmd.Flags().GetBool("write-env")
	createBackup, _ := cmd.Flags().GetBool("backup")

	if generateNewAuthSecret {
		if newAuthSecret != "" {
			return fmt.Errorf("--new-auth-secret and --generate-new-auth-secret cannot be used together")
		}
		generated, err := generateAuthSecretHex(32)
		if err != nil {
			return fmt.Errorf("failed to generate auth secret: %w", err)
		}
		newAuthSecret = generated
	}

	if strings.TrimSpace(newAuthSecret) == "" {
		return fmt.Errorf("new AUTH_SECRET is required; use --new-auth-secret or --generate-new-auth-secret")
	}

	if strings.TrimSpace(oldAuthSecret) == "" {
		oldAuthSecret = string(GetAuthSecret())
	}

	if err := rekeyProviderKeysFile(filePath, []byte(oldAuthSecret), []byte(newAuthSecret), createBackup, cmd.OutOrStdout(), false); err != nil {
		return err
	}

	if writeEnv {
		if err := upsertEnvVar(envFilePath, "AUTH_SECRET", newAuthSecret); err != nil {
			return fmt.Errorf("failed to update %s: %w", envFilePath, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Updated %s with a new AUTH_SECRET\n", envFilePath)
	}

	fmt.Fprintln(cmd.OutOrStdout(), "Restart the agent server so it loads the new AUTH_SECRET before reading provider keys again.")
	return nil
}

func generateAuthSecretHex(numBytes int) (string, error) {
	buf := make([]byte, numBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func upsertEnvVar(path, key, value string) error {
	contentBytes, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	content := ""
	if err == nil {
		content = string(contentBytes)
	}

	lines := []string{}
	if content != "" {
		lines = strings.Split(content, "\n")
	}

	updated := false
	for i, line := range lines {
		if strings.HasPrefix(line, key+"=") {
			lines[i] = fmt.Sprintf("%s=%s", key, value)
			updated = true
			break
		}
	}

	if !updated {
		lines = append(lines, fmt.Sprintf("%s=%s", key, value))
	}

	output := strings.Join(lines, "\n")
	if !strings.HasSuffix(output, "\n") {
		output += "\n"
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(output), 0600)
}
