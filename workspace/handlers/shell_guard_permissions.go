package handlers

import "os"

// Called only after resolveGuardWritePath accepted this workspace directory.
// The kernel grant and Unix permissions must both allow the user's slot to write.
func prepareGuardWriteDirectory(path, root string, slotted bool) error {
	mode := os.FileMode(0755)
	if slotted {
		mode = 0770
	}
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return nil // Existing file grants stay as-is.
	}
	if err := os.MkdirAll(path, mode); err != nil {
		return err
	}
	if !slotted {
		return nil
	}
	return makeSlotWriteDirectoryUsable(path, root)
}
