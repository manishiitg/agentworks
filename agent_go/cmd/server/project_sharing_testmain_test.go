package server

// Tests run with project sharing on (the default) regardless of the environment. Sharing off
// (AGENTWORKS_PROJECT_SHARING=off) is tested in project_sharing_test.go, which switches it off explicitly.
func init() {
	projectSharingEnabled = func() bool { return true }
}
