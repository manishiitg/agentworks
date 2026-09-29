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

// Platform MCP credentials are sealed at rest. Coding CLIs' native reads run
// outside the shell sandbox, so nothing they can open may hold a usable
// credential in plain text:
//
//   - OAuth tokens and client registrations under the mcpagent tokens root
//     (<tokens root>/_platform/<server>.json and .client.json, and older
//     per-user or catalog paths there) are sealed, each bound to its path.
//   - Client secrets never stay in the MCP config overlay: they move to the
//     server's sealed client file, and the overlay keeps only
//     oauth.client_secret_file, a reference mcpagent reads (and unseals) when
//     it builds the OAuth client.

// platformClientSealer seals every credential file under the tokens root.
type platformClientSealer struct{}

func (platformClientSealer) Handles(path string) bool {
	rel, err := filepath.Rel(mcpagentTokensRoot(), filepath.Clean(path))
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) && strings.HasSuffix(rel, ".json")
}

func platformClientAAD(path string) []byte {
	rel, _ := filepath.Rel(mcpagentTokensRoot(), filepath.Clean(path))
	return []byte("platform-mcp-credential\x00" + filepath.ToSlash(rel))
}

func (platformClientSealer) Seal(path string, plaintext []byte) ([]byte, error) {
	sealed, err := encryptSecretValueWithAAD(string(plaintext), platformClientAAD(path))
	return []byte(sealed), err
}

func (platformClientSealer) Open(path string, sealed []byte) ([]byte, error) {
	trimmed := strings.TrimSpace(string(sealed))
	// A credential written before sealing is plain JSON: still readable,
	// and sealed at start (sealPlainPlatformCredentials) or on its next write.
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

// sealPlainPlatformCredentials seals every credential file under the tokens
// root still in plain JSON (written before sealing), once at start.
func sealPlainPlatformCredentials() (int, error) {
	root := mcpagentTokensRoot()
	sealed := 0
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if entry.IsDir() || !entry.Type().IsRegular() || !(platformClientSealer{}).Handles(path) {
			return nil
		}
		raw, err := os.ReadFile(path) //nolint:gosec // G304: files under the tokens root
		if err != nil {
			return err
		}
		if !strings.HasPrefix(strings.TrimSpace(string(raw)), "{") {
			return nil // already sealed
		}
		if err := oauth.WriteTokenFile(path, raw); err != nil {
			return fmt.Errorf("seal %s: %w", path, err)
		}
		sealed++
		return nil
	})
	return sealed, err
}
