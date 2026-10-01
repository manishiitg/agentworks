package server

// The reader and shared-directory flows are still in the code (project_sharing.go) and keep their tests,
// which run with project sharing switched on. The default (off) is tested in project_sharing_test.go,
// which switches it back off explicitly.
func init() {
	projectSharingEnabled = func() bool { return true }
}
