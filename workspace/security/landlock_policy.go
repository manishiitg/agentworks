package security

const landlockRunnerName = "video-studio-landlock-runner"

// LandlockPolicy is serialized by the workspace service and consumed by the
// dedicated Linux launcher. Keeping restriction setup in the child avoids
// irreversibly sandboxing the long-lived Go server process.
type LandlockPolicy struct {
	ReadPaths  []string `json:"read_paths"`
	WritePaths []string `json:"write_paths"`
	WorkDir    string   `json:"work_dir"`
	// PrivateTmp: the launcher was started in its own user and mount
	// namespaces and must give the command a private /tmp (Linux).
	PrivateTmp bool `json:"private_tmp,omitempty"`
	// BrowserScoped: the command's own browser socket folder and profile are
	// in WritePaths, so the shared browser folders are not granted.
	BrowserScoped bool `json:"browser_scoped,omitempty"`
}

// SandboxCapability is safe to expose from the health endpoint. Detail must
// describe capabilities only; it must never include policy paths or secrets.
type SandboxCapability struct {
	Available bool   `json:"available"`
	Backend   string `json:"backend,omitempty"`
	Detail    string `json:"detail,omitempty"`
}
