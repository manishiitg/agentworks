package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

// Automatic updates (PLAT-861), OFF by default since 2026-10-18: the helper still checks every few hours and announces a newer
// build, but installs only when the person ran `agentworks update --auto on`. When on it
// installs the build the person's default login server offers, unless the release is of another compatibility epoch (marked breaking), the person turned
// this off, or a chat is being delivered to. Anything that needs the person is said once a day on a prompt.

// autoUpdateInstallsOnWindows stays off until the Windows bridge has run for real users for a while: until then a Windows
// CLI only says an update is available.
var autoUpdateInstallsOnWindows = false

// The steps after the swap; tests replace them.
var (
	autoUpdateStopHelper    = func(ctx context.Context) error { return stopAgentBridge(ctx, "update") }
	autoUpdateRestoreHelper = restoreAgentBridge
	autoUpdatePlugins       = updateInstalledPlugins
	autoUpdateLaptops       = refreshLaptopConnections
)

type autoUpdateState struct {
	// FailedBuild is a build whose automatic install was rolled back; it is not tried again until another is offered.
	FailedBuild string `json:"failed_build,omitempty"`
	// UpdatedNote is said once on the next prompt after the CLI updated itself.
	UpdatedNote string `json:"updated_note,omitempty"`
	// Needs lists what needs the person, per server, with the day it was last said.
	Needs []autoUpdateNeed `json:"needs,omitempty"`
	// LastOffered is the build each server offered at the last check that acted on it. An offer that has not changed since is
	// not installed again, so a manual update (or a server rolling back) is never undone by the schedule.
	LastOffered map[string]string `json:"last_offered,omitempty"`
	// CheckedAt is the last check of the default server.
	CheckedAt time.Time `json:"checked_at,omitempty"`
}

type autoUpdateNeed struct {
	Server string `json:"server"`
	Text   string `json:"text"`
	Said   string `json:"said,omitempty"` // YYYY-MM-DD
}

type autoUpdateConfig struct {
	Enabled *bool `json:"enabled,omitempty"`
}

func autoUpdateConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "agentworks", "auto-update.json"), nil
}

// autoUpdateOn is the person's setting: off unless turned on here (agentworks update --auto on). AGENTWORKS_AUTO_UPDATE=0 forces
// it off. Nothing installs itself by default (owner, 2026-10-18); a newer build is only announced.
func autoUpdateOn() bool {
	if os.Getenv("AGENTWORKS_AUTO_UPDATE") == "0" {
		return false
	}
	path, err := autoUpdateConfigPath()
	if err != nil {
		return false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var cfg autoUpdateConfig
	if json.Unmarshal(raw, &cfg) != nil || cfg.Enabled == nil {
		return false
	}
	return *cfg.Enabled
}

// autoUpdateSetting handles `agentworks update --auto on|off|status`.
func autoUpdateSetting(o *options, value string) error {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "on", "off":
		on := strings.EqualFold(strings.TrimSpace(value), "on")
		path, err := autoUpdateConfigPath()
		if err != nil {
			return err
		}
		if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		raw, _ := json.Marshal(autoUpdateConfig{Enabled: &on})
		if err = os.WriteFile(path, raw, 0o600); err != nil {
			return err
		}
	case "status":
	default:
		return errors.New("use --auto on, --auto off or --auto status")
	}
	state := "on"
	if !autoUpdateOn() {
		state = "off"
	}
	fmt.Fprintf(o.stdout, "Automatic AgentWorks updates are %s.", state)
	if state == "off" {
		fmt.Fprint(o.stdout, " Run agentworks update yourself, or agentworks update --auto on.")
	}
	if cliVersion == "dev" {
		fmt.Fprint(o.stdout, " (This is a development build: it never updates itself.)")
	}
	fmt.Fprintln(o.stdout)
	return nil
}

func autoUpdateStatePath(root string) string { return filepath.Join(root, "update-state.json") }

func loadAutoUpdateState(root string) autoUpdateState {
	var st autoUpdateState
	if raw, err := os.ReadFile(autoUpdateStatePath(root)); err == nil {
		_ = json.Unmarshal(raw, &st)
	}
	return st
}

func saveAutoUpdateState(root string, st autoUpdateState) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return
	}
	raw, _ := json.Marshal(st)
	tmp := autoUpdateStatePath(root) + ".tmp"
	if os.WriteFile(tmp, raw, 0o600) == nil {
		_ = os.Rename(tmp, autoUpdateStatePath(root))
	}
}

// setNeed records (or replaces) what the person has to do for one server; clearNeed drops it.
func (st *autoUpdateState) setNeed(server, text string) {
	for i := range st.Needs {
		if st.Needs[i].Server == server {
			if st.Needs[i].Text != text {
				st.Needs[i].Text, st.Needs[i].Said = text, ""
			}
			return
		}
	}
	st.Needs = append(st.Needs, autoUpdateNeed{Server: server, Text: text})
}

func (st *autoUpdateState) clearNeed(server string) {
	kept := st.Needs[:0]
	for _, n := range st.Needs {
		if n.Server != server {
			kept = append(kept, n)
		}
	}
	st.Needs = kept
}

// autoUpdateHookLine is what the prompt hook adds to a chat's context: that the CLI updated itself (once), and each thing
// that needs the person at most once a day. It never asks the agent to run anything, and never approves anything.
func autoUpdateHookLine(root string, now time.Time) string {
	st := loadAutoUpdateState(root)
	today := now.Format("2006-01-02")
	var lines []string
	changed := false
	if st.UpdatedNote != "" {
		lines = append(lines, st.UpdatedNote)
		updateLog(root, "told the chat: %s", boundedUpdateLine(st.UpdatedNote))
		st.UpdatedNote, changed = "", true
	}
	for i := range st.Needs {
		if st.Needs[i].Said != today {
			lines = append(lines, st.Needs[i].Text)
			updateLog(root, "told the chat: %s", boundedUpdateLine(st.Needs[i].Text))
			st.Needs[i].Said, changed = today, true
		}
	}
	if changed {
		saveAutoUpdateState(root, st)
	}
	return strings.Join(lines, "\n")
}

// pendingUpdateNotice is what the update check left for the person, without marking it as said.
func pendingUpdateNotice(root string) string {
	st := loadAutoUpdateState(root)
	var lines []string
	for _, n := range st.Needs {
		if strings.TrimSpace(n.Text) != "" {
			lines = append(lines, n.Text)
		}
	}
	return strings.Join(lines, "\n")
}

func boundedUpdateLine(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > 160 {
		text = text[:160] + "..."
	}
	return text
}

// updateLog writes one [UPDATE] line to the helper's log (the check runs in its own process).
func updateLog(root, format string, args ...any) {
	f, err := os.OpenFile(filepath.Join(root, "bridge.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s [UPDATE] %s\n", time.Now().Format("2006/01/02 15:04:05"), fmt.Sprintf(format, args...))
}

// builtNotOlderThan reports whether this CLI was built at or after the given time (unix seconds); unknown on either side is no.
func builtNotOlderThan(offered int64) bool {
	mine, err := strconv.ParseInt(cliBuiltAt, 10, 64)
	return err == nil && offered > 0 && mine >= offered
}

func offeredID(r cliRelease) string { return firstNonEmpty(r.CLIBuild, r.Version) }

func shortHost(server string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(strings.TrimRight(server, "/"), "https://"), "http://")
	return s
}

// runAutoUpdate is one check: the default login's server may install; any other server the person is attached to only
// produces a notice, and never a downgrade.
func runAutoUpdate(o *options) error {
	// The app owns its bundled CLI version, even if an old standalone CLI had
	// opted into automatic updates. Never mutate a signed application bundle.
	if bundledDesktop() != "" {
		return nil
	}
	root, err := agentworksclient.DefaultLocalBridgeRoot()
	if err != nil {
		return err
	}
	if cliVersion == "dev" {
		return nil
	}
	st := loadAutoUpdateState(root)
	st.CheckedAt = time.Now()
	defer func() { saveAutoUpdateState(root, st) }()
	ctx := context.Background()

	server, serverErr := updateServer(o)
	on := autoUpdateOn()
	if serverErr == nil {
		release, ok := latestCLIRelease(ctx, server)
		if ok {
			updateLog(root, "check server=%s running=%s offered=%s auto=%v", shortHost(server), shortVersion(firstNonEmpty(cliBuild, cliVersion)), shortVersion(offeredID(release)), on)
		} else {
			updateLog(root, "check server=%s: no readable offer, will try again", shortHost(server))
		}
		if st.LastOffered == nil {
			st.LastOffered = map[string]string{}
		}
		switch {
		case !ok:
			// unreachable or unreadable: say nothing, try again next time
		case cliIsCurrent(cliVersion, cliBuild, release.Version, release.CLIBuild):
			st.clearNeed(server)
			st.FailedBuild = ""
			st.LastOffered[server] = offeredID(release)
		case release.CompatEpoch != cliCompatEpoch:
			updateLog(root, "skip: the offered release is a different compatibility epoch (%q, this CLI %q)", release.CompatEpoch, cliCompatEpoch)
			reason := strings.TrimSpace(release.BreakingReason)
			if reason == "" {
				reason = "this release is not compatible with older versions"
			}
			st.setNeed(server, "AgentWorks update needs you: "+reason+". Run agentworks update when ready.")
		case !on:
			updateLog(root, "skip: automatic updates are off")
			st.setNeed(server, "A newer AgentWorks CLI is available (automatic updates are off): run agentworks update, or ask the person to.")
		case runtime.GOOS == "windows" && !autoUpdateInstallsOnWindows:
			updateLog(root, "skip: Windows only notifies")
			st.setNeed(server, "AgentWorks update available: ask the person to run agentworks update.")
		case offeredID(release) == st.FailedBuild:
			updateLog(root, "skip: this build was rolled back before")
			st.setNeed(server, "AgentWorks could not install the offered update automatically (it was rolled back): ask the person to run agentworks update.")
		case st.LastOffered[server] == "" && builtNotOlderThan(release.BuiltAt):
			// First check on this machine: a CLI built after the offered one (installed by hand from elsewhere) is not moved back.
			updateLog(root, "skip: this CLI was built at or after the offered one")
			st.LastOffered[server] = offeredID(release)
		case st.LastOffered[server] == offeredID(release):
			// The offer is the one already acted on, yet this build differs: the person updated by hand or the server went
			// back. Leave it alone.
			updateLog(root, "skip: the offer has not changed since the last check")
		default:
			st.clearNeed(server)
			updateLog(root, "install: build %s", shortVersion(offeredID(release)))
			failed, note := autoUpdateInstall(o, server, release)
			if failed != "" {
				updateLog(root, "failed and rolled back where needed: %s", failed)
				st.FailedBuild = offeredID(release)
				st.setNeed(server, "AgentWorks could not update itself and rolled back ("+failed+"): ask the person to run agentworks update.")
			} else {
				updateLog(root, "installed build %s and restarted the helper", shortVersion(offeredID(release)))
				st.FailedBuild = ""
				st.LastOffered[server] = offeredID(release)
				st.UpdatedNote = note
			}
		}
	}
	// Other servers the chats are attached to: a notice, never an install.
	if root != "" {
		if dirs, e := agentworksclient.CallLocalBridge(ctx, root, agentworksclient.LocalBridgeRequest{Action: "directories"}); e == nil {
			servers := map[string]bool{}
			for _, d := range dirs.Directories {
				if d.Server != "" && d.Server != server {
					servers[d.Server] = true
				}
			}
			names := make([]string, 0, len(servers))
			for s := range servers {
				names = append(names, s)
			}
			sort.Strings(names)
			for _, other := range names {
				release, ok := latestCLIRelease(ctx, other)
				switch {
				case !ok, cliIsCurrent(cliVersion, cliBuild, release.Version, release.CLIBuild):
					st.clearNeed(other)
				case release.CompatEpoch != cliCompatEpoch:
					reason := firstNonEmpty(strings.TrimSpace(release.BreakingReason), "this release is not compatible with older versions")
					st.setNeed(other, "AgentWorks at "+shortHost(other)+" needs a different CLI: "+reason+". Run agentworks update --server "+other+" when ready (it also changes the CLI for other servers).")
				default:
					st.setNeed(other, "AgentWorks at "+shortHost(other)+" offers another CLI build than this one; it is not installed automatically from a server that is not your default login.")
				}
			}
		}
	}
	return nil
}

// autoUpdateInstall installs the offered release the way `agentworks update` does, keeps the previous executable until the
// restarted helper answers, and rolls back when it does not. It returns a short reason on failure ("" on success) and, on
// success, the one-time note for the next prompt (with the plugin steps that need the person).
func autoUpdateInstall(o *options, server string, release cliRelease) (failure, note string) {
	asset, err := updateAssetName()
	if err != nil {
		return err.Error(), ""
	}
	target := updateTargetOverride
	if target == "" {
		if target, err = os.Executable(); err != nil {
			return "cannot locate the running program", ""
		}
	}
	base := strings.TrimRight(server, "/") + "/api/downloads/cli"
	backup, err := installUpdate(base, asset, target, release, true, nil)
	if err != nil {
		return err.Error(), ""
	}
	rollback := func(reason string) (string, string) {
		_ = autoUpdateStopHelper(context.Background())
		if renameErr := os.Rename(backup, target); renameErr == nil {
			_ = autoUpdateRestoreHelper(context.Background(), o, target)
		}
		return reason, ""
	}
	if err = autoUpdateStopHelper(context.Background()); err != nil {
		return rollback("the messaging helper did not stop")
	}
	restoreCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err = autoUpdateRestoreHelper(restoreCtx, o, target); err != nil {
		return rollback("the new version's messaging helper did not start")
	}
	_ = os.Remove(backup)
	var plugins bytes.Buffer
	pluginOptions := *o
	pluginOptions.stderr = &plugins
	autoUpdatePlugins(context.Background(), &pluginOptions, false)
	note = "AgentWorks updated itself to build " + shortVersion(firstNonEmpty(release.CLIBuild, release.Version)) + "."
	if err := autoUpdateLaptops(context.Background(), o, target, release.CLIBuild); err != nil {
		note += " A computer connection could not reconnect; check AgentWorks status. Saved folders and permissions are unchanged."
	}
	// Fixed wording only: what the plugin step printed (it can contain a command) is not passed into a chat's context.
	if extra := strings.ToLower(plugins.String()); strings.Contains(extra, "muse") {
		note += " The Muse plugin was updated; the person must approve it and start a new Muse chat."
	} else if strings.TrimSpace(extra) != "" {
		note += " Plugins were refreshed."
	}
	return "", note
}

// autoUpdateHelperCallback is what the local helper calls on its schedule: run the check in a new process, because
// an update replaces the helper itself. The check also runs when automatic updates are off, to leave the notice.
func autoUpdateHelperCallback(ctx context.Context) {
	if cliVersion == "dev" {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	child := exec.Command(exe, "update", "--auto-run")
	detach(child)
	child.Stdin, child.Stdout, child.Stderr = nil, nil, nil
	if child.Start() == nil {
		go func() { _ = child.Wait() }()
	}
}
