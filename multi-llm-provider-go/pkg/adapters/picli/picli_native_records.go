package picli

// NativeRecordPath resolves only this platform-owned runtime directory.
func NativeRecordPath(workingDir, sessionID string) string {
	if workingDir == "" || sessionID == "" {
		return ""
	}
	_, dir := piSessionRuntimeDirs(workingDir, sessionID)
	return latestPiTranscriptPathInDir(dir, sessionID)
}
