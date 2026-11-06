package utils

import "io/fs"

// ProjectDirectoryMode preserves a project root's existing slot-group policy
// for directories the service creates later. Setgid alone inherits the group,
// but a 0755 mkdir still removes that group's write access.
func ProjectDirectoryMode(root fs.FileInfo) fs.FileMode {
	if root.Mode()&fs.ModeSetgid != 0 && root.Mode().Perm()&0o020 != 0 {
		return fs.ModeSetgid | 0o770
	}
	return 0o755
}

// ProjectFileMode is ProjectDirectoryMode for a new file in folder: a slot's shell runs as the folder's group, so a
// service-made file there must be writable by that group, as the slot's own files are. Never writable by others.
func ProjectFileMode(folder fs.FileInfo) fs.FileMode {
	if ProjectDirectoryMode(folder)&fs.ModeSetgid != 0 {
		return 0o660
	}
	return 0o644
}
