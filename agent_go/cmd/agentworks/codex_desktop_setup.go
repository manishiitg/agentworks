package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// The desktop-only installer ships an immutable public bundle and its digest.
// Refresh it only through person-run setup/update, never from a hook or timer.
const codexPluginBundleRevision = "66a6f5dbb3fc423ea0dbcf8cf4659bba62f2582b"
const codexPluginBundleSHA256 = "6002b93163b20e75ebd148a2f88f740b6384924aac9c9766a2d044cfba44c5ac"

func codexManagedMarketplace(home string) bool {
	profile := codexHomeDir(home)
	var cfg struct {
		Marketplaces map[string]struct {
			SourceType string `toml:"source_type"`
			Source     string `toml:"source"`
		} `toml:"marketplaces"`
	}
	raw, err := os.ReadFile(filepath.Join(profile, "config.toml"))
	if profile == "" || err != nil || toml.Unmarshal(raw, &cfg) != nil {
		return false
	}
	m := cfg.Marketplaces["agentworks"]
	return m.SourceType == "local" && filepath.Clean(m.Source) == filepath.Join(profile, "plugins", "marketplaces", "agentworks")
}

func setupCodexPlugin(ctx context.Context, o *options, name string) error {
	if err := codexHomeError(); err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	// The native marketplace command shells out to git clone. A desktop-only
	// machine without Git needs the same verified direct package, not another
	// installation prerequisite.
	_, gitErr := exec.LookPath("git")
	if gitErr != nil || codexManagedMarketplace(home) || codexExecutable(func(n string) bool { _, e := exec.LookPath(n); return e == nil }) == "" {
		return installCodexDesktopPlugin(ctx, o, codexHomeDir(home), name)
	}
	// Preserve a person's disabled choice just as the normal updater does.
	raw, _ := os.ReadFile(filepath.Join(codexHomeDir(home), "config.toml"))
	var cfg struct {
		Plugins map[string]struct{ Enabled *bool } `toml:"plugins"`
	}
	_ = toml.Unmarshal(raw, &cfg)
	steps := [][]string{{"plugin", "marketplace", "add", pluginsRepo}}
	if p := cfg.Plugins[name+"@agentworks"]; p.Enabled == nil || *p.Enabled {
		steps = append(steps, []string{"plugin", "add", name + "@agentworks"})
	}
	if err = runPluginSteps(ctx, o, "codex", steps); err == nil {
		return nil
	}
	// Some packaged app binaries exist but cannot be started outside the app.
	var startErr *os.PathError
	var lookupErr *exec.Error
	if errors.As(err, &startErr) || errors.As(err, &lookupErr) {
		fmt.Fprintln(o.stderr, "Codex's bundled command could not start; installing its desktop plugin directly.")
		return installCodexDesktopPlugin(ctx, o, codexHomeDir(home), name)
	}
	return err
}

func installCodexDesktopPlugin(ctx context.Context, o *options, profile, name string) error {
	// Refuse a broken config before downloading or replacing any installed files.
	if _, err := readCodexSettings(profile); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://codeload.github.com/"+pluginsRepo+"/zip/"+codexPluginBundleRevision, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("could not download the Codex plugin; check your connection and run agentworks setup codex again: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Codex plugin download returned HTTP %d; run agentworks setup codex again", resp.StatusCode)
	}
	bundle, err := io.ReadAll(io.LimitReader(resp.Body, (16<<20)+1))
	if err != nil {
		return err
	}
	if len(bundle) > 16<<20 {
		return errors.New("Codex plugin download is too large")
	}
	return installCodexDesktopBundle(o, profile, name, bundle, codexPluginBundleSHA256)
}

func installCodexDesktopBundle(o *options, profile, name string, bundle []byte, digest string) error {
	if name != "agentworks-codex" && name != "agentworks-codex-tools" {
		return errors.New("unknown Codex plugin")
	}
	sum := sha256.Sum256(bundle)
	if hex.EncodeToString(sum[:]) != digest {
		return errors.New("Codex plugin checksum does not match; nothing was installed")
	}
	z, err := zip.NewReader(bytes.NewReader(bundle), int64(len(bundle)))
	if err != nil {
		return fmt.Errorf("invalid Codex plugin archive: %w", err)
	}
	parent := filepath.Join(profile, "plugins", "marketplaces")
	if err = os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(parent, ".agentworks-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	var expanded uint64
	for _, f := range z.File {
		// Check every entry before selecting files; ZIP paths always use '/'.
		if strings.Contains(f.Name, "\\") || path.IsAbs(f.Name) || strings.Contains(f.Name, ":") || f.Mode()&os.ModeSymlink != 0 {
			return errors.New("unsafe path in Codex plugin archive")
		}
		for _, part := range strings.Split(f.Name, "/") {
			if part == ".." {
				return errors.New("unsafe path in Codex plugin archive")
			}
		}
		_, rel, ok := strings.Cut(f.Name, "/")
		if !ok || f.FileInfo().IsDir() || !(strings.HasPrefix(rel, "plugins/codex/") || strings.HasPrefix(rel, "plugins/codex-tools/")) {
			continue
		}
		expanded += f.UncompressedSize64
		if expanded > 32<<20 {
			return errors.New("Codex plugin archive is too large")
		}
		r, e := f.Open()
		if e != nil {
			return e
		}
		data, e := io.ReadAll(io.LimitReader(r, (32<<20)+1))
		r.Close()
		if e != nil {
			return e
		}
		if e = writeDesktopPluginFile(filepath.Join(stage, filepath.FromSlash(rel)), data); e != nil {
			return e
		}
	}
	entries := []any{}
	versions := map[string]string{}
	for plugin, dir := range map[string]string{"agentworks-codex": "codex", "agentworks-codex-tools": "codex-tools"} {
		var manifest struct{ Name, Version string }
		raw, e := os.ReadFile(filepath.Join(stage, "plugins", dir, ".codex-plugin", "plugin.json"))
		if e != nil || json.Unmarshal(raw, &manifest) != nil || manifest.Name != plugin || manifest.Version == "" || strings.ContainsAny(manifest.Version, "/\\:") || manifest.Version == "." || manifest.Version == ".." {
			return errors.New("Codex plugin archive has an invalid manifest")
		}
		versions[plugin] = manifest.Version
		entries = append(entries, map[string]any{"name": plugin, "source": map[string]string{"source": "local", "path": "./plugins/" + dir}, "policy": map[string]string{"installation": "AVAILABLE", "authentication": "ON_INSTALL"}, "category": "Productivity"})
	}
	catalog, _ := json.MarshalIndent(map[string]any{"name": "agentworks", "plugins": entries}, "", "  ")
	if err = writeDesktopPluginFile(filepath.Join(stage, ".agents", "plugins", "marketplace.json"), catalog); err != nil {
		return err
	}
	source := filepath.Join(parent, "agentworks")
	if err = os.RemoveAll(source); err != nil {
		return err
	}
	if err = os.Rename(stage, source); err != nil {
		return err
	}
	dir := "codex"
	if name == "agentworks-codex-tools" {
		dir = "codex-tools"
	}
	cache := filepath.Join(profile, "plugins", "cache", "agentworks", name)
	if err = os.RemoveAll(cache); err != nil {
		return err
	}
	if err = os.CopyFS(filepath.Join(cache, versions[name]), os.DirFS(filepath.Join(source, "plugins", dir))); err != nil {
		return err
	}
	_, err = editCodexSettings(profile, o, func(doc map[string]any) error {
		marketplaces := codexSettingsTable(doc, "marketplaces")
		marketplaces["agentworks"] = map[string]any{"source_type": "local", "source": source}
		plugins := codexSettingsTable(doc, "plugins")
		id := name + "@agentworks"
		entry := codexSettingsTable(plugins, id)
		if _, exists := entry["enabled"]; !exists {
			entry["enabled"] = true
		}
		return nil
	})
	return err
}

func writeDesktopPluginFile(file string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	return os.WriteFile(file, data, 0o600)
}

func readCodexSettings(profile string) (map[string]any, error) {
	doc := map[string]any{}
	raw, err := os.ReadFile(filepath.Join(profile, "config.toml"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if len(raw) > 0 && toml.Unmarshal(raw, &doc) != nil {
		return nil, errors.New("Codex config.toml is not valid TOML; fix it in Codex settings, then run setup again")
	}
	// These provider-owned sections are tables, never scalars. Do not replace a
	// malformed section (or one plugin/server entry) and lose the person's data.
	for _, section := range []string{"marketplaces", "plugins", "mcp_servers"} {
		value, exists := doc[section]
		if !exists {
			continue
		}
		table, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("Codex config.toml %s must be a table; fix it in Codex settings, then run setup again", section)
		}
		for _, entry := range table {
			if _, ok := entry.(map[string]any); !ok {
				return nil, fmt.Errorf("Codex config.toml %s contains an invalid entry; fix it in Codex settings, then run setup again", section)
			}
		}
	}
	return doc, nil
}

func codexSettingsTable(doc map[string]any, key string) map[string]any {
	if table, ok := doc[key].(map[string]any); ok {
		return table
	}
	table := map[string]any{}
	doc[key] = table
	return table
}

// Keep logical settings (including disabled state and trust) and back up the
// original text before changing it. No auth data is read from AgentWorks here.
func editCodexSettings(profile string, o *options, edit func(map[string]any) error) (bool, error) {
	doc, err := readCodexSettings(profile)
	if err != nil {
		return false, err
	}
	before, _ := toml.Marshal(doc)
	if err = edit(doc); err != nil {
		return false, err
	}
	after, err := toml.Marshal(doc)
	if err != nil || bytes.Equal(before, after) {
		return false, err
	}
	file := filepath.Join(profile, "config.toml")
	if prior, e := os.ReadFile(file); e == nil {
		backup := file + ".agentworks-backup-" + time.Now().UTC().Format("20060102T150405.000000000Z")
		if err = os.WriteFile(backup, prior, 0o600); err != nil {
			return false, err
		}
		fmt.Fprintln(o.stdout, "Saved a copy of your previous settings: "+backup)
	}
	if err = os.MkdirAll(profile, 0o700); err != nil {
		return false, err
	}
	f, err := os.CreateTemp(profile, ".agentworks-*.toml")
	if err != nil {
		return false, err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(after)
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return false, err
	}
	return true, os.Rename(f.Name(), file)
}
