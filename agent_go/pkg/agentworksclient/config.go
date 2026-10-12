package agentworksclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Config struct {
	Server       string    `json:"server"`
	Token        string    `json:"token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
}

func DefaultConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "agentworks", "config.json"), nil
}

func LoadConfig(path string) (Config, error) {
	var cfg Config
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if !info.Mode().IsRegular() {
		return cfg, errors.New("config must be a regular file, not a symbolic link")
	}
	if info.Mode().Perm()&0077 != 0 {
		return cfg, fmt.Errorf("config is accessible to other users; run chmod 600 %q", path)
	}
	if info.Size() > 1<<20 {
		return cfg, errors.New("config exceeds 1 MiB limit")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("invalid config: %w", err)
	}
	return cfg, nil
}

// SaveConfig atomically replaces credentials with a private file.
func SaveConfig(path string, cfg Config) error {
	if _, err := ValidateServer(cfg.Server); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return errors.New("refusing to replace non-regular config")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".agentworks-config-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// A named login is a separate private connection file under the user's config
// directory, so a second login (for example to another server) never replaces
// the default one. Names are lowercase and short; "default" means the default
// login and cannot be saved as a name.
const DefaultLoginName = "default"

var loginNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)

func ValidateLoginName(name string) error {
	if name == DefaultLoginName {
		return errors.New(`"default" is the default login; choose another name`)
	}
	if !loginNamePattern.MatchString(name) {
		return errors.New("a login name is 1 to 32 lowercase letters, digits, dots, underscores or hyphens, starting with a letter or digit")
	}
	return nil
}

func loginsDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "agentworks", "logins"), nil
}

// LoginPath is the connection file of a named login, or the default login's
// file for "default".
func LoginPath(name string) (string, error) {
	if name == DefaultLoginName {
		return DefaultConfigPath()
	}
	if err := ValidateLoginName(name); err != nil {
		return "", err
	}
	dir, err := loginsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".json"), nil
}

type LoginInfo struct {
	Name   string `json:"name"`
	Server string `json:"server"`
}

// ListLogins returns the default login and every named login with the server it
// signs in to. It never returns credentials or file paths.
func ListLogins() []LoginInfo {
	var out []LoginInfo
	if path, err := DefaultConfigPath(); err == nil {
		if cfg, e := LoadConfig(path); e == nil && cfg.Server != "" {
			out = append(out, LoginInfo{Name: DefaultLoginName, Server: cfg.Server})
		}
	}
	dir, err := loginsDir()
	if err != nil {
		return out
	}
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name(), ".json")
		if entry.IsDir() || name == entry.Name() || ValidateLoginName(name) != nil {
			continue
		}
		if cfg, e := LoadConfig(filepath.Join(dir, entry.Name())); e == nil && cfg.Server != "" {
			out = append(out, LoginInfo{Name: name, Server: cfg.Server})
		}
	}
	return out
}

// SaveLoginIfSameServer saves a named login while holding a lock on it, and
// refuses when a concurrent login has meanwhile saved the same name for a
// different server, so two sign-ins racing for one name cannot overwrite each other.
func SaveLoginIfSameServer(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	unlock, err := lockFileExclusive(lock)
	if err != nil {
		return err
	}
	defer unlock()
	existing, err := LoadConfig(path)
	if err != nil {
		return err
	}
	if existing.Server != "" {
		have, e1 := ValidateServer(existing.Server)
		want, e2 := ValidateServer(cfg.Server)
		if e1 != nil || e2 != nil || have != want {
			return fmt.Errorf("this login name was just saved for %s by another sign-in; choose another name", existing.Server)
		}
	}
	return SaveConfig(path, cfg)
}
