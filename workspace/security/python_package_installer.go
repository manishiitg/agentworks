package security

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// Only a declared-requirements launch needs the installer. Hard-link the
// release binary where possible; never download or copy it on every tool call.
func stagePythonPackageInstaller(directory string) error {
	assets := os.Getenv("AGENTWORKS_PLAYWRIGHT_PACKAGES_DIR")
	if assets == "" {
		assets = "packages"
	}
	name := "uv"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	source := filepath.Join(assets, name)
	target := filepath.Join(directory, name)
	if err := os.Link(source, target); err == nil {
		return nil
	} else if os.IsNotExist(err) {
		return nil // The bootstrap uses pip if this release has no native uv.
	}
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer file.Close()
	destination, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0555)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(destination, file)
	closeErr := destination.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
