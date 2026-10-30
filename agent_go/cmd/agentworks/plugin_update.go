package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// pluginUpdate is one coding CLI's AgentWorks plugin refresh after the CLI itself
// was updated. Auto commands are the provider's own documented update commands;
// they never approve trust or attach a chat. Manual is the one line the person
// runs when the app cannot be updated from the command line.
type pluginUpdate struct {
	Provider string
	Auto     [][]string
	Manual   string
	Disabled bool // left disabled on purpose: only its marketplace is refreshed
}

// codexAppBundleCLI is the command line the Codex desktop app ships inside the
// ChatGPT app on macOS (layout read from the installed app; no other layout is
// assumed). The person may have no codex on PATH and still use the app.
var codexGOOS = runtime.GOOS

var codexAppBundleCLI = "/Applications/ChatGPT.app/Contents/Resources/codex-cli/CodexCLI.app/Contents/MacOS/codex"

// codexExecutable returns what to run for Codex: "codex" when it is on PATH, else
// the app's own verified command line on macOS, else "" (Codex is not installed).
func codexExecutable(onPath func(string) bool) string {
	if onPath("codex") {
		return "codex"
	}
	if codexGOOS == "darwin" {
		if info, err := os.Stat(codexAppBundleCLI); err == nil && info.Mode().IsRegular() && info.Mode()&0111 != 0 {
			return codexAppBundleCLI
		}
	}
	return ""
}

// codexHomeDir is the profile Codex itself uses: CODEX_HOME when set, else ~/.codex.
// A relative CODEX_HOME depends on the folder the command runs in, so it is
// refused ("" and codexHomeError) rather than silently reading the default profile.
func codexHomeDir(home string) string {
	if v := os.Getenv("CODEX_HOME"); v != "" {
		if filepath.IsAbs(v) {
			return v
		}
		return ""
	}
	return filepath.Join(home, ".codex")
}

func codexHomeError() error {
	if v := os.Getenv("CODEX_HOME"); v != "" && !filepath.IsAbs(v) {
		return fmt.Errorf("CODEX_HOME is a relative path (%s). Set it to the full path of the Codex profile, or unset it, so AgentWorks reads the same profile Codex uses", v)
	}
	return nil
}

// codexPluginDisabled reports whether the person turned the AgentWorks Codex plugin
// off; an update must not turn it back on.
func codexPluginDisabled(config string) bool {
	var cfg struct {
		Plugins map[string]struct {
			Enabled *bool `toml:"enabled"`
		} `toml:"plugins"`
	}
	if toml.Unmarshal([]byte(config), &cfg) != nil {
		return false
	}
	p, ok := cfg.Plugins["agentworks-codex@agentworks"]
	return ok && p.Enabled != nil && !*p.Enabled
}

// providerAvailable says whether a coding CLI can be run: on PATH, or for Codex
// also the app's own command line.
func providerAvailable(provider string) bool {
	onPath := func(n string) bool { _, err := exec.LookPath(n); return err == nil }
	if provider == "codex" {
		return codexExecutable(onPath) != ""
	}
	return onPath(provider)
}

// providerExecutable is the command to run for a provider's own plugin commands.
func providerExecutable(provider string) string {
	if provider == "codex" {
		if exe := codexExecutable(func(n string) bool { _, err := exec.LookPath(n); return err == nil }); exe != "" {
			return exe
		}
	}
	return provider
}

// installedPluginUpdates decides what to refresh from what is installed under
// home and which provider commands exist.
func installedPluginUpdates(home string, onPath func(string) bool, read func(string) string) []pluginUpdate {
	var out []pluginUpdate
	if onPath("claude") && strings.Contains(read(filepath.Join(home, ".claude", "plugins", "installed_plugins.json")), "agentworks@agentworks") {
		out = append(out, pluginUpdate{Provider: "claude", Auto: [][]string{{"plugin", "marketplace", "update", "agentworks"}, {"plugin", "update", "agentworks@agentworks"}},
			Manual: "claude plugin marketplace update agentworks && claude plugin update agentworks@agentworks, then reload Claude Code"})
	}
	if onPath("claude") && strings.Contains(read(filepath.Join(home, ".claude", "plugins", "installed_plugins.json")), "agentworks-tools@agentworks") {
		out = append(out, pluginUpdate{Provider: "claude", Auto: [][]string{{"plugin", "marketplace", "update", "agentworks"}, {"plugin", "update", "agentworks-tools@agentworks"}},
			Manual: "claude plugin marketplace update agentworks && claude plugin update agentworks-tools@agentworks, then reload Claude Code"})
	}
	if codexHomeDir(home) != "" && codexExecutable(onPath) != "" && strings.Contains(read(filepath.Join(codexHomeDir(home), "config.toml")), "agentworks-codex-tools@agentworks") {
		out = append(out, pluginUpdate{Provider: "codex", Auto: [][]string{{"plugin", "marketplace", "upgrade", "agentworks"}, {"plugin", "add", "agentworks-codex-tools@agentworks"}},
			Manual: "codex plugin marketplace upgrade agentworks && codex plugin add agentworks-codex-tools@agentworks, then reload Codex"})
	}
	if codexHomeDir(home) != "" && codexExecutable(onPath) != "" && (strings.Contains(read(filepath.Join(codexHomeDir(home), "config.toml")), "agentworks-codex@agentworks") || strings.Contains(read(filepath.Join(codexHomeDir(home), "plugins", "installed_plugins.json")), "agentworks-codex")) {
		out = append(out, pluginUpdate{Provider: "codex", Auto: [][]string{{"plugin", "marketplace", "upgrade", "agentworks"}, {"plugin", "add", "agentworks-codex@agentworks"}},
			Manual: "codex plugin marketplace upgrade agentworks && codex plugin add agentworks-codex@agentworks, then reload Codex"})
		if codexPluginDisabled(read(filepath.Join(codexHomeDir(home), "config.toml"))) {
			// The person turned the plugin off: refresh the marketplace only and
			// never run plugin add, which would enable it again.
			last := &out[len(out)-1]
			last.Auto = last.Auto[:1]
			last.Disabled = true
		}
	}
	if onPath("muse") {
		out = append(out, pluginUpdate{Provider: "muse", Manual: "muse plugins marketplace update agentworks && muse plugins remove agentworks-muse && muse plugins install agentworks-muse@agentworks, then approve it and restart Muse (skip this if you do not use the AgentWorks plugin in Muse)"})
	}
	// Cursor's hooks run the CLI itself, so an update needs nothing once agentworks setup cursor was run.
	if onPath("cursor-agent") && !strings.Contains(read(filepath.Join(home, ".cursor", "hooks.json")), "plugin hook cursor") {
		out = append(out, pluginUpdate{Provider: "cursor", Manual: "agentworks setup cursor, then restart Cursor (skip this if you do not use AgentWorks in Cursor)"})
	}
	if onPath("pi") {
		out = append(out, pluginUpdate{Provider: "pi", Manual: "agentworks attach in a terminal and approve the Pi package, then reload Pi (skip this if you do not use AgentWorks in Pi)"})
	}
	return out
}

// Claude keeps hook approval with the installed package. A server/CLI release
// does not require installing that same package again.
func claudePluginCurrent(home, id string, read func(string) string) bool {
	var installed struct {
		Plugins map[string][]struct {
			Version string `json:"version"`
		} `json:"plugins"`
	}
	if json.Unmarshal([]byte(read(filepath.Join(home, ".claude", "plugins", "installed_plugins.json"))), &installed) != nil || len(installed.Plugins[id]) == 0 {
		return false
	}
	var known map[string]struct {
		InstallLocation string `json:"installLocation"`
	}
	if json.Unmarshal([]byte(read(filepath.Join(home, ".claude", "plugins", "known_marketplaces.json"))), &known) != nil {
		return false
	}
	root := known["agentworks"].InstallLocation
	if !filepath.IsAbs(root) {
		return false
	}
	var catalog struct {
		Plugins []struct{ Name, Source string } `json:"plugins"`
	}
	if json.Unmarshal([]byte(read(filepath.Join(root, ".claude-plugin", "marketplace.json"))), &catalog) != nil {
		return false
	}
	name, _, _ := strings.Cut(id, "@")
	for _, entry := range catalog.Plugins {
		if entry.Name != name || filepath.IsAbs(entry.Source) {
			continue
		}
		path := filepath.Join(root, entry.Source, ".claude-plugin", "plugin.json")
		if rel, err := filepath.Rel(root, path); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return false
		}
		var manifest struct {
			Version string `json:"version"`
		}
		if json.Unmarshal([]byte(read(path)), &manifest) != nil || manifest.Version == "" {
			return false
		}
		for _, copy := range installed.Plugins[id] {
			if copy.Version != manifest.Version {
				return false
			}
		}
		return true
	}
	return false
}

const (
	pluginsRepo    = "manishiitg/agentworks-plugins"
	pluginsRepoURL = "https://github.com/manishiitg/agentworks-plugins.git"
	oldPluginsRepo = "manishiitg/agentworks"
)

// onOldPluginsSource reports whether a registered marketplace source is the full
// product repository rather than the small plugins repository.
func onOldPluginsSource(source string) bool {
	source = strings.ToLower(source)
	return strings.Contains(source, oldPluginsRepo) && !strings.Contains(source, "agentworks-plugins")
}

// oldMarketplaceProviders lists the providers whose "agentworks" marketplace still
// points at the full repository (a 400 MB clone) instead of the plugins repository.
// Only the marketplace named agentworks is looked at.
func oldMarketplaceProviders(home string, read func(string) string, museList func() string) map[string]bool {
	old := map[string]bool{}
	var known map[string]struct {
		Source struct {
			Repo string `json:"repo"`
			URL  string `json:"url"`
		} `json:"source"`
	}
	if json.Unmarshal([]byte(read(filepath.Join(home, ".claude", "plugins", "known_marketplaces.json"))), &known) == nil {
		if m, ok := known["agentworks"]; ok && onOldPluginsSource(m.Source.Repo+" "+m.Source.URL) {
			old["claude"] = true
		}
	}
	inSection := false
	codexHome := codexHomeDir(home)
	codexLines := []string{}
	if codexHome != "" {
		codexLines = strings.Split(read(filepath.Join(codexHome, "config.toml")), "\n")
	}
	for _, line := range codexLines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			inSection = line == "[marketplaces.agentworks]"
			continue
		}
		if inSection && strings.HasPrefix(line, "source ") && onOldPluginsSource(line) {
			old["codex"] = true
		}
	}
	if museList != nil {
		for _, line := range strings.Split(museList(), "\n") {
			if strings.HasPrefix(line, "agentworks\t") && onOldPluginsSource(line) {
				old["muse"] = true
			}
		}
	}
	return old
}

// migrateMarketplace moves one provider's agentworks marketplace to the plugins
// repository, keeping the marketplace name so plugin ids do not change. It returns
// false when it could not, after trying to put the old source back.
func migrateMarketplace(ctx context.Context, provider string) bool {
	run := func(args ...string) bool {
		step, cancel := context.WithTimeout(ctx, 120*time.Second)
		defer cancel()
		return exec.CommandContext(step, providerExecutable(provider), args...).Run() == nil
	}
	// The plugin's scope and state are recorded before anything is removed, so the
	// reinstall and the rollback restore the same scope and report a lost enablement.
	before := pluginListBlock(ctx, provider)
	var remove, add, restore, reinstall []string
	switch provider {
	case "claude":
		remove, add, restore = []string{"plugin", "marketplace", "remove", "agentworks"}, []string{"plugin", "marketplace", "add", pluginsRepo}, []string{"plugin", "marketplace", "add", oldPluginsRepo}
		reinstall = []string{"plugin", "install", "agentworks@agentworks"}
		if scope := pluginListScope(before); scope != "" {
			reinstall = append(reinstall, "--scope", scope)
		}
	case "codex":
		remove, add, restore = []string{"plugin", "marketplace", "remove", "agentworks"}, []string{"plugin", "marketplace", "add", pluginsRepo}, []string{"plugin", "marketplace", "add", oldPluginsRepo}
		reinstall = []string{"plugin", "add", "agentworks-codex@agentworks"}
	case "muse":
		remove, add, restore = []string{"plugins", "marketplace", "remove", "agentworks"}, []string{"plugins", "marketplace", "add", "agentworks", pluginsRepoURL}, []string{"plugins", "marketplace", "add", "agentworks", "https://github.com/" + oldPluginsRepo + ".git"}
	default:
		return false
	}
	if !run(remove...) {
		return false
	}
	if !run(add...) {
		run(restore...)
		if reinstall != nil {
			run(reinstall...)
		}
		return false
	}
	if reinstall != nil && !run(reinstall...) {
		return false
	}
	after := pluginListBlock(ctx, provider)
	if before != "" && !strings.Contains(strings.ToLower(before), "disabled") && (after == "" || strings.Contains(strings.ToLower(after), "disabled")) {
		fmt.Fprintf(os.Stderr, "The %s plugin is installed but not enabled after the switch. Run: %s\n", provider, pluginEnableStep(provider))
	}
	return true
}

// pluginListBlock returns the part of "<provider> plugin list" that is about the
// AgentWorks plugin (empty when the provider has no such listing or no entry).
func pluginListBlock(ctx context.Context, provider string) string {
	args := []string{"plugin", "list"}
	if provider == "muse" {
		args = []string{"plugins", "list"}
	}
	step, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(step, providerExecutable(provider), args...).Output()
	if err != nil {
		return ""
	}
	var block []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "agentworks") {
			block = append(block, line)
		}
	}
	text := strings.Join(block, "\n")
	if provider == "claude" {
		// Claude prints the version, scope and status on the lines after the name.
		lines := strings.Split(string(out), "\n")
		for i, line := range lines {
			if strings.Contains(line, "agentworks@agentworks") {
				text = strings.Join(lines[i:min(i+4, len(lines))], "\n")
				break
			}
		}
	}
	return text
}

func pluginListScope(block string) string {
	for _, line := range strings.Split(block, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "Scope:"); ok {
			scope := strings.TrimSpace(rest)
			if scope == "user" || scope == "project" || scope == "local" {
				return scope
			}
		}
	}
	return ""
}

func pluginEnableStep(provider string) string {
	switch provider {
	case "claude":
		return "claude plugin enable agentworks@agentworks, then reload Claude Code"
	case "codex":
		return "codex plugin add agentworks-codex@agentworks, then reload Codex"
	}
	return "muse plugins approve agentworks-muse, then restart Muse"
}

// updateInstalledPlugins runs the safe refresh commands and prints one plain
// line per provider that still needs a step from the person.
func updateInstalledPlugins(ctx context.Context, o *options, switchSource bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	onPath := func(name string) bool { _, e := exec.LookPath(name); return e == nil }
	read := func(path string) string { b, _ := os.ReadFile(path); return string(b) }
	if e := codexHomeError(); e != nil {
		fmt.Fprintf(o.stderr, "Skipped the Codex plugin: %v.\n", e)
	}
	old := oldMarketplaceProviders(home, read, func() string {
		if !onPath("muse") {
			return ""
		}
		out, _ := exec.CommandContext(ctx, "muse", "plugins", "marketplace", "list").Output()
		return string(out)
	})
	for _, update := range installedPluginUpdates(home, onPath, read) {
		if update.Provider == "claude" {
			step, cancel := context.WithTimeout(ctx, 90*time.Second)
			_, err := exec.CommandContext(step, "claude", update.Auto[0]...).CombinedOutput()
			cancel()
			if err != nil {
				fmt.Fprintln(o.stderr, "Could not check the Claude plugin; kept its installed hooks unchanged.")
				continue
			}
			id := update.Auto[1][2]
			if claudePluginCurrent(home, id, read) {
				fmt.Fprintf(o.stderr, "Claude plugin %s is current; kept its installed hooks unchanged.\n", id)
				continue
			}
			update.Auto = update.Auto[1:] // the marketplace was already refreshed
		}
		if old[update.Provider] && switchSource && (update.Disabled || strings.Contains(strings.ToLower(pluginListBlock(ctx, update.Provider)), "disabled")) {
			// Moving the source reinstalls the plugin, which would turn it back on.
			fmt.Fprintf(o.stderr, "Your %s plugin is turned off, so its source was not switched (that would turn it on). Turn it on, then run agentworks update again.\n", update.Provider)
		} else if old[update.Provider] {
			if !switchSource {
				fmt.Fprintf(o.stderr, "Your %s plugin still updates from the large repository. Run agentworks update (without --keep-plugin-source) to move it (about a minute; reload the app afterwards).\n", update.Provider)
			} else if migrateMarketplace(ctx, update.Provider) {
				fmt.Fprintf(o.stderr, "Switched the %s plugin source to the small plugins repository (%s).\n", update.Provider, pluginsRepo)
			} else {
				fmt.Fprintf(o.stderr, "Could not switch the %s plugin source to %s; it keeps using the full repository for now.\n", update.Provider, pluginsRepo)
			}
		}
		if update.Provider == "muse" {
			updateMusePlugin(ctx, o)
			continue
		}
		done := len(update.Auto) > 0
		for _, args := range update.Auto {
			step, cancel := context.WithTimeout(ctx, 90*time.Second)
			result, e := exec.CommandContext(step, providerExecutable(update.Provider), args...).CombinedOutput()
			cancel()
			if e != nil && !strings.Contains(strings.ToLower(string(result)), "already") {
				done = false
				break
			}
		}
		if done && update.Disabled {
			fmt.Fprintf(o.stderr, "The %s plugin is turned off; refreshed its source and left it off.\n", update.Provider)
			continue
		}
		if done {
			if update.Provider == "codex" {
				fmt.Fprintln(o.stderr, "Updated the Codex plugin. Quit Codex fully and reopen it to use the updated plugin.")
			} else {
				fmt.Fprintf(o.stderr, "Updated the %s plugin. Reload %s to use it.\n", update.Provider, update.Provider)
			}
			continue
		}
		fmt.Fprintf(o.stderr, "To update the %s plugin, run: %s\n", update.Provider, update.Manual)
	}
}

// musePluginVersions reads Muse's "plugins list" (installed) and "plugins list --available" output:
// one tab-separated line per plugin, "<id> <version> key=value ...". It returns the installed
// AgentWorks plugin's version and whether it is enabled, and the version the marketplace offers.
func musePluginVersions(installedList, availableList string) (installed string, enabled bool, offered string) {
	for _, line := range strings.Split(installedList, "\n") {
		if f := strings.Split(line, "\t"); len(f) >= 2 && f[0] == "agentworks-muse" {
			installed, enabled = f[1], !strings.Contains(line, "enabled=false")
		}
	}
	for _, line := range strings.Split(availableList, "\n") {
		if f := strings.Split(line, "\t"); len(f) >= 2 && f[0] == "agentworks-muse@agentworks" {
			offered = f[1]
		}
	}
	return installed, enabled, offered
}

// updateMusePlugin brings an installed AgentWorks plugin in Muse to the version its marketplace
// offers. Muse 1.4.4 keeps an installed marketplace plugin on its first snapshot, so a new version
// means remove and install again; it is done only when the offered version differs. It never approves
// the new hooks and tool server: that stays the person's step.
func updateMusePlugin(ctx context.Context, o *options) {
	run := func(args ...string) (string, error) {
		step, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		out, err := exec.CommandContext(step, "muse", args...).Output()
		return string(out), err
	}
	manual := "muse plugins marketplace update agentworks && muse plugins remove agentworks-muse && muse plugins install agentworks-muse@agentworks, then approve it and restart Muse"
	list, err := run("plugins", "list")
	if err != nil {
		fmt.Fprintf(o.stderr, "Could not read Muse's plugins. To update the Muse plugin, run: %s\n", manual)
		return
	}
	if installed, _, _ := musePluginVersions(list, ""); installed == "" {
		fmt.Fprintln(o.stderr, "The Muse plugin is not installed; nothing to update.")
		return
	}
	if _, err = run("plugins", "marketplace", "update", "agentworks"); err != nil {
		fmt.Fprintf(o.stderr, "Could not refresh the Muse plugin source. To update the Muse plugin, run: %s\n", manual)
		return
	}
	available, err := run("plugins", "list", "--available")
	installed, enabled, offered := musePluginVersions(list, available)
	if err != nil || offered == "" {
		fmt.Fprintf(o.stderr, "Could not read the Muse plugin version on offer. To update it, run: %s\n", manual)
		return
	}
	if !versionNewer(offered, installed) {
		fmt.Fprintf(o.stderr, "The Muse plugin is up to date (version %s).\n", installed)
		return
	}
	if !enabled {
		fmt.Fprintf(o.stderr, "The Muse plugin is turned off; version %s is on offer and it was left alone. Turn it on, then run agentworks update again.\n", offered)
		return
	}
	if _, err = run("plugins", "remove", "agentworks-muse"); err != nil {
		fmt.Fprintf(o.stderr, "Could not remove the old Muse plugin. To update it, run: %s\n", manual)
		return
	}
	if _, err = run("plugins", "install", "agentworks-muse@agentworks"); err != nil {
		if _, err = run("plugins", "install", "agentworks-muse@agentworks"); err != nil { // the old one is gone: try once more
			fmt.Fprintln(o.stderr, "The old Muse plugin was removed but the new one could not be installed. Run: muse plugins install agentworks-muse@agentworks, then approve it and restart Muse.")
			return
		}
	}
	fmt.Fprintf(o.stderr, "Updated the Muse plugin from %s to %s. Muse asks you to trust AgentWorks' hooks: approve once with muse plugins approve agentworks-muse (look first with muse plugins inspect agentworks-muse). The hooks are what tell AgentWorks which Muse chat is which; if you skip them (Esc), AgentWorks messaging will not work in that chat. Then start a NEW Muse chat; an older one remembers the old tool names and will keep failing.\n", installed, offered)
}

// versionNewer reports whether dotted numeric version a is newer than b ("0.1.10" > "0.1.9"). A marketplace
// that offers an older version never replaces a newer installed one.
func versionNewer(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x > y
		}
	}
	return false
}

// pluginInstallHelp is the exact line the person runs to install the AgentWorks plugin for one CLI, so a chat that
// is attached without native delivery says what to do next instead of leaving the person to find the commands.
func pluginInstallHelp(provider string) string {
	switch provider {
	case "claude":
		return "claude plugin marketplace add " + pluginsRepo + " && claude plugin install agentworks@agentworks, then reload Claude Code"
	case "codex":
		return "codex plugin marketplace add " + pluginsRepo + ", then in the Codex app open Settings > Plugins and install AgentWorks, and trust its hooks in Settings > Hooks"
	case "muse":
		return "muse plugins marketplace add agentworks " + pluginsRepoURL + " && muse plugins install agentworks-muse@agentworks, then muse plugins approve agentworks-muse and start a new Muse chat"
	case "cursor":
		return "agentworks setup cursor, then restart Cursor and type /agentworks attach in the chat"
	case "pi":
		return "run agentworks attach in a terminal and accept the Pi extension install (no repository clone needed), then reload Pi"
	}
	return ""
}
