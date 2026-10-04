package videoproduct

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productdeps"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

func syncManagedProductSkills(ctx context.Context, workspacePath string) error {
	if err := productdeps.Ensure(ctx, workspacePath, mustVideoStudioManifest().Dependencies); err != nil {
		return err
	}
	return syncClaudeProductSkills(workspacePath)
}

// syncClaudeProductSkills materializes the product-owned skills in Claude
// Code's actual discovery location. The embedded copies remain the service
// source of truth; this makes the same guidance available to Claude Code and
// visible in the project Files panel without a second, reference-only copy.
func syncClaudeProductSkills(workspacePath string) error {
	root := filepath.Join(workspacePath, ".claude", "skills")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("create Claude Code Video Studio skills folder: %w", err)
	}
	for _, definition := range profileSkills {
		sourceRoot := filepath.ToSlash(filepath.Dir(definition.path))
		targetRoot := filepath.Join(root, definition.name)
		if err := fs.WalkDir(profileSkillFiles, sourceRoot, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			relative, err := filepath.Rel(filepath.FromSlash(sourceRoot), filepath.FromSlash(path))
			if err != nil {
				return err
			}
			target := filepath.Join(targetRoot, relative)
			if entry.IsDir() {
				return os.MkdirAll(target, 0o755)
			}
			content, err := profileSkillFiles.ReadFile(path)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			return os.WriteFile(target, content, 0o644)
		}); err != nil {
			return fmt.Errorf("materialize Claude Code Video Studio skill %s: %w", strings.TrimSpace(definition.name), err)
		}
	}
	// The previous deployment introduced this product-specific reference mirror.
	// It is not a user skill namespace, so remove only that exact legacy folder
	// after the real Claude Code skills have been materialized successfully.
	legacyRoot := filepath.Join(workspacePath, "skills", "video-studio")
	if err := os.RemoveAll(legacyRoot); err != nil {
		return fmt.Errorf("remove legacy Video Studio skills mirror: %w", err)
	}
	return nil
}

// SyncVisibleSkillsForExistingProjects upgrades workspaces created before the
// visible product-skill library existed. It is intentionally limited to the
// Video Studio project layout and is safe to run at every agent startup.
func SyncVisibleSkillsForExistingProjects(docsRoot string) error {
	docsRoot = strings.TrimSpace(docsRoot)
	if docsRoot == "" {
		return nil
	}
	usersRoot := filepath.Join(docsRoot, workspaceref.UsersDir)
	users, err := os.ReadDir(usersRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("list workspace users: %w", err)
	}
	for _, user := range users {
		if !user.IsDir() {
			continue
		}
		projectsRoot := filepath.Join(usersRoot, user.Name(), "Chats", "Video Studio", "projects")
		projects, err := os.ReadDir(projectsRoot)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("list Video Studio projects for %s: %w", user.Name(), err)
		}
		for _, project := range projects {
			if !project.IsDir() {
				continue
			}
			if err := syncClaudeProductSkills(filepath.Join(projectsRoot, project.Name())); err != nil {
				return fmt.Errorf("sync Claude Code skills for Video Studio project %s: %w", project.Name(), err)
			}
		}
	}
	return nil
}
