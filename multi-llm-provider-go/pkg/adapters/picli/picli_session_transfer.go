package picli

// SessionStoreDir is the folder Pi writes one native session's journal into
// (its --session-dir) for a launch whose runtime folder is runtimeDir (the
// call's CLI runtime folder, or the working directory when it has none). The
// native session transfer reads and writes the journal there.
func SessionStoreDir(runtimeDir, nativeSessionID string) string {
	_, sessionDir := piSessionRuntimeDirs(runtimeDir, nativeSessionID)
	return sessionDir
}

// CommandAvailable reports whether this computer can start Pi (installed, PI_BIN
// or the npx fallback), without starting it.
func CommandAvailable() error {
	_, _, err := piCommandPrefix()
	return err
}
