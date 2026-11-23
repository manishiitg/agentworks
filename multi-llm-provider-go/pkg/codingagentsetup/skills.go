package codingagentsetup

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	skillassets "github.com/manishiitg/multi-llm-provider-go/skills"
)

const managedSkillMarker = ".llm-provider-mcp-managed"

var delegationSkillFiles = []string{"SKILL.md"}

var deprecatedDelegationSkillFiles = []string{
	filepath.Join("agents", "openai.yaml"),
}

type delegationSkillDestination struct {
	host      string
	directory string
}

func delegationSkillDestinations(project string, hosts []string) []delegationSkillDestination {
	destinations := make([]delegationSkillDestination, 0, len(hosts))
	for _, host := range hosts {
		var root string
		switch host {
		case "codex":
			root = filepath.Join(project, ".agents", "skills")
		case "claude":
			root = filepath.Join(project, ".claude", "skills")
		default:
			continue
		}
		destinations = append(destinations, delegationSkillDestination{
			host:      host,
			directory: filepath.Join(root, skillassets.DelegationSkillName),
		})
	}
	return destinations
}

func delegationSkillEntryPaths(project string, hosts []string) []string {
	destinations := delegationSkillDestinations(project, hosts)
	paths := make([]string, 0, len(destinations))
	for _, destination := range destinations {
		paths = append(paths, filepath.Join(destination.directory, "SKILL.md"))
	}
	return paths
}

func preflightDelegationSkills(project string, hosts []string) error {
	for _, destination := range delegationSkillDestinations(project, hosts) {
		if err := preflightDelegationSkill(destination); err != nil {
			return err
		}
	}
	return nil
}

func preflightDelegationSkill(destination delegationSkillDestination) error {
	info, err := os.Lstat(destination.directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect %s skill directory %q: %w", destination.host, destination.directory, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("cannot install %s skill: %q is not a regular directory", destination.host, destination.directory)
	}

	marker := filepath.Join(destination.directory, managedSkillMarker)
	markerInfo, markerErr := os.Lstat(marker)
	if markerErr == nil {
		if !markerInfo.Mode().IsRegular() {
			return fmt.Errorf("cannot install %s skill: management marker %q is not a regular file", destination.host, marker)
		}
		return nil
	}
	if !errors.Is(markerErr, os.ErrNotExist) {
		return fmt.Errorf("inspect %s skill management marker %q: %w", destination.host, marker, markerErr)
	}

	entry := filepath.Join(destination.directory, "SKILL.md")
	entryInfo, entryErr := os.Lstat(entry)
	if errors.Is(entryErr, os.ErrNotExist) {
		return nil
	}
	if entryErr != nil {
		return fmt.Errorf("inspect existing %s skill %q: %w", destination.host, entry, entryErr)
	}
	if !entryInfo.Mode().IsRegular() {
		return fmt.Errorf("cannot install %s skill: %q is not a regular file", destination.host, entry)
	}
	existing, err := os.ReadFile(entry)
	if err != nil {
		return fmt.Errorf("read existing %s skill %q: %w", destination.host, entry, err)
	}
	canonical, err := embeddedDelegationSkillFile("SKILL.md")
	if err != nil {
		return err
	}
	if bytes.Equal(existing, canonical) {
		return nil
	}
	return fmt.Errorf("cannot install %s skill: %q already contains an unmanaged skill; move or rename it and run setup again", destination.host, entry)
}

func installDelegationSkills(project string, hosts []string) ([]string, error) {
	if err := preflightDelegationSkills(project, hosts); err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(hosts))
	for _, destination := range delegationSkillDestinations(project, hosts) {
		if err := os.MkdirAll(destination.directory, 0o755); err != nil {
			return nil, fmt.Errorf("create %s skill directory %q: %w", destination.host, destination.directory, err)
		}
		for _, relativePath := range delegationSkillFiles {
			content, err := embeddedDelegationSkillFile(relativePath)
			if err != nil {
				return nil, err
			}
			target := filepath.Join(destination.directory, relativePath)
			if err := writeFileAtomically(target, content, 0o644); err != nil {
				return nil, fmt.Errorf("install %s skill file %q: %w", destination.host, target, err)
			}
		}
		if err := removeDeprecatedDelegationSkillFiles(destination.directory); err != nil {
			return nil, fmt.Errorf("remove deprecated %s skill metadata: %w", destination.host, err)
		}
		marker := filepath.Join(destination.directory, managedSkillMarker)
		if err := writeFileAtomically(marker, []byte("Installed by llm-provider-mcp setup.\n"), 0o644); err != nil {
			return nil, fmt.Errorf("write %s skill management marker %q: %w", destination.host, marker, err)
		}
		paths = append(paths, filepath.Join(destination.directory, "SKILL.md"))
	}
	return paths, nil
}

func removeDeprecatedDelegationSkillFiles(directory string) error {
	for _, relativePath := range deprecatedDelegationSkillFiles {
		path := filepath.Join(directory, relativePath)
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove %q: %w", path, err)
		}
		_ = os.Remove(filepath.Dir(path))
	}
	return nil
}

func embeddedDelegationSkillFile(relativePath string) ([]byte, error) {
	path := filepath.ToSlash(filepath.Join(skillassets.DelegationSkillName, relativePath))
	content, err := skillassets.Files.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read embedded delegation skill file %q: %w", path, err)
	}
	return content, nil
}

func writeFileAtomically(path string, content []byte, mode fs.FileMode) (returnErr error) {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".llm-provider-mcp-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	closed := false
	defer func() {
		if !closed {
			if closeErr := temporary.Close(); returnErr == nil && closeErr != nil {
				returnErr = closeErr
			}
		}
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(mode); err != nil {
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	closed = true
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return nil
}

func removeManagedDelegationSkills(project string, hosts []string) (removed, kept []string, returnErr error) {
	for _, destination := range delegationSkillDestinations(project, hosts) {
		info, err := os.Lstat(destination.directory)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return removed, kept, fmt.Errorf("inspect %s skill directory %q: %w", destination.host, destination.directory, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			kept = append(kept, destination.directory)
			continue
		}
		markerInfo, err := os.Lstat(filepath.Join(destination.directory, managedSkillMarker))
		if errors.Is(err, os.ErrNotExist) {
			kept = append(kept, destination.directory)
			continue
		}
		if err != nil {
			return removed, kept, fmt.Errorf("inspect %s skill management marker: %w", destination.host, err)
		}
		if !markerInfo.Mode().IsRegular() {
			kept = append(kept, destination.directory)
			continue
		}
		if err := os.RemoveAll(destination.directory); err != nil {
			return removed, kept, fmt.Errorf("remove %s skill directory %q: %w", destination.host, destination.directory, err)
		}
		removed = append(removed, destination.directory)
	}
	return removed, kept, nil
}
