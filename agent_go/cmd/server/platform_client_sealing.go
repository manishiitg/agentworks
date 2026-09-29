package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/oauth"
)

// Platform OAuth client secrets are kept sealed in the platform client file
// (<tokens root>/_platform/<server>.client.json), never in the MCP config
// overlay: coding CLIs' native reads run outside the shell sandbox and can
// read that overlay. The overlay keeps only oauth.client_secret_file, a
// reference mcpagent reads (and unseals) when it builds the OAuth client.

// platformClientSealer seals platform client files, bound to their path.
type platformClientSealer struct{}

func platformClientRoot() string {
	return filepath.Join(mcpagentTokensRoot(), platformMCPTokenUserID)
}

func (platformClientSealer) Handles(path string) bool {
	rel, err := filepath.Rel(platformClientRoot(), filepath.Clean(path))
	return err == nil && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) &&
		!strings.Contains(rel, string(filepath.Separator)) && strings.HasSuffix(rel, ".client.json")
}

func platformClientAAD(path string) []byte {
	return []byte("platform-mcp-client\x00" + filepath.Base(filepath.Clean(path)))
}

func (platformClientSealer) Seal(path string, plaintext []byte) ([]byte, error) {
	sealed, err := encryptSecretValueWithAAD(string(plaintext), platformClientAAD(path))
	return []byte(sealed), err
}

func (platformClientSealer) Open(path string, sealed []byte) ([]byte, error) {
	trimmed := strings.TrimSpace(string(sealed))
	// A registration cached before sealing is plain JSON: still readable,
	// sealed the next time it is written.
	if strings.HasPrefix(trimmed, "{") {
		return []byte(trimmed), nil
	}
	plaintext, err := decryptSecretValueWithAAD(trimmed, platformClientAAD(path))
	return []byte(plaintext), err
}

// sealPlatformClientSecret moves an inline client secret into the server's
// sealed client file and leaves the reference. A config without an inline
// secret is unchanged.
func sealPlatformClientSecret(serverName string, cfg *oauth.OAuthConfig) error {
	if cfg == nil || cfg.ClientSecret == "" {
		return nil
	}
	path := expandPath(getUserClientFilePath(platformMCPTokenUserID, serverName))
	record := registeredClient{}
	if data, err := oauth.ReadTokenFile(path); err == nil {
		_ = json.Unmarshal(data, &record)
	}
	record.ClientID, record.ClientSecret = cfg.ClientID, cfg.ClientSecret
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err := oauth.WriteTokenFile(path, data); err != nil {
		return fmt.Errorf("seal client secret for %s: %w", serverName, err)
	}
	cfg.ClientSecret, cfg.ClientSecretFile = "", path
	return nil
}

// isPlatformClientSecretFile reports whether a config's secret reference
// points at that server's own platform client file, the only reference the
// platform ever writes.
func isPlatformClientSecretFile(serverName, path string) bool {
	return path == "" || filepath.Clean(path) == filepath.Clean(expandPath(getUserClientFilePath(platformMCPTokenUserID, serverName)))
}

// migratePlatformClientSecrets seals every inline client secret already in
// the overlay (written before secrets moved out), once at start.
func (api *StreamingAPI) migratePlatformClientSecrets() error {
	path := api.getUserConfigPath()
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	overlay, err := mcpclient.LoadConfig(path, api.logger)
	if err != nil {
		return err
	}
	moved := 0
	for name, server := range overlay.MCPServers {
		if server.OAuth == nil || server.OAuth.ClientSecret == "" {
			continue
		}
		copied := *server.OAuth
		if err := sealPlatformClientSecret(name, &copied); err != nil {
			return err
		}
		server.OAuth = &copied
		overlay.MCPServers[name] = server
		moved++
	}
	if moved == 0 {
		return nil
	}
	if err := mcpclient.SaveConfig(path, overlay); err != nil {
		return err
	}
	log.Printf("[MCP] sealed %d platform OAuth client secret(s) out of %s", moved, path)
	return os.Chmod(path, 0o600)
}
