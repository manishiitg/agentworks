package cliruntime

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// PrepareProjectGit lets a private Linux CLI use repositories created by the
// platform shell's different UID. Trust stays within this project; Git 2.43
// needs exact repository paths, including linked worktrees, rather than /*.
func PrepareProjectGit(runtimeDir, providerHome, project string) error {
	if !filepath.IsAbs(runtimeDir) || !filepath.IsAbs(project) {
		return fmt.Errorf("private CLI Git requires absolute runtime and project paths")
	}
	if providerHome == "" || providerHome == "." || providerHome == ".." || filepath.Base(providerHome) != providerHome {
		return fmt.Errorf("invalid private CLI home")
	}
	project, err := filepath.EvalSymlinks(project)
	if err != nil {
		return err
	}
	roots := []string{project}
	// Match the Files/Git scanners: dependency and build output can contain
	// tens of thousands of files and are not user repository discovery roots.
	skip := map[string]bool{
		"node_modules": true, ".venv": true, "venv": true, "vendor": true,
		"dist": true, "build": true, ".next": true, "target": true,
		"coverage": true, "__pycache__": true, ".cache": true,
		".sandbox-cache": true, ".report-cache": true,
	}
	if err := filepath.WalkDir(project, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // An inaccessible subtree cannot supply a usable repository.
		}
		if path != project && entry.IsDir() && skip[entry.Name()] {
			return filepath.SkipDir
		}
		if entry.Name() == ".git" {
			if parent := filepath.Dir(path); parent != project {
				roots = append(roots, parent)
			}
			if entry.IsDir() {
				return filepath.SkipDir
			}
		}
		return nil // WalkDir never follows a folder symlink outside the project.
	}); err != nil {
		return err
	}
	home := runtimeDir
	for _, component := range []string{"", ".sandbox-cache", "cli-home", providerHome} {
		home = filepath.Join(home, component)
		if err := os.Mkdir(home, 0700); err != nil && !os.IsExist(err) {
			return err
		}
		info, err := os.Lstat(home)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("private CLI Git home is not a directory: %s", home)
		}
	}
	config := filepath.Join(home, ".gitconfig")
	if info, err := os.Lstat(config); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("private CLI Git config is not a regular file")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	git := func(args ...string) (string, error) {
		out, err := exec.Command("git", append([]string{"config", "--file", config}, args...)...).CombinedOutput()
		return string(out), err
	}
	// Keep names, aliases and other private-home settings. Reset only the
	// platform's trust list, so rebinding a Crew does not retain its old root.
	want := "\n" + strings.Join(roots, "\n") + "\n"
	if have, _ := git("--get-all", "safe.directory"); have != want {
		if out, err := git("--replace-all", "safe.directory", ""); err != nil {
			return fmt.Errorf("prepare private Git trust: %w: %s", err, out)
		}
		for _, root := range roots {
			if out, err := git("--add", "safe.directory", root); err != nil {
				return fmt.Errorf("prepare private Git trust: %w: %s", err, out)
			}
		}
	}
	return nil
}
