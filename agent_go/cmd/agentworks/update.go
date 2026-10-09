package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// cliVersion identifies this build. Release builds stamp the source commit
// via -X main.cliVersion=<sha>; anything else reports "dev" and always
// accepts an update.
var cliVersion = "dev"

// updateTargetOverride pins the install target in tests. Empty means replace
// the running executable.
var updateTargetOverride string

func versionCommand(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the CLI version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return o.output(map[string]any{"version": cliVersion, "os": runtime.GOOS, "arch": runtime.GOARCH})
		},
	}
}

type cliRelease struct {
	Version string `json:"version"`
	Release string `json:"release"`
}

func updateCommand(o *options) *cobra.Command {
	var check, force bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Self-update the CLI from the connected server",
		Long:  "Download the CLI build published by the connected server, verify its checksum, and replace this executable. Uses the saved login server unless --server is given; no access token needed.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUpdate(o, check, force)
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "Only report whether an update is available")
	cmd.Flags().BoolVar(&force, "force", false, "Reinstall even when versions match")
	return cmd
}

func updateServer(o *options) (string, error) {
	if o.serverURL != "" {
		return o.serverURL, nil
	}
	if env := o.getenv("AGENTWORKS_SERVER"); env != "" {
		return env, nil
	}
	if path, err := o.path(); err == nil {
		if cfg, err := loadSavedServer(path); err == nil && cfg != "" {
			return cfg, nil
		}
	}
	return "", fmt.Errorf("server is required: use --server or agentworks login")
}

func loadSavedServer(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var cfg struct {
		Server string `json:"server"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", err
	}
	return strings.TrimSpace(cfg.Server), nil
}

func updateAssetName() (string, error) {
	var osName string
	switch runtime.GOOS {
	case "darwin":
		osName = "darwin"
	case "linux":
		osName = "linux"
	case "windows":
		osName = "windows"
	default:
		return "", fmt.Errorf("self-update supports macOS, Linux and Windows, not %s", runtime.GOOS)
	}
	switch runtime.GOARCH {
	case "arm64", "amd64":
	default:
		return "", fmt.Errorf("self-update supports arm64 and amd64, not %s", runtime.GOARCH)
	}
	if osName == "windows" {
		return "agentworks-windows-" + runtime.GOARCH + ".exe", nil
	}
	return "agentworks-" + osName + "-" + runtime.GOARCH, nil
}

var updateHTTPClient = &http.Client{Timeout: 5 * time.Minute}

func updateFetch(url string) ([]byte, error) {
	resp, err := updateHTTPClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned HTTP %d for %s", resp.StatusCode, url)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

func runUpdate(o *options, check, force bool) error {
	server, err := updateServer(o)
	if err != nil {
		return err
	}
	asset, err := updateAssetName()
	if err != nil {
		return err
	}
	base := strings.TrimRight(server, "/") + "/api/downloads/cli"
	raw, err := updateFetch(base + "/version.json")
	if err != nil {
		return fmt.Errorf("server does not publish CLI updates: %w", err)
	}
	var release cliRelease
	if err := json.Unmarshal(raw, &release); err != nil || strings.TrimSpace(release.Version) == "" {
		return fmt.Errorf("server published an unreadable CLI version")
	}
	current := cliVersion
	upToDate := !force && current != "dev" && current == release.Version
	if check {
		return o.output(map[string]any{"current": current, "latest": release.Version, "update_available": !upToDate})
	}
	if upToDate {
		return o.output(map[string]any{"current": current, "latest": release.Version, "updated": false})
	}
	target := updateTargetOverride
	if target == "" {
		if target, err = os.Executable(); err != nil {
			return fmt.Errorf("locate current executable: %w", err)
		}
	}
	binary, err := updateFetch(base + "/" + asset)
	if err != nil {
		return err
	}
	sidecar, err := updateFetch(base + "/" + asset + ".sha256")
	if err != nil {
		return err
	}
	fields := strings.Fields(string(sidecar))
	if len(fields) == 0 {
		return fmt.Errorf("server published an unreadable checksum")
	}
	want := strings.ToLower(fields[0])
	sum := sha256.Sum256(binary)
	if hex.EncodeToString(sum[:]) != want {
		return fmt.Errorf("checksum mismatch for %s; refusing to install", asset)
	}
	dir := filepath.Dir(target)
	staged, err := os.CreateTemp(dir, "agentworks-update-*")
	if err != nil {
		return fmt.Errorf("stage update in %s: %w", dir, err)
	}
	stagedPath := staged.Name()
	defer os.Remove(stagedPath)
	if _, err := staged.Write(binary); err != nil {
		staged.Close()
		return fmt.Errorf("stage update: %w", err)
	}
	if err := staged.Close(); err != nil {
		return fmt.Errorf("stage update: %w", err)
	}
	if err := os.Chmod(stagedPath, 0o755); err != nil {
		return fmt.Errorf("stage update: %w", err)
	}
	backup := target + ".bak"
	os.Remove(backup)
	if err := os.Rename(target, backup); err != nil {
		return fmt.Errorf("replace %s: %w (need write permission on the install directory)", target, err)
	}
	if err := os.Rename(stagedPath, target); err != nil {
		os.Rename(backup, target)
		return fmt.Errorf("install update: %w", err)
	}
	if out, err := exec.Command(target, "version").CombinedOutput(); err != nil || !strings.Contains(string(out), release.Version) {
		os.Rename(backup, target)
		return fmt.Errorf("new binary failed to run; previous version restored")
	}
	os.Remove(backup)
	return o.output(map[string]any{"previous": current, "current": release.Version, "path": target, "updated": true})
}
