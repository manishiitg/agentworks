package security

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Use the same release archive served by the authenticated browser bridge.
// Staging beside the other helpers adds no host install or filesystem grant.
func stagePythonPlaywrightRuntime(directory string, mode os.FileMode) error {
	assets := os.Getenv("AGENTWORKS_PLAYWRIGHT_PACKAGES_DIR")
	explicit := assets != ""
	if !explicit {
		assets = "packages"
	}
	archive, err := zip.OpenReader(filepath.Join(assets, "agentworks-playwright-python.zip"))
	if os.IsNotExist(err) && !explicit {
		return nil // Standalone workspace library callers have no platform assets.
	}
	if err != nil {
		return fmt.Errorf("AgentWorks browser helper package is unavailable; ask an admin to update the app: %w", err)
	}
	defer archive.Close()
	files := map[string][]byte{}
	for _, file := range archive.File {
		name := strings.TrimPrefix(file.Name, "agentworks-playwright-python/")
		name = strings.TrimPrefix(name, "runtime-dependencies/")
		if !strings.HasPrefix(name, "agentworks_playwright/") && !strings.HasPrefix(name, "websocket/") {
			continue
		}
		if file.FileInfo().IsDir() {
			continue
		}
		if !filepath.IsLocal(name) || !file.Mode().IsRegular() {
			return fmt.Errorf("AgentWorks browser helper package contains an invalid file")
		}
		reader, err := file.Open()
		if err != nil {
			return err
		}
		content, readErr := io.ReadAll(reader)
		reader.Close()
		if readErr != nil {
			return readErr
		}
		files[name] = content
	}
	for _, required := range []string{"agentworks_playwright/__init__.py", "websocket/__init__.py"} {
		if _, ok := files[required]; !ok {
			return fmt.Errorf("AgentWorks browser helper package is incomplete (%s); ask an admin to update the app", required)
		}
	}
	dirMode := os.FileMode(0700)
	if mode&0040 != 0 {
		dirMode = os.ModeSetgid | 0750
	}
	for name, content := range files {
		target := filepath.Join(directory, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), dirMode); err != nil {
			return err
		}
		if err := os.WriteFile(target, content, mode); err != nil {
			return err
		}
	}
	return nil
}
