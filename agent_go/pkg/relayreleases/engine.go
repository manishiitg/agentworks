// Package relayreleases implements immutable Relay release snapshots.
package relayreleases

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/relayapi"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

var relayVersionPattern = regexp.MustCompile(`^v[1-9][0-9]*$`)

func releaseWorkspace(workspace, version string) string {
	return path.Join(workflowtypes.RelayReleaseRoot(workspace), version)
}

type builtinEngine struct{}

// Builtin is retained for migration conformance and public-only builds.
func Builtin() relayapi.Releases { return builtinEngine{} }
func (builtinEngine) Read(ctx context.Context, p relayapi.ReleasePorts, workspace, version string) (*relayapi.Release, string, error) {
	return readRelayRelease(ctx, p, workspace, version)
}
func (builtinEngine) Active(ctx context.Context, p relayapi.ReleasePorts, workspace string) (*relayapi.Release, string, error) {
	return activeRelayRelease(ctx, p, workspace)
}
func (builtinEngine) Resolve(ctx context.Context, p relayapi.ReleasePorts, workspace, version string) (*relayapi.Release, string, error) {
	return resolveRelayRelease(ctx, p, workspace, version)
}
func (builtinEngine) List(ctx context.Context, p relayapi.ReleasePorts, workspace string) ([]relayapi.Release, error) {
	return listRelayReleases(ctx, p, workspace)
}
func (builtinEngine) Verify(ctx context.Context, p relayapi.ReleasePorts, release *relayapi.Release, workspace string) error {
	return verifyRelayRelease(ctx, p, release, workspace)
}
func (builtinEngine) Publish(ctx context.Context, p relayapi.ReleasePorts, workspace string) (*relayapi.Release, error) {
	return publishRelayRelease(ctx, p, workspace)
}
func readRelayRelease(ctx context.Context, p relayapi.ReleasePorts, workspace, version string) (*relayapi.Release, string, error) {
	if !relayVersionPattern.MatchString(version) {
		return nil, "", fmt.Errorf("invalid Relay version %q", version)
	}
	releaseWorkspace := releaseWorkspace(workspace, version)
	raw, exists, err := p.Read(ctx, path.Join(releaseWorkspace, "release.json"))
	if err != nil {
		return nil, "", err
	}
	if !exists {
		return nil, "", fmt.Errorf("%w: %q", relayapi.ErrVersionNotFound, version)
	}
	var release relayapi.Release
	if err := json.Unmarshal([]byte(raw), &release); err != nil || release.Version != version || release.Hash == "" || len(release.Files) == 0 || release.FileCount != len(release.Files) {
		return nil, "", fmt.Errorf("Relay version %q has invalid release metadata", version)
	}
	return &release, releaseWorkspace, nil
}

func SnapshotHash(content map[string]string) string {
	keys := make([]string, 0, len(content))
	for key := range content {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	hasher := sha256.New()
	for _, key := range keys {
		hasher.Write([]byte(key))
		hasher.Write([]byte{0})
		hasher.Write([]byte(content[key]))
		hasher.Write([]byte{0})
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func verifyRelayRelease(ctx context.Context, p relayapi.ReleasePorts, release *relayapi.Release, workspace string) error {
	if release == nil {
		return errors.New("missing Relay release")
	}
	paths, err := p.ListFiles(ctx, workspace)
	if err != nil {
		return err
	}
	content := make(map[string]string, len(release.Files))
	known := make(map[string]bool, len(release.Files))
	for _, file := range release.Files {
		known[file] = true
	}
	for _, file := range paths {
		relative := strings.TrimPrefix(file, workspace+"/")
		if relative == file || (!known[relative] && (relative == "release.json" || RuntimeFile(relative))) {
			continue
		}
		if !known[relative] {
			return fmt.Errorf("published Relay contains unexpected file %q", relative)
		}
		raw, exists, err := p.Read(ctx, file)
		if err != nil || !exists {
			return fmt.Errorf("published Relay file %q is unavailable", relative)
		}
		content[relative] = raw
	}
	if len(content) != release.FileCount || SnapshotHash(content) != release.Hash {
		return fmt.Errorf("published Relay %s changed after publishing", release.Version)
	}
	return nil
}

func activeRelayRelease(ctx context.Context, p relayapi.ReleasePorts, workspace string) (*relayapi.Release, string, error) {
	raw, exists, err := p.Read(ctx, path.Join(workflowtypes.RelayReleaseRoot(workspace), "active.json"))
	if err != nil || !exists {
		return nil, "", errors.New("Relay has no published version")
	}
	var pointer struct {
		Version string `json:"version"`
	}
	if json.Unmarshal([]byte(raw), &pointer) != nil {
		return nil, "", errors.New("Relay active version is invalid")
	}
	return readRelayRelease(ctx, p, workspace, pointer.Version)
}

func resolveRelayRelease(ctx context.Context, p relayapi.ReleasePorts, workspace, version string) (*relayapi.Release, string, error) {
	if version == "" {
		return activeRelayRelease(ctx, p, workspace)
	}
	return readRelayRelease(ctx, p, workspace, version)
}

func listRelayReleases(ctx context.Context, p relayapi.ReleasePorts, workspace string) ([]relayapi.Release, error) {
	names, err := p.ListFolders(ctx, workflowtypes.RelayReleaseRoot(workspace))
	if err != nil {
		return nil, err
	}
	var releases []relayapi.Release
	for _, name := range names {
		if !relayVersionPattern.MatchString(name) {
			continue
		}
		release, _, err := readRelayRelease(ctx, p, workspace, name)
		if err != nil {
			release = &relayapi.Release{Version: name, Error: err.Error()}
		}
		releases = append(releases, *release)
	}
	sort.Slice(releases, func(i, j int) bool {
		var left, right int
		fmt.Sscanf(releases[i].Version, "v%d", &left)
		fmt.Sscanf(releases[j].Version, "v%d", &right)
		return left < right
	})
	return releases, nil
}

func SnapshotFile(relative string) bool {
	if relative == "" || relative == "release.json" || strings.HasSuffix(relative, ".pyc") || RuntimeFile(relative) || strings.HasPrefix(relative, ".") {
		return false
	}
	for _, part := range strings.Split(relative, "/") {
		if strings.HasPrefix(part, ".") || part == "__pycache__" {
			return false
		}
	}
	return true
}

func RuntimeFile(relative string) bool {
	if relative == "schedule-runs.json" || relative == ".webhook-sequence" || relative == "workflow.json.kb-lock" {
		return true
	}
	for _, prefix := range []string{"runs/", "relay_releases/", "chat/", "chats/", "chat_history/", "backup/", "publish/", "planning/revisions/", "planning/changelog/", "variables/changelog/", "knowledgebase/notes/", ".sandbox-cache/", "costs/", "db/", "config/", "webhooks/", "builder/", "session/", "sessions/", "logs/"} {
		if strings.HasPrefix(relative, prefix) {
			return true
		}
	}
	return false
}

// publishRelayRelease freezes a Relay in a nested workspace. The ordinary
// runtime then reads that workspace unchanged while the parent remains a draft.

func publishRelayRelease(ctx context.Context, p relayapi.ReleasePorts, workspace string) (*relayapi.Release, error) {
	if p.LockPublication == nil || p.Prepare == nil || p.ValidatePublished == nil || p.Published == nil || p.Read == nil || p.ListFiles == nil || p.ListFolders == nil || p.CreateFolder == nil || p.Write == nil {
		return nil, errors.New("Relay publication host capabilities unavailable")
	}
	unlock := p.LockPublication(workspace)
	if unlock == nil {
		return nil, errors.New("Relay publication lock unavailable")
	}
	defer unlock()
	spec, err := p.Prepare(ctx, workspace)
	if err != nil {
		return nil, err
	}
	functions := append([]string(nil), spec.Functions...)
	files, err := p.ListFiles(ctx, workspace)
	if err != nil {
		return nil, fmt.Errorf("list Relay files: %w", err)
	}
	content := map[string]string{}
	var size int
	for _, file := range files {
		relative := strings.TrimPrefix(file, workspace+"/")
		if relative == file || !SnapshotFile(relative) {
			continue
		}
		if len(content) >= 5000 {
			return nil, errors.New("Relay has more than 5000 publishable files")
		}
		raw, exists, err := p.Read(ctx, file)
		if err != nil || !exists {
			return nil, fmt.Errorf("read Relay file %q: %w", relative, err)
		}
		if !utf8.ValidString(raw) {
			return nil, fmt.Errorf("Relay file %q is not UTF-8 text", relative)
		}
		size += len(raw)
		if size > 50*1024*1024 {
			return nil, errors.New("Relay publishable files exceed 50 MiB")
		}
		content[relative] = raw
	}
	for _, required := range spec.RequiredFiles {
		if _, ok := content[required]; !ok {
			return nil, fmt.Errorf("Relay needs %s before publishing", required)
		}
	}
	for _, required := range spec.RequiredScripts {
		if _, exists := content[required]; !exists {
			return nil, fmt.Errorf("Relay script %q needs saved %s", path.Base(path.Dir(required)), required)
		}
	}
	// Hash the exact file names and contents, independent of workspace listing order.
	keys := make([]string, 0, len(content))
	for key := range content {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	hash := SnapshotHash(content)
	if active, _, err := activeRelayRelease(ctx, p, workspace); err == nil && active.Hash == hash {
		return active, nil
	}
	names, err := p.ListFolders(ctx, workflowtypes.RelayReleaseRoot(workspace))
	if err != nil {
		return nil, err
	}
	next := 1
	for _, name := range names {
		if !relayVersionPattern.MatchString(name) {
			continue
		}
		var number int
		fmt.Sscanf(name, "v%d", &number)
		if number >= next {
			next = number + 1
		}
	}
	version := fmt.Sprintf("v%d", next)
	root := workflowtypes.RelayReleaseRoot(workspace)
	destination := releaseWorkspace(workspace, version)
	if err := p.CreateFolder(ctx, path.Dir(root)); err != nil {
		return nil, err
	}
	if err := p.CreateFolder(ctx, root); err != nil {
		return nil, err
	}
	if err := p.CreateFolder(ctx, destination); err != nil {
		return nil, err
	}
	created := map[string]bool{destination: true}
	for _, key := range keys {
		folder := destination
		for _, part := range strings.Split(path.Dir(key), "/") {
			if part == "." {
				break
			}
			folder = path.Join(folder, part)
			if !created[folder] {
				if err := p.CreateFolder(ctx, folder); err != nil {
					return nil, err
				}
				created[folder] = true
			}
		}
		if err := p.Write(ctx, path.Join(destination, key), content[key]); err != nil {
			return nil, fmt.Errorf("copy Relay file %q: %w", key, err)
		}
	}
	release := &relayapi.Release{Version: version, SourceWorkspace: workspace, Hash: hash, PublishedAt: time.Now().UTC(), Functions: functions, OutputStep: spec.OutputStep, FileCount: len(keys), Files: keys}
	encoded, _ := json.Marshal(release)
	if err := p.Write(ctx, path.Join(destination, "release.json"), string(encoded)); err != nil {
		return nil, err
	}
	if err := p.ValidatePublished(ctx, destination, release); err != nil {
		return nil, err
	}
	if err := verifyRelayRelease(ctx, p, release, destination); err != nil {
		return nil, err
	}
	pointer, _ := json.Marshal(map[string]string{"version": version})
	if err := p.Write(ctx, path.Join(root, "active.json"), string(pointer)); err != nil {
		return nil, err
	}
	p.Published(path.Join(workspace, "planning/plan.json"))
	return release, nil
}
