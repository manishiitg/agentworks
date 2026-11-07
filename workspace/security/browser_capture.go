package security

import (
	"fmt"
)

// AuthorizeBrowserCaptureDirectory checks the complete capture directory,
// including canonical ancestors and denied descendants, before trusted exports.
func AuthorizeBrowserCaptureDirectory(directory, baseDir string, allowed, blocked []string) error {
	base := canonicalPath(baseDir)
	directory, _, err := resolveBrowserArtifactPath(directory, base)
	if err != nil {
		return err
	}
	directory = canonicalPath(directory)
	if directory == base || !pathWithin(directory, base) || !pathAllowedForArtifactWrite(directory, base, allowed) {
		return fmt.Errorf("capture directory is outside granted workspace paths")
	}
	for _, value := range blocked {
		value, _, err := resolveBrowserArtifactPath(value, base)
		if err != nil {
			return err
		}
		value = canonicalPath(value)
		if pathWithin(directory, value) || pathWithin(value, directory) {
			return fmt.Errorf("capture directory intersects a blocked workspace path")
		}
	}
	return nil
}
