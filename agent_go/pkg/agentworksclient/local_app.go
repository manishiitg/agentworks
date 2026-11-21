package agentworksclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
)

// LocalAppServer names the AgentWorks Mac app reached through its private
// messaging socket. Port 0 is never a TCP address: New routes this exact server
// to LocalAppSocketPath, so a request for it cannot leave the Mac. The app
// attaches the Mac owner's messaging login to socket requests; no credential is
// saved for it.
const LocalAppServer = "http://localhost:0"

func localAppDir() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "agentworks", "local-app"), nil
}

// LocalAppSocketPath is the Mac app's messaging socket. Only the app creates it.
func LocalAppSocketPath() (string, error) {
	dir, err := localAppDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "agentbridge.sock"), nil
}

// LocalAppConfigPath is separate from browser logins and holds no token.
func LocalAppConfigPath() (string, error) {
	dir, err := localAppDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "connection.json"), nil
}

func localAppSocket() (string, error) {
	path, err := LocalAppSocketPath()
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return "", errors.New("Open the AgentWorks Mac app to connect this chat locally")
	}
	if err = CheckPrivateAccess(path, info); err != nil {
		return "", fmt.Errorf("unsafe AgentWorks app socket: %w", err)
	}
	return path, nil
}

// LoadLocalAppConfig is the connection for a chat with no chosen network: the
// Mac app's socket, while the app is open.
func LoadLocalAppConfig() (Config, string, error) {
	path, err := LocalAppConfigPath()
	if err != nil {
		return Config{}, "", err
	}
	if _, err = localAppSocket(); err != nil {
		return Config{}, path, err
	}
	cfg := Config{Server: LocalAppServer}
	if saved, e := LoadConfig(path); e != nil || saved.Server != cfg.Server || saved.Token != "" || saved.RefreshToken != "" {
		if err = SaveConfig(path, cfg); err != nil {
			return Config{}, path, err
		}
	}
	return cfg, path, nil
}

func localAppTransport() *http.Transport {
	return &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		path, err := localAppSocket()
		if err != nil {
			return nil, err
		}
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}
}
