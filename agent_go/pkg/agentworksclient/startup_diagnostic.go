package agentworksclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"time"
)

// StartupDiagnostic contains only machine/build metadata. Never add error text,
// stderr, command arguments, local paths or login contents to this wire record.
type StartupDiagnostic struct {
	ID               string    `json:"id"`
	At               time.Time `json:"at"`
	Version          string    `json:"version"`
	Build            string    `json:"build"`
	OS               string    `json:"os"`
	Arch             string    `json:"arch"`
	Cause            string    `json:"cause"`
	ExecutableSHA256 string    `json:"executable_sha256"`
}

type savedStartupDiagnostic struct {
	Server     string            `json:"server"`
	ConfigPath string            `json:"config_path"`
	Diagnostic StartupDiagnostic `json:"diagnostic"`
}

func startupDiagnosticPath(root, configPath string) string {
	key := sha256.Sum256([]byte(filepath.Clean(configPath)))
	return filepath.Join(root, fmt.Sprintf("startup-failure-%x.json", key[:12]))
}

// RecordStartupFailure retains the most recent failure for this saved login.
// Raw stderr remains in startup.log. A failure to record must not replace the
// original startup error or prevent the caller from reconnecting later.
func RecordStartupFailure(root, configPath, version, build, cause string) {
	cfg, err := LoadConfig(configPath)
	if err != nil || cfg.Server == "" {
		return
	}
	server, err := ValidateServer(cfg.Server)
	if err != nil {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	f, err := os.Open(exe)
	if err != nil {
		return
	}
	hash := sha256.New()
	_, err = io.Copy(hash, f)
	f.Close()
	if err != nil {
		return
	}
	// Release metadata may be absent when build-id discovery fails. Preserve
	// the startup failure instead of losing the diagnostic for that build.
	if version == "" {
		version = "unknown"
	}
	if build == "" {
		build = "unknown"
	}
	d := StartupDiagnostic{At: time.Now().UTC(), Version: version, Build: build, OS: runtime.GOOS, Arch: runtime.GOARCH, Cause: cause, ExecutableSHA256: hex.EncodeToString(hash.Sum(nil))}
	id := sha256.Sum256([]byte(d.At.Format(time.RFC3339Nano) + d.ExecutableSHA256 + cause))
	d.ID = hex.EncodeToString(id[:])
	if !d.Valid() {
		return
	}
	path := startupDiagnosticPath(root, configPath)
	unlock, err := liveFileLock(path + ".lock")
	if err != nil {
		return
	}
	defer unlock()
	if atomicLiveJSON(path, savedStartupDiagnostic{server, configPath, d}) == nil {
		log.Printf("[BRIDGE-STARTUP] recorded id=%s cause=%s version=%s build=%s executable_sha256=%s", d.ID, cause, version, build, d.ExecutableSHA256)
	}
}

// Existing connection/metadata refreshes flush this record after authentication.
// The originating saved login and server must match; joined networks do not
// inherit it. The file lock prevents concurrent chats from posting it twice.
func (c *Client) reportStartupFailure(ctx context.Context, root, configPath string, a AgentAttachment) {
	if configPath == "" {
		return
	}
	path := startupDiagnosticPath(root, configPath)
	if _, err := os.Lstat(path); err != nil {
		return
	}
	unlock, err := liveFileLock(path + ".lock")
	if err != nil {
		return
	}
	defer unlock()
	var saved savedStartupDiagnostic
	if readLiveJSON(path, &saved) != nil || saved.ConfigPath != configPath || saved.Server != c.baseURL {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, err = c.request(callCtx, http.MethodPost, "/api/external/v1/agents/attachment", map[string]any{"action": "startup_failure", "agent_name": a.AgentName, "attachment_id": a.ID, "diagnostic": saved.Diagnostic}, true)
	if err != nil {
		return
	}
	if os.Remove(path) == nil {
		log.Printf("[BRIDGE-STARTUP] reported id=%s attachment=%s", saved.Diagnostic.ID, a.ID)
	}
}

// Valid is deliberately a closed schema: diagnostics cannot carry stderr or
// arbitrary error strings into the server log.
func (d StartupDiagnostic) Valid() bool {
	hexValue := func(s string, min int) bool {
		if len(s) < min || len(s) > 64 {
			return false
		}
		for _, r := range s {
			if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
				return false
			}
		}
		return true
	}
	return hexValue(d.ID, 64) && hexValue(d.ExecutableSHA256, 64) &&
		(d.Version == "unknown" || d.Version == "dev" || hexValue(d.Version, 7)) && (d.Build == "unknown" || d.Build == "dev" || hexValue(d.Build, 7)) &&
		!d.At.IsZero() && !d.At.After(time.Now().Add(time.Minute)) &&
		slices.Contains([]string{"windows", "darwin", "linux"}, d.OS) &&
		slices.Contains([]string{"amd64", "arm64"}, d.Arch) &&
		slices.Contains([]string{"spawn_failed", "early_exit", "timeout"}, d.Cause)
}
