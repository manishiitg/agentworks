package security

const landlockRunnerName = "video-studio-landlock-runner"

// LandlockPolicy is serialized by the workspace service and consumed by the
// dedicated Linux launcher. Keeping restriction setup in the child avoids
// irreversibly sandboxing the long-lived Go server process.
type LandlockPolicy struct {
	ReadPaths  []string `json:"read_paths"`
	WritePaths []string `json:"write_paths"`
	WorkDir    string   `json:"work_dir"`
	// ListPaths may be listed (folder names only, no file reads). Coding
	// CLIs get "/": Muse opens every folder from / down to its workspace.
	ListPaths []string `json:"list_paths,omitempty"`
	// PrivateTmp: the launcher was started in its own user and mount
	// namespaces and must give the command a private /tmp (Linux).
	PrivateTmp bool `json:"private_tmp,omitempty"`
	// PrivatePTS gives the command its own terminal devices before granting PTY access.
	PrivatePTS bool `json:"private_pts,omitempty"`
	// BrowserScoped: the command's own browser socket folder and profile are
	// in WritePaths, so the shared browser folders are not granted. A command
	// run as a slot is always browser scoped and gets no browser folder at all
	// (PLAT-478).
	BrowserScoped bool `json:"browser_scoped,omitempty"`
	// ReadOnlyOverlays are blocked-write paths inside a writable path.
	// Landlock rules only add access, so it cannot take write back from a
	// subpath; the launcher bind-mounts each one read-only in its own mount
	// namespace instead. Requires PrivateTmp (the namespaces).
	ReadOnlyOverlays []string `json:"read_only_overlays,omitempty"`
	// HiddenPaths are blocked paths inside a granted (readable or writable) path. Landlock cannot take access back
	// from a subpath either, so the launcher mounts an empty, unreadable, read-only placeholder over each one in its
	// own mount namespace: the command can neither read nor change the real file or folder.
	HiddenPaths []string `json:"hidden_paths,omitempty"`
	// PrivateRoots are folders the command must not see into at all (a slot's tmux sockets, PLAT-480): the launcher
	// mounts an empty tmpfs over each in its own mount namespace and binds back only the policy paths (read, write,
	// working folder) that lie inside it. Requires PrivateTmp (the namespaces).
	PrivateRoots []string `json:"private_roots,omitempty"`
	// ShellWrap makes every shell the command starts run under one more Landlock layer (PLAT-872-84). Requires
	// PrivateTmp (the namespaces).
	ShellWrap *ShellWrap `json:"shell_wrap,omitempty"`
}

// ShellWrap: inside the command's mount namespace the launcher binds the real shells to a private path and mounts
// itself over them. A coding CLI that runs /bin/bash or /bin/sh (they ignore $SHELL) then starts the launcher, which
// removes ScrubEnv from the environment, adds the Inner layer and runs the real shell. The CLI keeps what its own
// layer grants (its logins); the commands it runs do not.
type ShellWrap struct {
	// Wrapper is this launcher's own binary, mounted over each shell.
	Wrapper string `json:"wrapper"`
	// Shells are the shell paths to wrap; empty means bash, sh and dash in /usr/bin and /bin.
	Shells []string `json:"shells,omitempty"`
	// Inner is the layer each wrapped shell runs under: the CLI's grants without its logins.
	Inner LandlockPolicy `json:"inner"`
	// ScrubEnv are environment names removed before the shell runs (the CLI's login tokens).
	ScrubEnv []string `json:"scrub_env,omitempty"`
}

// SandboxCapability is safe to expose from the health endpoint. Detail must
// describe capabilities only; it must never include policy paths or secrets.
type SandboxCapability struct {
	Available bool   `json:"available"`
	Backend   string `json:"backend,omitempty"`
	Detail    string `json:"detail,omitempty"`
}
