package main

import (
	"context"
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

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/spf13/cobra"
)

// cliVersion identifies this build. Release builds stamp the source commit
// via -X main.cliVersion=<sha>; anything else reports "dev" and always
// accepts an update.
var cliVersion = "dev"

// cliBuild identifies the source the CLI is built from (its own packages and every package of this repository it imports; stamped
// by the release scripts via -X main.cliBuild=<id>). Unlike cliVersion it changes only when the CLI's source does, so a deploy that
// touches only server code does not make every installed CLI "out of date".
var cliBuild = "dev"

// cliCompatEpoch is the compatibility epoch this CLI was built with (-X main.cliCompatEpoch=..., from deploy/cli-release.json).
var cliCompatEpoch = ""

// cliBuiltAt is when this CLI was built, unix seconds as text (-X main.cliBuiltAt=...); empty for development builds.
var cliBuiltAt = ""

// cliIsCurrent reports whether a CLI (version, build) is the one the server offers: by build when both sides have one, else by the
// whole-repository revision.
func cliIsCurrent(version, build, latestVersion, latestBuild string) bool {
	if latestBuild != "" && build != "" && build != "dev" {
		return build == latestBuild
	}
	return version == latestVersion
}

// updateTargetOverride pins the install target in tests. Empty means replace
// the running executable.
var updateTargetOverride string

func versionCommand(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the CLI version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := map[string]any{"version": cliVersion, "os": runtime.GOOS, "arch": runtime.GOARCH}
			// What the background update check left for the person (an update that needs them, or one that only
			// notifies, as on Windows) shows here too, not only in a chat: not everyone has a chat open.
			if root, err := agentworksclient.DefaultLocalBridgeRoot(); err == nil {
				if notice := pendingUpdateNotice(root); notice != "" {
					out["update_notice"] = notice
				}
			}
			return o.output(out)
		},
	}
}

type cliRelease struct {
	Version  string `json:"version"`
	CLIBuild string `json:"cli_build"`
	Release  string `json:"release"`
	// CompatEpoch names the generation of releases that work with each other. A client installs automatically only a
	// release of its own epoch; bumping it at release time is the act of marking a release breaking, and later releases keep
	// it, so clients on the new one update by themselves again. BreakingReason is the one line shown to the person.
	CompatEpoch string `json:"compat_epoch,omitempty"`
	// BuiltAt is when the server's CLI was built (unix seconds); only used to avoid moving a newer CLI back.
	BuiltAt        int64  `json:"built_at,omitempty"`
	BreakingReason string `json:"breaking_reason,omitempty"`
}

func updateCommand(o *options) *cobra.Command {
	var check, force, switchSource, keepSource, autoRun bool
	var auto string
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Self-update the CLI from the connected server",
		Long:  "Download the CLI build published by the connected server, verify its checksum, and replace this executable. Uses the saved login server unless --server is given; no access token needed.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if auto != "" {
				return autoUpdateSetting(o, auto)
			}
			if autoRun {
				return runAutoUpdate(o)
			}
			// The plugins move to the small plugins repository by default; --keep-plugin-source leaves them where they are.
			return runUpdate(o, check, force, switchSource || !keepSource)
		},
	}
	cmd.Flags().StringVar(&auto, "auto", "", "Turn automatic updates on or off, or show their state: on, off or status (off by default)")
	cmd.Flags().BoolVar(&autoRun, "auto-run", false, "Run one automatic update check now (what the background helper does)")
	_ = cmd.Flags().MarkHidden("auto-run")
	cmd.Flags().BoolVar(&check, "check", false, "Only report whether an update is available")
	cmd.Flags().BoolVar(&force, "force", false, "Reinstall even when versions match")
	cmd.Flags().BoolVar(&switchSource, "switch-plugin-source", false, "Move the AgentWorks coding-CLI plugins to the small plugins repository (now the default, so this flag changes nothing)")
	_ = cmd.Flags().MarkHidden("switch-plugin-source")
	cmd.Flags().BoolVar(&keepSource, "keep-plugin-source", false, "Leave the AgentWorks coding-CLI plugins on the large repository instead of moving them to the small plugins repository")
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

func runUpdate(o *options, check, force, switchSource bool) (resultErr error) {
	if bundledDesktop() != "" && !check {
		return fmt.Errorf("this CLI is bundled with AgentWorks; open the app and choose Check for Updates to update both together")
	}
	server, err := updateServer(o)
	if err != nil {
		return err
	}
	asset, err := updateAssetName()
	if err != nil {
		return err
	}
	base := strings.TrimRight(server, "/") + "/api/downloads/cli"
	var progress io.Writer
	if !check {
		progress = o.stderr
	}
	updateStep(progress, "Checking for an AgentWorks update...")
	raw, err := updateFetch(base + "/version.json")
	if err != nil {
		return fmt.Errorf("server does not publish CLI updates: %w", err)
	}
	var release cliRelease
	if err := json.Unmarshal(raw, &release); err != nil || strings.TrimSpace(release.Version) == "" {
		return fmt.Errorf("server published an unreadable CLI version")
	}
	current := cliVersion
	upToDate := !force && current != "dev" && cliIsCurrent(current, cliBuild, release.Version, release.CLIBuild)
	if check {
		return o.output(map[string]any{"current": current, "latest": release.Version, "update_available": !upToDate})
	}
	if upToDate {
		defer func() { reportUpdateMessaging(o, resultErr) }()
		updateStep(progress, "AgentWorks is already up to date.")
		// Restore existing chat connections if needed. Startup discovery belongs
		// to that helper; a healthy helper must not rescan old desktop history.
		if err = restoreAgentBridge(context.Background(), o, ""); err != nil {
			return err
		}
		target, err := os.Executable()
		if err != nil {
			return err
		}
		if err = refreshLaptopConnections(context.Background(), o, target, release.CLIBuild); err != nil {
			return err
		}
		return o.output(map[string]any{"current": current, "latest": release.Version, "updated": false})
	}
	target := updateTargetOverride
	if target == "" {
		if target, err = os.Executable(); err != nil {
			return fmt.Errorf("locate current executable: %w", err)
		}
	}
	updateStep(progress, "Downloading AgentWorks...")
	if _, err = installUpdate(base, asset, target, release, false, progress); err != nil {
		return err
	}
	defer func() { reportUpdateMessaging(o, resultErr) }()
	updateStep(progress, "Reconnecting AgentWorks...")
	if err = stopAgentBridge(context.Background(), "update"); err != nil {
		return err
	}
	if err = restoreAgentBridge(context.Background(), o, target); err != nil {
		return err
	}
	if err = refreshLaptopConnections(context.Background(), o, target, release.CLIBuild); err != nil {
		return fmt.Errorf("CLI updated; computer connection needs attention: %w", err)
	}
	updateStep(progress, "Updating installed plugins...")
	updateInstalledPlugins(context.Background(), o, switchSource)
	updateStep(progress, "AgentWorks update complete.")
	return o.output(map[string]any{"previous": current, "current": release.Version, "path": target, "updated": true})
}

// installUpdate downloads the asset, checks its checksum, swaps it in for the executable and runs the new one's
// `version` as a self-test (restoring the old one if that fails). With keepBackup the previous executable stays at
// the returned path so a caller can roll back.
func installUpdate(base, asset, target string, release cliRelease, keepBackup bool, progress io.Writer) (string, error) {
	compressed, err := downloadUpdate(base+"/"+asset+".gz", progress, time.Minute)
	if err != nil {
		return "", err
	}
	binary, err := unpackUpdate(compressed)
	if err != nil {
		return "", err
	}
	updateStep(progress, "Verifying the download...")
	sidecar, err := updateFetch(base + "/" + asset + ".sha256")
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(sidecar))
	if len(fields) == 0 {
		return "", fmt.Errorf("server published an unreadable checksum")
	}
	want := strings.ToLower(fields[0])
	sum := sha256.Sum256(binary)
	if hex.EncodeToString(sum[:]) != want {
		return "", fmt.Errorf("checksum mismatch for %s; refusing to install", asset)
	}
	updateStep(progress, "Installing AgentWorks...")
	dir := filepath.Dir(target)
	staged, err := os.CreateTemp(dir, "agentworks-update-*")
	if err != nil {
		return "", fmt.Errorf("stage update in %s: %w", dir, err)
	}
	stagedPath := staged.Name()
	defer os.Remove(stagedPath)
	if _, err := staged.Write(binary); err != nil {
		staged.Close()
		return "", fmt.Errorf("stage update: %w", err)
	}
	if err := staged.Close(); err != nil {
		return "", fmt.Errorf("stage update: %w", err)
	}
	if err := os.Chmod(stagedPath, 0o755); err != nil {
		return "", fmt.Errorf("stage update: %w", err)
	}
	backup := target + ".bak"
	os.Remove(backup) // a stale one from an earlier update
	if err := os.Rename(target, backup); err != nil {
		return "", fmt.Errorf("replace %s: %w (need write permission on the install directory)", target, err)
	}
	if err := os.Rename(stagedPath, target); err != nil {
		os.Rename(backup, target)
		return "", fmt.Errorf("install update: %w", err)
	}
	if out, err := exec.Command(target, "version").CombinedOutput(); err != nil || !strings.Contains(string(out), release.Version) {
		os.Rename(backup, target)
		return "", fmt.Errorf("new binary failed to run; previous version restored")
	}
	if !keepBackup {
		os.Remove(backup)
	}
	return target + ".bak", nil
}
